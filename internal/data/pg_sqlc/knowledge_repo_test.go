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
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
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
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	authA := biz.Identity{TenantID: "t-test-01", UserID: 11}
	authB := biz.Identity{TenantID: "t-test-02", UserID: 5}
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
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
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
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
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

func TestKnowledgeRepo_library_custom_chunk_delimiter_round_trips(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
	ctx := context.Background()

	lib := knowledgeTestLibrary("自定义区分串")
	custom := "——\t。"
	lib.ChunkDelimiter = &custom
	created, err := repo.CreateLibrary(ctx, auth, lib)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	got, err := repo.GetLibrary(ctx, auth, created.ID)
	if err != nil || *got.ChunkDelimiter != custom {
		t.Fatalf("get library delimiter = (%v, %v), want %q", got, err, custom)
	}
}

func TestKnowledgeRepo_library_oversize_chunk_delimiter_rejected_by_check(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}

	lib := knowledgeTestLibrary("超长区分串")
	oversize := strings.Repeat("-", 33)
	lib.ChunkDelimiter = &oversize
	if _, err := repo.CreateLibrary(context.Background(), auth, lib); !errors.Is(err, biz.ErrLibraryInvalidArgument) {
		t.Fatalf("create with 33-char delimiter = %v, want ErrLibraryInvalidArgument", err)
	}
}

func TestKnowledgeRepo_library_inserted_without_delimiter_defaults_to_blank_line(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
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

// TestKnowledgeRepo_library_visibility_scope 验证库级可见性口径：归属三元组
// 经数据范围推导（种子用户 11 全租户、用户 2 仅本人），单行读、写前锁定与
// 动态 List 共用同一判定——不可见与不存在统一 NotFound，可见集合只含可见行。
func TestKnowledgeRepo_library_visibility_scope(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	ctx := context.Background()
	admin := biz.Identity{TenantID: "t-test-01", UserID: 11} // 全租户数据范围
	scoped := biz.Identity{TenantID: "t-test-01", UserID: 2} // 仅本人

	adminLib, err := repo.CreateLibrary(ctx, admin, knowledgeTestLibrary("管理员可见库"))
	if err != nil {
		t.Fatalf("create admin library: %v", err)
	}
	userLib, err := repo.CreateLibrary(ctx, scoped, knowledgeTestLibrary("普通用户库"))
	if err != nil {
		t.Fatalf("create scoped library: %v", err)
	}
	userLib2, err := repo.CreateLibrary(ctx, scoped, knowledgeTestLibrary("普通用户库二"))
	if err != nil {
		t.Fatalf("create second scoped library: %v", err)
	}

	// 单行读：不可见与不存在统一 NotFound。
	if _, err := repo.GetLibrary(ctx, scoped, adminLib.ID); !errors.Is(err, biz.ErrLibraryNotFound) {
		t.Fatalf("scoped get admin library = %v, want ErrLibraryNotFound", err)
	}
	// 写前锁定同口径。
	renamed := *adminLib
	renamed.Name = "管理员可见库改名"
	if _, err := repo.UpdateLibrary(ctx, scoped, &renamed); !errors.Is(err, biz.ErrLibraryNotFound) {
		t.Fatalf("scoped update admin library = %v, want ErrLibraryNotFound", err)
	}
	if err := repo.SoftDeleteLibrary(ctx, scoped, adminLib.ID); !errors.Is(err, biz.ErrLibraryNotFound) {
		t.Fatalf("scoped delete admin library = %v, want ErrLibraryNotFound", err)
	}

	// 可见集合：普通用户只含本人库，管理员含全部。
	scopedVisible, err := repo.ListVisibleLibraries(ctx, scoped)
	if err != nil {
		t.Fatalf("list visible as scoped: %v", err)
	}
	if !visibleIDsEqual(scopedVisible, []int64{userLib.ID, userLib2.ID}) {
		t.Fatalf("scoped visible libraries = %v, want both own libraries", libraryIDsOf(scopedVisible))
	}
	adminVisible, err := repo.ListVisibleLibraries(ctx, admin)
	if err != nil {
		t.Fatalf("list visible as admin: %v", err)
	}
	if !visibleIDsEqual(adminVisible, []int64{adminLib.ID, userLib.ID, userLib2.ID}) {
		t.Fatalf("admin visible libraries = %v, want all three", libraryIDsOf(adminVisible))
	}

	// 动态 List 与可见集合同口径。
	list, err := repo.ListLibraries(ctx, scoped, &dynq.Query{})
	if err != nil {
		t.Fatalf("scoped dynamic list: %v", err)
	}
	for _, item := range list.Items {
		if item.ID == adminLib.ID {
			t.Fatalf("scoped dynamic list leaked invisible library %d", adminLib.ID)
		}
	}
	if len(list.Items) != 2 {
		t.Fatalf("scoped dynamic list = %v, want both own libraries", libraryIDsOf(list.Items))
	}

	// 条目列表随所属库可见性收窄：不可见库的条目不进入列表与计数。
	adminDoc, err := repo.CreateDocument(ctx, admin, knowledgeTestDocument(adminLib.ID, "管理员条目"))
	if err != nil {
		t.Fatalf("create admin document: %v", err)
	}
	scopedDoc, err := repo.CreateDocument(ctx, scoped, knowledgeTestDocument(userLib.ID, "普通条目"))
	if err != nil {
		t.Fatalf("create scoped document: %v", err)
	}
	scopedDoc2, err := repo.CreateDocument(ctx, scoped, knowledgeTestDocument(userLib2.ID, "普通条目二"))
	if err != nil {
		t.Fatalf("create second scoped document: %v", err)
	}
	docs, err := repo.ListDocuments(ctx, scoped, &dynq.Query{})
	if err != nil {
		t.Fatalf("scoped document list: %v", err)
	}
	scopedDocIDs := map[int64]struct{}{}
	for _, doc := range docs.Items {
		scopedDocIDs[doc.ID] = struct{}{}
	}
	if len(docs.Items) != 2 || docs.Total == nil || *docs.Total != 2 {
		t.Fatalf("scoped document list = %d total=%v, want 2 own documents", len(docs.Items), docs.Total)
	}
	if _, ok := scopedDocIDs[adminDoc.ID]; ok {
		t.Fatalf("scoped document list leaked invisible document %d", adminDoc.ID)
	}
	adminDocs, err := repo.ListDocuments(ctx, admin, &dynq.Query{})
	if err != nil {
		t.Fatalf("admin document list: %v", err)
	}
	if len(adminDocs.Items) != 3 {
		t.Fatalf("admin document list size = %d, want 3 (every library visible)", len(adminDocs.Items))
	}
	for _, doc := range adminDocs.Items {
		if doc.ID != adminDoc.ID && doc.ID != scopedDoc.ID && doc.ID != scopedDoc2.ID {
			t.Fatalf("admin document list contains unexpected row %d", doc.ID)
		}
	}

	// 客户端声明的库过滤与可见集合求交：混合列表只保留请求范围内且可见
	// 的部分（可见但未请求的第二库必须被排除），仅不可见库的条件得到空页
	// （空交集短路，List 与 Count 同口径）。
	mixed, err := repo.ListDocuments(ctx, scoped, &dynq.Query{Where: []dynq.Condition{
		{Field: "libraryId", Op: dynq.OpIn, Value: []any{adminLib.ID, userLib.ID}},
	}})
	if err != nil {
		t.Fatalf("scoped document list with mixed filter: %v", err)
	}
	if len(mixed.Items) != 1 || mixed.Items[0].ID != scopedDoc.ID || mixed.Total == nil || *mixed.Total != 1 {
		t.Fatalf("mixed filter list = %+v total=%v, want only document %d",
			mixed.Items, mixed.Total, scopedDoc.ID)
	}
	invisibleOnly, err := repo.ListDocuments(ctx, scoped, &dynq.Query{Where: []dynq.Condition{
		{Field: "libraryId", Op: dynq.OpIn, Value: []any{adminLib.ID}},
	}})
	if err != nil {
		t.Fatalf("scoped document list with invisible filter: %v", err)
	}
	if invisibleOnly.Items == nil || len(invisibleOnly.Items) != 0 ||
		invisibleOnly.Total == nil || *invisibleOnly.Total != 0 {
		t.Fatalf("invisible-only filter list = %+v total=%v, want empty non-nil page with total 0",
			invisibleOnly.Items, invisibleOnly.Total)
	}
	byEquals, err := repo.ListDocuments(ctx, scoped, &dynq.Query{Where: []dynq.Condition{
		{Field: "libraryId", Op: dynq.OpEq, Value: userLib.ID},
	}})
	if err != nil {
		t.Fatalf("scoped document list with eq filter: %v", err)
	}
	if len(byEquals.Items) != 1 || byEquals.Items[0].ID != scopedDoc.ID {
		t.Fatalf("eq filter list = %+v, want only document %d", byEquals.Items, scopedDoc.ID)
	}

	// 软删库退出可见集合与条目列表：不可见对所有可见者生效。
	if err := repo.SoftDeleteLibrary(ctx, admin, userLib.ID); err != nil {
		t.Fatalf("soft delete scoped library: %v", err)
	}
	scopedVisible, err = repo.ListVisibleLibraries(ctx, scoped)
	if err != nil || !visibleIDsEqual(scopedVisible, []int64{userLib2.ID}) {
		t.Fatalf("scoped visible after delete = %v (%v), want only %d",
			libraryIDsOf(scopedVisible), err, userLib2.ID)
	}
	adminVisible, err = repo.ListVisibleLibraries(ctx, admin)
	if err != nil || !visibleIDsEqual(adminVisible, []int64{adminLib.ID, userLib2.ID}) {
		t.Fatalf("admin visible after delete = %v (%v), want the two remaining libraries",
			libraryIDsOf(adminVisible), err)
	}
	afterDelete, err := repo.ListDocuments(ctx, scoped, &dynq.Query{})
	if err != nil {
		t.Fatalf("scoped document list after delete: %v", err)
	}
	if len(afterDelete.Items) != 1 || afterDelete.Items[0].ID != scopedDoc2.ID ||
		afterDelete.Total == nil || *afterDelete.Total != 1 {
		t.Fatalf("scoped document list after delete = %+v total=%v, want only document %d",
			afterDelete.Items, afterDelete.Total, scopedDoc2.ID)
	}
}

func libraryIDsOf(libs []*biz.KnowledgeLibrary) []int64 {
	ids := make([]int64, 0, len(libs))
	for _, lib := range libs {
		ids = append(ids, lib.ID)
	}
	return ids
}

func visibleIDsEqual(libs []*biz.KnowledgeLibrary, want []int64) bool {
	if len(libs) != len(want) {
		return false
	}
	got := make(map[int64]struct{}, len(libs))
	for _, lib := range libs {
		got[lib.ID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			return false
		}
	}
	return true
}
