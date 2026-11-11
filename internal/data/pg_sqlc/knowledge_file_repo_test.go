package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"grape/internal/biz"
)

// 文件介质仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* 时跳过）：
// kb_file CRUD 与软删、跨租户 RLS 隔离、kb_document_file_chk 约束拒绝
// 与 file 条目原件事实列的回读。

func knowledgeTestFile() *biz.KnowledgeFile {
	return &biz.KnowledgeFile{
		Etag:     "0123456789abcdef0123456789abcdef",
		Sha256:   "ab3f9c02d1f76623052a26129dc7b2b4f5a076c838bd7b1c8e6bd41c23c9d7f1",
		MimeType: "text/plain",
		FileSize: 1024,
	}
}

func TestKnowledgeFileRepo_file_crud_and_soft_delete(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeFileRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
	ctx := context.Background()

	created, err := repo.CreateFile(ctx, auth, knowledgeTestFile())
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if created.ID <= 0 || created.ObjectKey != fmt.Sprintf("kb-file/%d", created.ID) {
		t.Fatalf("created file = %+v, want server id and derived object key", created)
	}
	if created.MimeType != "text/plain" || created.FileSize != 1024 || created.CreatedBy != 11 || created.UpdatedBy != 11 {
		t.Fatalf("created file facts = %+v, want input facts with operator identity", created)
	}

	got, err := repo.GetFile(ctx, auth, created.ID)
	if err != nil || got.Etag != created.Etag || got.Sha256 != created.Sha256 ||
		got.MimeType != created.MimeType || got.FileSize != created.FileSize {
		t.Fatalf("get file = (%+v, %v), want round-trip media facts", got, err)
	}

	if err := repo.SoftDeleteFile(ctx, auth, created.ID); err != nil {
		t.Fatalf("soft delete file: %v", err)
	}
	if _, err := repo.GetFile(ctx, auth, created.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("get deleted file = %v, want ErrDocumentNotFound", err)
	}
	if err := repo.SoftDeleteFile(ctx, auth, created.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("delete already deleted file = %v, want ErrDocumentNotFound", err)
	}
}

func TestKnowledgeFileRepo_tenant_isolation(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeFileRepo(d)
	authA := biz.Identity{TenantID: "t-test-01", UserID: 11}
	authB := biz.Identity{TenantID: "t-test-02", UserID: 5}
	ctx := context.Background()

	file, err := repo.CreateFile(ctx, authA, knowledgeTestFile())
	if err != nil {
		t.Fatalf("create file for tenant A: %v", err)
	}

	// 跨租户读等同不存在。
	if _, err := repo.GetFile(ctx, authB, file.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("tenant B get file = %v, want ErrDocumentNotFound", err)
	}
	// 跨租户软删同口径（先锁后写不命中，行不受影响）。
	if err := repo.SoftDeleteFile(ctx, authB, file.ID); !errors.Is(err, biz.ErrDocumentNotFound) {
		t.Fatalf("tenant B soft delete file = %v, want ErrDocumentNotFound", err)
	}
	if _, err := repo.GetFile(ctx, authA, file.ID); err != nil {
		t.Fatalf("tenant A get file after failed cross-tenant delete: %v", err)
	}
}

func TestKnowledgeRepo_file_document_without_facts_rejected_by_check(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
	ctx := context.Background()

	lib, err := repo.CreateLibrary(ctx, auth, knowledgeTestLibrary("文件约束库"))
	if err != nil {
		t.Fatalf("create library: %v", err)
	}

	// kind='file' 缺 file_name / mime_type：kb_document_file_chk 拒绝，
	// 映射 ErrDocumentInvalidArgument（content 为空不触发 text_chk，
	// 拒绝只能来自 file_chk）。
	if _, err := repo.CreateDocument(ctx, auth, &biz.KnowledgeDocument{
		LibraryID: lib.ID,
		Kind:      biz.DocumentKindFile,
		Title:     "missing file facts",
		Status:    biz.KnowledgeStatusEnabled,
	}); !errors.Is(err, biz.ErrDocumentInvalidArgument) {
		t.Fatalf("create file document without facts = %v, want ErrDocumentInvalidArgument", err)
	}

	// kind='text' 不受 file_chk 限制：正文非空即可落库。
	if _, err := repo.CreateDocument(ctx, auth, knowledgeTestDocument(lib.ID, "text without file facts")); err != nil {
		t.Fatalf("create text document without file facts: %v", err)
	}
}

func TestKnowledgeRepo_file_document_round_trips_original_facts(t *testing.T) {
	d := setupTestDB(t)
	repo := NewKnowledgeRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 11}
	ctx := context.Background()

	lib, err := repo.CreateLibrary(ctx, auth, knowledgeTestLibrary("原件事实库"))
	if err != nil {
		t.Fatalf("create library: %v", err)
	}

	fileID := int64(990001)
	fileName := "notes.txt"
	mimeType := "text/plain"
	fileSize := int64(2048)
	created, err := repo.CreateDocument(ctx, auth, &biz.KnowledgeDocument{
		LibraryID: lib.ID,
		Kind:      biz.DocumentKindFile,
		Title:     "原件事实条目",
		Status:    biz.KnowledgeStatusEnabled,
		FileID:    &fileID,
		FileName:  &fileName,
		MimeType:  &mimeType,
		FileSize:  &fileSize,
	})
	if err != nil {
		t.Fatalf("create file document: %v", err)
	}
	got, err := repo.GetDocument(ctx, auth, created.ID)
	if err != nil {
		t.Fatalf("get file document: %v", err)
	}
	if got.FileID == nil || *got.FileID != fileID || got.FileName == nil || *got.FileName != fileName ||
		got.MimeType == nil || *got.MimeType != mimeType || got.FileSize == nil || *got.FileSize != fileSize {
		t.Fatalf("file facts = (%v, %v, %v, %v), want inserted original facts",
			got.FileID, got.FileName, got.MimeType, got.FileSize)
	}
}
