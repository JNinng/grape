package biz

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// 知识库切分区分串的用例测试：库级字段的缺省、显式空串、更新保持与长度
// 校验，以及重建索引时库级区分串透传到摄取管线。

// libraryFakeRepo 是知识库用例单测的仓储替身：记录写入的库，读取返回
// 预置的库与条目；未覆盖的方法沿用嵌入接口零值（触达即 panic）。
type libraryFakeRepo struct {
	KnowledgeRepo
	existing *KnowledgeLibrary
	doc      *KnowledgeDocument
	created  *KnowledgeLibrary
	updated  *KnowledgeLibrary
}

func (r *libraryFakeRepo) GetLibrary(context.Context, Identity, int64) (*KnowledgeLibrary, error) {
	if r.existing == nil {
		return nil, ErrLibraryNotFound
	}
	return r.existing, nil
}

func (r *libraryFakeRepo) CreateLibrary(_ context.Context, _ Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error) {
	r.created = l
	return l, nil
}

func (r *libraryFakeRepo) UpdateLibrary(_ context.Context, _ Identity, l *KnowledgeLibrary) (*KnowledgeLibrary, error) {
	r.updated = l
	return l, nil
}

func (r *libraryFakeRepo) ListDocumentsByLibrary(context.Context, Identity, int64, int32, int32) (*KnowledgeDocumentPage, error) {
	return &KnowledgeDocumentPage{}, nil
}

func (r *libraryFakeRepo) GetDocument(context.Context, Identity, int64) (*KnowledgeDocument, error) {
	return r.doc, nil
}

func (r *libraryFakeRepo) MarkDocumentIndexed(context.Context, Identity, int64, string) (*KnowledgeDocument, error) {
	return r.doc, nil
}

func strPtr(s string) *string { return &s }

func newLibraryUsecaseForTest(t *testing.T, repo KnowledgeRepo, store *mockVectorStore) *KnowledgeUsecase {
	t.Helper()
	ingest, err := NewDocumentUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4})
	if err != nil {
		t.Fatalf("NewDocumentUsecase: %v", err)
	}
	return NewKnowledgeUsecase(repo, nil, store, ingest)
}

func libraryAuth() Identity { return Identity{TenantID: "t-kb", UserID: 3} }

func TestNormalizeChunking_delimiter_unset_takes_default_and_explicit_values_kept(t *testing.T) {
	for name, tc := range map[string]struct {
		in   *string
		want string
	}{
		"unset takes default": {nil, defaultChunkDelimiter},
		"empty kept":          {strPtr(""), ""},
		"custom kept":         {strPtr("---"), "---"},
	} {
		l := KnowledgeLibrary{ChunkDelimiter: tc.in}
		normalizeChunking(&l)
		if l.ChunkDelimiter == nil || *l.ChunkDelimiter != tc.want {
			t.Fatalf("%s: delimiter = %v, want %q", name, l.ChunkDelimiter, tc.want)
		}
	}
}

func TestCreateLibrary_without_delimiter_persists_default(t *testing.T) {
	repo := &libraryFakeRepo{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

	if _, err := uc.CreateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{Name: "hr", Status: KnowledgeStatusUnspecified}); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if got := *repo.created.ChunkDelimiter; got != "\n\n" {
		t.Fatalf("persisted delimiter = %q, want default blank-line delimiter", got)
	}
}

func TestCreateLibrary_with_empty_delimiter_persists_empty(t *testing.T) {
	repo := &libraryFakeRepo{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

	if _, err := uc.CreateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{Name: "hr", Status: KnowledgeStatusUnspecified, ChunkDelimiter: strPtr("")}); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if got := *repo.created.ChunkDelimiter; got != "" {
		t.Fatalf("persisted delimiter = %q, want empty (delimiter split disabled)", got)
	}
}

func TestCreateLibrary_with_delimiter_at_rune_limit_persists_value(t *testing.T) {
	repo := &libraryFakeRepo{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})
	atLimit := strings.Repeat("。", maxChunkDelimiterRunes)

	if _, err := uc.CreateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{Name: "hr", Status: KnowledgeStatusUnspecified, ChunkDelimiter: &atLimit}); err != nil {
		t.Fatalf("CreateLibrary(32 runes) = %v, want accepted", err)
	}
	if got := *repo.created.ChunkDelimiter; got != atLimit {
		t.Fatalf("persisted delimiter = %q, want the 32-rune value unchanged", got)
	}
}

func TestCreateLibrary_with_oversize_delimiter_rejected(t *testing.T) {
	repo := &libraryFakeRepo{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})
	oversize := strings.Repeat("。", maxChunkDelimiterRunes+1)

	_, err := uc.CreateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{Name: "hr", Status: KnowledgeStatusUnspecified, ChunkDelimiter: &oversize})
	if !errors.Is(err, ErrLibraryInvalidArgument) {
		t.Fatalf("CreateLibrary(33 runes) = %v, want ErrLibraryInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatal("repo written despite invalid delimiter")
	}
}

func TestUpdateLibrary_without_delimiter_keeps_existing_value(t *testing.T) {
	repo := &libraryFakeRepo{existing: &KnowledgeLibrary{ID: 9, ChunkDelimiter: strPtr("")}}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

	if _, err := uc.UpdateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{ID: 9, Name: "hr", Status: KnowledgeStatusEnabled, ChunkSize: 800}); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if got := *repo.updated.ChunkDelimiter; got != "" {
		t.Fatalf("persisted delimiter = %q, want existing empty value kept", got)
	}
}

func TestUpdateLibrary_with_delimiter_overwrites(t *testing.T) {
	repo := &libraryFakeRepo{existing: &KnowledgeLibrary{ID: 9, ChunkDelimiter: strPtr("")}}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

	if _, err := uc.UpdateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{ID: 9, Name: "hr", Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkDelimiter: strPtr("|")}); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if got := *repo.updated.ChunkDelimiter; got != "|" {
		t.Fatalf("persisted delimiter = %q, want |", got)
	}
}

func TestUpdateLibrary_without_delimiter_for_missing_library_returns_not_found(t *testing.T) {
	uc := newLibraryUsecaseForTest(t, &libraryFakeRepo{}, &mockVectorStore{})

	_, err := uc.UpdateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{ID: 9, Name: "hr", Status: KnowledgeStatusEnabled, ChunkSize: 800})
	if !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("UpdateLibrary = %v, want ErrLibraryNotFound", err)
	}
}

func TestReindexDocument_uses_library_delimiter_value_for_chunking(t *testing.T) {
	for name, tc := range map[string]struct {
		delimiter string
		want      []string
	}{
		"custom delimiter splits on it":       {"|", []string{"甲\n\n乙", "丙"}},
		"default delimiter splits paragraphs": {"\n\n", []string{"甲", "乙|丙"}},
		"empty delimiter keeps fixed window":  {"", []string{"甲\n\n乙|丙"}},
	} {
		t.Run(name, func(t *testing.T) {
			content := "甲\n\n乙|丙"
			store := &mockVectorStore{}
			repo := &libraryFakeRepo{
				existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100, ChunkDelimiter: &tc.delimiter},
				doc:      &KnowledgeDocument{ID: 2, LibraryID: 1, Title: "t", Content: &content, Status: KnowledgeStatusEnabled},
			}
			uc := newLibraryUsecaseForTest(t, repo, store)

			if _, err := uc.ReindexDocument(ingestCtx(), libraryAuth(), 2); err != nil {
				t.Fatalf("ReindexDocument: %v", err)
			}
			got := make([]string, 0, len(store.added))
			for _, d := range store.added {
				got = append(got, d.Content)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("chunk contents = %q, want %q", got, tc.want)
			}
		})
	}
}
