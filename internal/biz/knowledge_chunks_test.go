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
		{ID: "2#10", Embedder: "space@v1", Content: "tenth", Metadata: map[string]any{"anchor": "runes 90-95", "chunk_index": float64(10)}},
		{ID: "2#2", Embedder: "space@v1", Content: "second", Metadata: map[string]any{"anchor": "runes 10-15", "chunk_index": float64(2)}},
		{ID: "2#0", Embedder: "space@v1", Content: "first", Metadata: map[string]any{"anchor": "runes 0-5", "chunk_index": float64(0)}},
	}}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(2)}, store)

	chunks, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 2)
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	want := []*KnowledgeChunk{
		{ID: "2#0", Index: 0, Anchor: "runes 0-5", Content: "first", Embedder: "space@v1"},
		{ID: "2#2", Index: 2, Anchor: "runes 10-15", Content: "second", Embedder: "space@v1"},
		{ID: "2#10", Index: 10, Anchor: "runes 90-95", Content: "tenth", Embedder: "space@v1"},
	}
	if !reflect.DeepEqual(chunks, want) {
		t.Fatalf("chunks = %+v, want index order %+v", chunks, want)
	}
}

func TestListDocumentChunks_filters_by_document_id_condition(t *testing.T) {
	store := &chunkListStore{}
	uc := newChunkListUsecase(t, &libraryFakeRepo{doc: chunkDoc(7)}, store)

	if _, err := uc.ListDocumentChunks(ingestCtx(), libraryAuth(), 7); err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	want := []MetadataCondition{{Key: "document_id", Op: VectorMetadataEq, Values: []any{"7"}}}
	if store.filter == nil || !reflect.DeepEqual(store.filter.Conditions, want) {
		t.Fatalf("filter = %+v, want document_id eq 7", store.filter)
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

func TestChunkIndexOf_prefers_id_suffix_then_metadata(t *testing.T) {
	for name, tc := range map[string]struct {
		id       string
		metadata map[string]any
		want     int
	}{
		"id suffix":              {"12#7", nil, 7},
		"id suffix beats meta":   {"12#7", map[string]any{"chunk_index": float64(3)}, 7},
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
