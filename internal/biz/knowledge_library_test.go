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

// TestGetDocument_invisible_library_maps_to_document_not_found 验证条目读面
// 随所属库可见性收窄：库不可见与条目不存在对外同一形态（不泄露条目存在性）。
func TestGetDocument_invisible_library_maps_to_document_not_found(t *testing.T) {
	uc := newLibraryUsecaseForTest(t, &libraryFakeRepo{doc: chunkDoc(2)}, &mockVectorStore{})

	if _, err := uc.GetDocument(context.Background(), libraryAuth(), 2); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("GetDocument with invisible library = %v, want ErrDocumentNotFound", err)
	}
}

// recordingMetadataStore 记录 chunk metadata 同步与按条件删除的调用次数：
// 用于断言可见性判定发生在副作用之前。
type recordingMetadataStore struct {
	mockVectorStore
	metadataCalls int
	deleteCalls   int
}

func (s *recordingMetadataStore) UpdateMetadata(context.Context, *VectorFilter, map[string]any) (int64, error) {
	s.metadataCalls++
	return 0, nil
}

func (s *recordingMetadataStore) DeleteByFilter(context.Context, *VectorFilter) (int64, error) {
	s.deleteCalls++
	return 0, nil
}

// visibilityFakeRepo 在 libraryFakeRepo 之上模拟写路径的可见性判定边界：
// 记录库内条目枚举与更新调用，UpdateLibrary 模拟仓储写前锁定对不可见库
// 返回 NotFound。用于断言不可见库不会先产生 chunk 副作用再被拒绝。
type visibilityFakeRepo struct {
	libraryFakeRepo
	docs         []*KnowledgeDocument
	listDocCalls int
	updateCalls  int
	count        int64
	countCalls   int
}

func (r *visibilityFakeRepo) ListDocumentsByLibrary(context.Context, Identity, int64, int32, int32) (*KnowledgeDocumentPage, error) {
	r.listDocCalls++
	return &KnowledgeDocumentPage{Items: r.docs}, nil
}

func (r *visibilityFakeRepo) UpdateLibrary(context.Context, Identity, *KnowledgeLibrary) (*KnowledgeLibrary, error) {
	r.updateCalls++
	return nil, ErrLibraryNotFound
}

func (r *visibilityFakeRepo) CountDocumentsByLibrary(context.Context, Identity, int64) (int64, error) {
	r.countCalls++
	return r.count, nil
}

// TestUpdateLibrary_when_library_invisible_rejects_before_chunk_sync 验证显式
// 携带切分区分串也不能绕过可见性：不可见库在 chunk metadata 同步之前即
// 按 NotFound 拒绝。
func TestUpdateLibrary_when_library_invisible_rejects_before_chunk_sync(t *testing.T) {
	repo := &visibilityFakeRepo{
		libraryFakeRepo: libraryFakeRepo{}, // existing 为空：库不可见
		docs:            []*KnowledgeDocument{chunkDoc(2)},
	}
	store := &recordingMetadataStore{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})
	uc.store = store
	delimiter := "|"

	_, err := uc.UpdateLibrary(ingestCtx(), libraryAuth(), &KnowledgeLibrary{
		ID: 9, Name: "hr", Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkDelimiter: &delimiter,
	})
	if !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("UpdateLibrary(invisible) = %v, want ErrLibraryNotFound", err)
	}
	if repo.listDocCalls != 0 || store.metadataCalls != 0 {
		t.Fatalf("list/metadata calls = %d/%d, want 0/0 (rejected before side effects)",
			repo.listDocCalls, store.metadataCalls)
	}
}

// TestDeleteLibrary_when_library_invisible_rejects_before_count_and_chunk_clear
// 验证删除前置：不可见库不做条目计数与 chunk 清理（避免 409/404 探测与
// 越权清理）。
func TestDeleteLibrary_when_library_invisible_rejects_before_count_and_chunk_clear(t *testing.T) {
	repo := &visibilityFakeRepo{libraryFakeRepo: libraryFakeRepo{}, count: 3}
	store := &recordingMetadataStore{}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})
	uc.store = store

	err := uc.DeleteLibrary(ingestCtx(), libraryAuth(), 9)
	if !errors.Is(err, ErrLibraryNotFound) {
		t.Fatalf("DeleteLibrary(invisible) = %v, want ErrLibraryNotFound (not ErrLibraryHasDocuments)", err)
	}
	if repo.countCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("count/delete calls = %d/%d, want 0/0 (rejected before side effects)",
			repo.countCalls, store.deleteCalls)
	}
}

// TestUpdateDocument_when_library_invisible_rejects_before_field_validation 验证
// 条目写面：不可见库统一 DocumentNotFound，先于请求字段校验，避免用
// 400/404 差异探测不可见库中的条目。
func TestUpdateDocument_when_library_invisible_rejects_before_field_validation(t *testing.T) {
	uc := newLibraryUsecaseForTest(t, &libraryFakeRepo{doc: chunkDoc(2)}, &mockVectorStore{})

	// 请求自带不匹配的 library_id：可见库时应为参数非法，不可见库时必须是
	// NotFound（校验顺序不能泄露条目存在性）。
	_, err := uc.UpdateDocument(ingestCtx(), libraryAuth(), &KnowledgeDocument{
		ID: 2, LibraryID: 999, Kind: "text", Title: "t", Status: KnowledgeStatusEnabled,
	})
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("UpdateDocument(invisible library) = %v, want ErrDocumentNotFound", err)
	}
}

// TestDeleteDocument_when_library_invisible_rejects_before_chunk_clear 验证
// 条目删除：库不可见时不清理 chunk。
func TestDeleteDocument_when_library_invisible_rejects_before_chunk_clear(t *testing.T) {
	store := &recordingMetadataStore{}
	uc := newLibraryUsecaseForTest(t, &libraryFakeRepo{doc: chunkDoc(2)}, &mockVectorStore{})
	uc.store = store

	err := uc.DeleteDocument(ingestCtx(), libraryAuth(), 2)
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("DeleteDocument(invisible library) = %v, want ErrDocumentNotFound", err)
	}
	if store.deleteCalls != 0 {
		t.Fatalf("chunk clear calls = %d, want 0", store.deleteCalls)
	}
}

// libraryFakeRepo 是知识库用例单测的仓储替身：记录写入的库与条目，读取返回
// 预置的库与条目；未覆盖的方法沿用嵌入接口零值（触达即 panic）。
type libraryFakeRepo struct {
	KnowledgeRepo
	existing   *KnowledgeLibrary
	doc        *KnowledgeDocument
	created    *KnowledgeLibrary
	updated    *KnowledgeLibrary
	createdDoc *KnowledgeDocument
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
	if r.doc == nil {
		return nil, ErrDocumentNotFound
	}
	return r.doc, nil
}

func (r *libraryFakeRepo) CreateDocument(_ context.Context, _ Identity, d *KnowledgeDocument) (*KnowledgeDocument, error) {
	r.createdDoc = d
	if d.ID == 0 {
		d.ID = 2
	}
	return d, nil
}

func (r *libraryFakeRepo) MarkDocumentIndexed(context.Context, Identity, int64, string) (*KnowledgeDocument, error) {
	if r.createdDoc != nil {
		return r.createdDoc, nil
	}
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

// kind 校验矩阵：创建接受 text / markdown（缺省 text，markdown 切片带
// heading_path），file 与未知取值拒绝；更新路径 kind 不可变。

func TestCreateDocument_accepts_text_markdown_and_defaults_text(t *testing.T) {
	for name, tc := range map[string]struct {
		kind     string
		wantKind string
	}{
		"empty defaults to text": {"", DocumentKindText},
		"text accepted":          {DocumentKindText, DocumentKindText},
		"markdown accepted":      {DocumentKindMarkdown, DocumentKindMarkdown},
	} {
		t.Run(name, func(t *testing.T) {
			content := "# 标题\n\n正文"
			store := &mockVectorStore{}
			repo := &libraryFakeRepo{
				existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100},
			}
			uc := newLibraryUsecaseForTest(t, repo, store)

			created, err := uc.CreateDocument(ingestCtx(), libraryAuth(), &KnowledgeDocument{
				LibraryID: 1, Kind: tc.kind, Title: "t", Content: &content, Status: KnowledgeStatusEnabled,
			})
			if err != nil {
				t.Fatalf("CreateDocument(kind=%q): %v", tc.kind, err)
			}
			if created.Kind != tc.wantKind {
				t.Fatalf("created kind = %q, want %q", created.Kind, tc.wantKind)
			}
			if repo.createdDoc.Kind != tc.wantKind {
				t.Fatalf("persisted kind = %q, want %q", repo.createdDoc.Kind, tc.wantKind)
			}
			// markdown 条目切片带 heading_path，text 条目保持既有 metadata 形状。
			_, hasPath := store.added[0].Metadata["heading_path"]
			if tc.wantKind == DocumentKindMarkdown && !hasPath {
				t.Fatalf("markdown chunk metadata = %v, want heading_path", store.added[0].Metadata)
			}
			if tc.wantKind == DocumentKindText && hasPath {
				t.Fatalf("text chunk metadata = %v, want no heading_path", store.added[0].Metadata)
			}
		})
	}
}

func TestCreateDocument_with_file_or_unknown_kind_rejected(t *testing.T) {
	for _, kind := range []string{DocumentKindFile, "html"} {
		repo := &libraryFakeRepo{
			existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100},
		}
		uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

		content := "c"
		_, err := uc.CreateDocument(ingestCtx(), libraryAuth(), &KnowledgeDocument{
			LibraryID: 1, Kind: kind, Title: "t", Content: &content, Status: KnowledgeStatusEnabled,
		})
		if !errors.Is(err, ErrDocumentKindUnsupported) {
			t.Fatalf("CreateDocument(kind=%q) = %v, want ErrDocumentKindUnsupported", kind, err)
		}
		if repo.createdDoc != nil {
			t.Fatalf("repo written despite unsupported kind %q", kind)
		}
	}
}

func TestUpdateDocument_with_changed_kind_rejected(t *testing.T) {
	content := "c"
	repo := &libraryFakeRepo{
		existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100},
		doc: &KnowledgeDocument{
			ID: 2, LibraryID: 1, Kind: DocumentKindText, Title: "t", Content: &content, Status: KnowledgeStatusEnabled,
		},
	}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})

	_, err := uc.UpdateDocument(ingestCtx(), libraryAuth(), &KnowledgeDocument{
		ID: 2, Kind: DocumentKindMarkdown, Title: "t", Content: &content, Status: KnowledgeStatusEnabled,
	})
	if !errors.Is(err, ErrDocumentInvalidArgument) {
		t.Fatalf("UpdateDocument(kind changed) = %v, want ErrDocumentInvalidArgument", err)
	}
}

func TestReindexDocument_markdown_doc_chunks_carry_heading_path(t *testing.T) {
	content := "# 部署\n\n数据库正文"
	store := &mockVectorStore{}
	repo := &libraryFakeRepo{
		existing: &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled, ChunkSize: 800, ChunkOverlap: 100},
		doc: &KnowledgeDocument{
			ID: 2, LibraryID: 1, Kind: DocumentKindMarkdown, Title: "t", Content: &content, Status: KnowledgeStatusEnabled,
		},
	}
	uc := newLibraryUsecaseForTest(t, repo, store)

	if _, err := uc.ReindexDocument(ingestCtx(), libraryAuth(), 2); err != nil {
		t.Fatalf("ReindexDocument: %v", err)
	}
	if len(store.added) != 1 || store.added[0].Metadata["heading_path"] != "部署" {
		t.Fatalf("chunks = %+v, want one chunk with heading_path 部署", store.added)
	}
}
