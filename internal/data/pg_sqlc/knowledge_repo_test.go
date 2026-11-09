package pg_sqlc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 知识库与条目仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* 时跳过）：
// CRUD、租户隔离、名称唯一冲突与库内条目分页。

func knowledgeTestLibrary(name string) *biz.KnowledgeLibrary {
	return &biz.KnowledgeLibrary{
		Status:       biz.KnowledgeStatusEnabled,
		Name:         name,
		ChunkSize:    800,
		ChunkOverlap: 100,
	}
}

func knowledgeTestDocument(libraryID int64, title string) *biz.KnowledgeDocument {
	content := "refund policy: full refund within 30 days"
	return &biz.KnowledgeDocument{
		LibraryID: libraryID,
		Kind:      "text",
		Title:     title,
		Content:   &content,
		Status:    biz.KnowledgeStatusEnabled,
	}
}

func TestKnowledgeRepo_library_and_document_crud(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctx := context.Background()

	created, err := repo.CreateLibrary(ctx, auth, knowledgeTestLibrary("仓库手册"))
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	if created.ID <= 0 || created.ChunkSize != 800 || created.ChunkOverlap != 100 {
		t.Fatalf("created library = %+v, want server id and chunk defaults", created)
	}

	// 同名库冲突映射 AlreadyExists。
	if _, err := repo.CreateLibrary(ctx, auth, knowledgeTestLibrary("仓库手册")); !errors.Is(err, biz.ErrLibraryAlreadyExists) {
		t.Fatalf("duplicate library name = %v, want ErrLibraryAlreadyExists", err)
	}

	// 更新状态与名称。
	created.Status = biz.KnowledgeStatusDisabled
	created.Name = "仓库手册（停用）"
	updated, err := repo.UpdateLibrary(ctx, auth, created)
	if err != nil {
		t.Fatalf("update library: %v", err)
	}
	if updated.Status != biz.KnowledgeStatusDisabled || updated.Name != "仓库手册（停用）" {
		t.Fatalf("updated library = %+v, want new status/name", updated)
	}

	doc, err := repo.CreateDocument(ctx, auth, knowledgeTestDocument(created.ID, "退款政策"))
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if doc.ID <= 0 || doc.LibraryID != created.ID || doc.Kind != "text" {
		t.Fatalf("created document = %+v", doc)
	}

	count, err := repo.CountDocumentsByLibrary(ctx, auth, created.ID)
	if err != nil || count != 1 {
		t.Fatalf("count documents = (%d, %v), want 1", count, err)
	}

	page, err := repo.ListDocumentsByLibrary(ctx, auth, created.ID, 20, 0)
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Title != "退款政策" {
		t.Fatalf("document page = %+v, want one 退款政策 row", page)
	}

	if err := repo.SoftDeleteDocument(ctx, auth, doc.ID); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, err := repo.GetDocument(ctx, auth, doc.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("get deleted document = %v, want ErrDocumentNotFound", err)
	}

	if err := repo.SoftDeleteLibrary(ctx, auth, created.ID); err != nil {
		t.Fatalf("delete library: %v", err)
	}
	if _, err := repo.GetLibrary(ctx, auth, created.ID); !errors.Is(err, biz.ErrLibraryNotFound) {
		t.Fatalf("get deleted library = %v, want ErrLibraryNotFound", err)
	}
}

func TestKnowledgeRepo_tenant_isolation(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	authA := biz.Identity{TenantID: "t-test-01", UserID: 1}
	authB := biz.Identity{TenantID: "t-test-02", UserID: 1}
	ctx := context.Background()

	libA, err := repo.CreateLibrary(ctx, authA, knowledgeTestLibrary("隔离测试库"))
	if err != nil {
		t.Fatalf("create library for tenant A: %v", err)
	}
	docA, err := repo.CreateDocument(ctx, authA, knowledgeTestDocument(libA.ID, "A 的条目"))
	if err != nil {
		t.Fatalf("create document for tenant A: %v", err)
	}

	// 跨租户读等同不存在。
	if _, err := repo.GetLibrary(ctx, authB, libA.ID); !errors.Is(err, biz.ErrLibraryNotFound) {
		t.Fatalf("tenant B get library = %v, want ErrLibraryNotFound", err)
	}
	if _, err := repo.GetDocument(ctx, authB, docA.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("tenant B get document = %v, want ErrDocumentNotFound", err)
	}

	// 跨租户动态列表看不到 A 的库。
	list, err := repo.ListLibraries(ctx, authB, &dynq.Query{})
	if err != nil {
		t.Fatalf("tenant B list libraries: %v", err)
	}
	for _, item := range list.Items {
		if item.ID == libA.ID {
			t.Fatalf("tenant B list leaked tenant A library %d", libA.ID)
		}
	}
}

func TestKnowledgeRepo_mark_indexed(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctx := context.Background()

	lib, err := repo.CreateLibrary(ctx, auth, knowledgeTestLibrary("索引回写库"))
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	doc, err := repo.CreateDocument(ctx, auth, knowledgeTestDocument(lib.ID, "索引回写条目"))
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if doc.IndexedAt != nil || doc.IndexedEmbedder != nil {
		t.Fatalf("fresh document already indexed: %+v", doc)
	}

	indexed, err := repo.MarkDocumentIndexed(ctx, auth, doc.ID, "bge-m3@v1")
	if err != nil {
		t.Fatalf("mark indexed: %v", err)
	}
	if indexed.IndexedAt == nil || indexed.IndexedEmbedder == nil || *indexed.IndexedEmbedder != "bge-m3@v1" {
		t.Fatalf("indexed document = %+v, want indexed_at and embedder", indexed)
	}
}

func TestKnowledgeRepo_library_chunk_delimiter_round_trips(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctx := context.Background()

	lib := knowledgeTestLibrary("区分串手册")
	paragraph := "\n\n"
	lib.ChunkDelimiter = &paragraph
	created, err := repo.CreateLibrary(ctx, auth, lib)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	got, err := repo.GetLibrary(ctx, auth, created.ID)
	if err != nil || got.ChunkDelimiter == nil || *got.ChunkDelimiter != "\n\n" {
		t.Fatalf("get library delimiter = (%v, %v), want blank-line delimiter", got, err)
	}

	empty := ""
	got.ChunkDelimiter = &empty
	updated, err := repo.UpdateLibrary(ctx, auth, got)
	if err != nil || *updated.ChunkDelimiter != "" {
		t.Fatalf("update library delimiter = (%v, %v), want explicit empty", updated, err)
	}
}

func TestKnowledgeRepo_library_oversize_chunk_delimiter_rejected_by_check(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}

	lib := knowledgeTestLibrary("超长区分串")
	oversize := strings.Repeat("-", 33)
	lib.ChunkDelimiter = &oversize
	if _, err := repo.CreateLibrary(context.Background(), auth, lib); !errors.Is(err, biz.ErrLibraryInvalidArgument) {
		t.Fatalf("create with 33-char delimiter = %v, want ErrLibraryInvalidArgument", err)
	}
}

func TestKnowledgeRepo_library_inserted_without_delimiter_defaults_to_blank_line(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctx := context.Background()

	const id int64 = 990001
	err := d.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(ctx,
			"INSERT INTO public.kb_library (id, tenant_id, \"name\", created_by, updated_by) VALUES ($1, current_setting('app.tenant_id'), 'default-delimiter', 1, 1)", id)
		return err
	})
	if err != nil {
		t.Fatalf("insert library without delimiter: %v", err)
	}
	got, err := repo.GetLibrary(ctx, auth, id)
	if err != nil || got.ChunkDelimiter == nil || *got.ChunkDelimiter != "\n\n" {
		t.Fatalf("get library delimiter = (%v, %v), want column default blank-line delimiter", got, err)
	}
}
