package biz

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// 条目实际分片查询用例：分片来自向量存储（不是回放切分算法），按分片序
// 返回；条目缺失 fail-closed，设施未启用原样透传。

// chunkListStore 是分片查询用例的向量存储替身：List 返回可编程结果并记录
// 过滤条件；其余方法沿用 mockVectorStore。
type chunkListStore struct {
	mockVectorStore
	docs   []*VectorDocument
	err    error
	calls  int
	filter *VectorFilter
}

func (s *chunkListStore) List(_ context.Context, filter *VectorFilter) ([]*VectorDocument, error) {
	s.calls++
	s.filter = filter
	return s.docs, s.err
}

func newChunkListUsecase(t *testing.T, repo KnowledgeRepo, store VectorStore) *KnowledgeUsecase {
	t.Helper()
	// 分片查询先判条目所在库可见（库级可见性）：替身未预置库时补一个
	// 与本测试条目同库的可见库，保持用例聚焦分片排序与过滤。
	if fake, ok := repo.(*libraryFakeRepo); ok && fake.existing == nil {
		fake.existing = &KnowledgeLibrary{ID: 1, Status: KnowledgeStatusEnabled}
	}
	uc := newLibraryUsecaseForTest(t, repo, &mockVectorStore{})
	uc.store = store
	return uc
}

func chunkDoc(id int64) *KnowledgeDocument {
	content := "body"
	return &KnowledgeDocument{ID: id, LibraryID: 1, Title: "t", Content: &content, Status: KnowledgeStatusEnabled}
}

func TestListDocumentChunks_returns_chunks_ordered_by_index(t *testing.T) {
	store := &chunkListStore{docs: []*VectorDocument{
		{ID: "2#10#11111111", Embedder: "space@v1", Content: "tenth", Metadata: map[string]any{"anchor": "runes 90-95", "chunk_index": float64(10)}},
		{ID: "2#2#22222222", Embedder: "space@v1", Content: "second", Metadata: map[string]any{"anchor": "runes 10-15", "chunk_index": float64(2)}},
		{ID: "2#0#33333333", Embedder: "space@v1", Content: "first", Metadata: map[string]any{"anchor": "runes 0-5", "chunk_index": float64(0)}},
	}}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	chunks, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 2)
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	want := []*KnowledgeChunk{
		{ID: "2#0#33333333", Index: 0, Anchor: "runes 0-5", Content: "first", Embedder: "space@v1"},
		{ID: "2#2#22222222", Index: 2, Anchor: "runes 10-15", Content: "second", Embedder: "space@v1"},
		{ID: "2#10#11111111", Index: 10, Anchor: "runes 90-95", Content: "tenth", Embedder: "space@v1"},
	}
	if !reflect.DeepEqual(chunks, want) {
		t.Fatalf("chunks = %+v, want index order %+v", chunks, want)
	}
	// 管理读面排除退役行：枚举条件必须同时带「键缺失」（未被 superseded）。
	// 条件集合的顺序不是契约，按 key 断言。
	if store.filter == nil || len(store.filter.Conditions) != 2 {
		t.Fatalf("list conditions = %+v, want document equality + superseded is-null", store.filter)
	}
	if cond := conditionByKey(store.filter.Conditions, VectorMetadataSupersededAt); cond.Op != VectorMetadataIsNull {
		t.Fatalf("superseded condition = %+v, want is-null on %s", cond, VectorMetadataSupersededAt)
	}
}

// TestListDocumentChunks_when_library_invisible_returns_document_not_found 验证
// 分片读面随所属库可见性收窄：库不可见时不触达向量枚举，统一 NotFound。
func TestListDocumentChunks_when_library_invisible_returns_document_not_found(t *testing.T) {
	store := &chunkListStore{}
	uc := newLibraryUsecaseForTest(t, &libraryFakeRepo{doc: chunkDoc(2)}, &mockVectorStore{})
	uc.store = store

	_, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 2)
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("ListDocumentChunks(invisible library) = %v, want ErrDocumentNotFound", err)
	}
	if store.calls != 0 {
		t.Fatalf("chunk enumeration calls = %d, want 0", store.calls)
	}
}

func TestListDocumentChunks_filters_by_document_id_condition(t *testing.T) {
	store := &chunkListStore{}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(7)}, store)

	if _, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 7); err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	if store.filter == nil || len(store.filter.Conditions) != 2 {
		t.Fatalf("filter = %+v, want document equality + superseded is-null", store.filter)
	}
	docCond := conditionByKey(store.filter.Conditions, "document_id")
	if docCond.Op != VectorMetadataEq || !reflect.DeepEqual(docCond.Values, []any{"7"}) {
		t.Fatalf("document condition = %+v, want eq 7", docCond)
	}
	if cond := conditionByKey(store.filter.Conditions, VectorMetadataSupersededAt); cond.Op != VectorMetadataIsNull {
		t.Fatalf("superseded condition = %+v, want is-null", cond)
	}
}

func TestListDocumentChunks_no_chunks_returns_empty_slice(t *testing.T) {
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(3)}, &chunkListStore{})

	chunks, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 3)
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	if chunks == nil || len(chunks) != 0 {
		t.Fatalf("chunks = %#v, want empty non-nil slice", chunks)
	}
}

func TestListDocumentChunks_invalid_id_returns_invalid_argument(t *testing.T) {
	store := &chunkListStore{}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(3)}, store)

	for _, id := range []int64{0, -1} {
		if _, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), id); !errors.Is(err, ErrDocumentInvalidArgument) {
			t.Fatalf("ListDocumentChunks(%d) = %v, want ErrDocumentInvalidArgument", id, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestListDocumentChunks_missing_document_returns_not_found(t *testing.T) {
	store := &chunkListStore{}
	uc := newChunkListUsecase(t, &libraryFakeRepo{}, store)

	if _, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 9); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("ListDocumentChunks(missing) = %v, want ErrDocumentNotFound", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 (document gate first)", store.calls)
	}
}

func TestListDocumentChunks_vector_store_disabled_propagates(t *testing.T) {
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(3)}, &chunkListStore{err: ErrVectorStoreDisabled})

	if _, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 3); !errors.Is(err, ErrVectorStoreDisabled) {
		t.Fatalf("ListDocumentChunks = %v, want ErrVectorStoreDisabled", err)
	}
}

// chunkDetailStore 是分片反查用例的向量存储替身：Get 返回可编程结果并
// 记录收到的切片段；其余方法沿用 mockVectorStore。
type chunkDetailStore struct {
	mockVectorStore
	doc   *VectorDocument
	err   error
	gotID string
}

func (s *chunkDetailStore) Get(_ context.Context, id string) (*VectorDocument, error) {
	s.gotID = id
	return s.doc, s.err
}

func knownChunk(id string, index int, documentID string) *VectorDocument {
	return &VectorDocument{
		ID:       id,
		Embedder: "space@v1",
		Content:  "chunk body",
		Metadata: map[string]any{
			"document_id": documentID,
			"anchor":      "runes 10-20",
			"chunk_index": float64(index),
		},
	}
}

func TestGetDocumentChunk_current_id_form_resolves_source_and_returns_detail(t *testing.T) {
	store := &chunkDetailStore{doc: knownChunk("2#1#a1b2c3d4", 1, "2")}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	detail, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#1#a1b2c3d4")
	if err != nil {
		t.Fatalf("GetDocumentChunk: %v", err)
	}
	if store.gotID != "2#1#a1b2c3d4" {
		t.Fatalf("store Get id = %q, want full chunk id", store.gotID)
	}
	if detail.Document == nil || detail.Document.ID != 2 {
		t.Fatalf("document = %+v, want source document 2", detail.Document)
	}
	want := &KnowledgeChunk{ID: "2#1#a1b2c3d4", Index: 1, Anchor: "runes 10-20", Content: "chunk body", Embedder: "space@v1"}
	if !reflect.DeepEqual(detail.Chunk, want) {
		t.Fatalf("chunk = %+v, want %+v", detail.Chunk, want)
	}
	if detail.SupersededAt != "" {
		t.Fatalf("superseded_at = %q, want empty for current chunk", detail.SupersededAt)
	}
}

func TestGetDocumentChunk_legacy_id_form_resolves_source(t *testing.T) {
	store := &chunkDetailStore{doc: knownChunk("2#7", 7, "2")}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	detail, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#7")
	if err != nil {
		t.Fatalf("GetDocumentChunk: %v", err)
	}
	if detail.Chunk.Index != 7 {
		t.Fatalf("index = %d, want 7", detail.Chunk.Index)
	}
	if store.gotID != "2#7" {
		t.Fatalf("store Get id = %q, want legacy id", store.gotID)
	}
}

func TestGetDocumentChunk_invalid_id_returns_invalid_argument(t *testing.T) {
	for name, id := range map[string]string{
		"empty":           "",
		"no separator":    "2",
		"non-numeric":     "abc#1",
		"zero source":     "0#1",
		"negative":        "-1#1",
		"missing index":   "2#",
		"non-numeric idx": "2#x",
		"bad hash":        "2#1#zzzzzzzz",
		"extra segment":   "2#1#a1b2c3d4#9",
	} {
		store := &chunkDetailStore{}
		uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)
		if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), id); !errors.Is(err, ErrDocumentInvalidArgument) {
			t.Fatalf("%s: GetDocumentChunk(%q) = %v, want ErrDocumentInvalidArgument", name, id, err)
		}
		if store.gotID != "" {
			t.Fatalf("%s: store Get called for invalid id %q", name, id)
		}
	}
}

func TestGetDocumentChunk_missing_document_returns_not_found_without_store_read(t *testing.T) {
	store := &chunkDetailStore{doc: knownChunk("9#0#a1b2c3d4", 0, "9")}
	uc := newChunkListUsecase(t, &libraryFakeRepo{}, store)

	if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "9#0#a1b2c3d4"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("GetDocumentChunk(missing document) = %v, want ErrDocumentNotFound", err)
	}
	if store.gotID != "" {
		t.Fatalf("store Get called before document visibility gate")
	}
}

func TestGetDocumentChunk_when_library_invisible_returns_not_found(t *testing.T) {
	repo := &libraryFakeRepo{doc: chunkDoc(2)}
	store := &chunkDetailStore{doc: knownChunk("2#0#a1b2c3d4", 0, "2")}
	uc := newChunkListUsecase(t, repo, store)
	repo.existing = nil // 库不可见（GetLibrary 返回 ErrLibraryNotFound）

	if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#0#a1b2c3d4"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("GetDocumentChunk(invisible library) = %v, want ErrDocumentNotFound", err)
	}
	if store.gotID != "" {
		t.Fatalf("store Get called for invisible library")
	}
}

func TestGetDocumentChunk_missing_chunk_returns_not_found(t *testing.T) {
	store := &chunkDetailStore{err: ErrVectorDocNotFound}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#0#a1b2c3d4"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("GetDocumentChunk(missing chunk) = %v, want ErrDocumentNotFound", err)
	}
}

func TestGetDocumentChunk_document_id_mismatch_returns_not_found(t *testing.T) {
	store := &chunkDetailStore{doc: knownChunk("2#0#a1b2c3d4", 0, "9")}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#0#a1b2c3d4"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("GetDocumentChunk(mismatched document_id) = %v, want ErrDocumentNotFound", err)
	}
}

func TestGetDocumentChunk_superseded_chunk_exposes_retirement_time(t *testing.T) {
	doc := knownChunk("2#0#a1b2c3d4", 0, "2")
	doc.Metadata[VectorMetadataSupersededAt] = "2026-02-03T04:05:06Z"
	store := &chunkDetailStore{doc: doc}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	detail, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#0#a1b2c3d4")
	if err != nil {
		t.Fatalf("GetDocumentChunk: %v", err)
	}
	if detail.SupersededAt != "2026-02-03T04:05:06Z" {
		t.Fatalf("superseded_at = %q, want retirement time", detail.SupersededAt)
	}
}

func TestGetDocumentChunk_vector_store_disabled_propagates(t *testing.T) {
	store := &chunkDetailStore{err: ErrVectorStoreDisabled}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	if _, err := uc.GetDocumentChunk(ingestCtx(), libraryAuth(), "2#0#a1b2c3d4"); !errors.Is(err, ErrVectorStoreDisabled) {
		t.Fatalf("GetDocumentChunk = %v, want ErrVectorStoreDisabled", err)
	}
}

func TestChunkIndexOf_prefers_id_suffix_then_metadata(t *testing.T) {
	for name, tc := range map[string]struct {
		id       string
		metadata map[string]any
		want     int
	}{
		"id suffix":              {"12#7", nil, 7},
		"id suffix beats meta":   {"12#7", map[string]any{"chunk_index": float64(3)}, 7},
		"hashed id index":        {"12#7#a1b2c3d4", nil, 7},
		"hashed id beats meta":   {"12#7#a1b2c3d4", map[string]any{"chunk_index": float64(3)}, 7},
		"metadata float64":       {"no-suffix", map[string]any{"chunk_index": float64(4)}, 4},
		"metadata int":           {"no-suffix", map[string]any{"chunk_index": 5}, 5},
		"metadata string":        {"no-suffix", map[string]any{"chunk_index": "6"}, 6},
		"unparseable falls to 0": {"12#x", nil, 0},
	} {
		if got := chunkIndexOf(tc.id, tc.metadata); got != tc.want {
			t.Fatalf("%s: chunkIndexOf = %d, want %d", name, got, tc.want)
		}
	}
}

func TestChunkIndexOf_nil_metadata_does_not_panic(t *testing.T) {
	if got := chunkIndexOf("no-suffix", nil); got != 0 {
		t.Fatalf("chunkIndexOf = %d, want 0", got)
	}
	_ = context.Background()
}
