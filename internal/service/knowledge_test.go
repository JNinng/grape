package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	knowledgev1 "grape/api/knowledge/v1"
	"grape/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// searchFakeStore 是检索端点单测的向量存储替身：只支撑 Search，记录
// 收到的过滤参数，结果与错误可编程。
type searchFakeStore struct {
	biz.VectorStore
	results []*biz.ScoredVectorDocument
	err     error
	filter  *biz.VectorFilter
}

func (s *searchFakeStore) Search(_ context.Context, _ []float32, filter *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	s.filter = filter
	return s.results, s.err
}

// searchFakeEmbedder 产出固定 2 维向量，embedErr 注入端点故障。
type searchFakeEmbedder struct{ embedErr error }

func (e searchFakeEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	if e.embedErr != nil {
		return nil, e.embedErr
	}
	return []float32{1, 0}, nil
}

func (searchFakeEmbedder) Dimension(string) (int, error) { return 2, nil }

// searchFakeResolver 返回固定默认空间。
type searchFakeResolver struct{}

func (searchFakeResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	return "space@v1", 2, nil
}

func (searchFakeResolver) ResolveEmbeddingSpace(context.Context, string) (biz.EmbeddingSpace, error) {
	return biz.EmbeddingSpace{Key: "space@v1", Dimension: 2}, nil
}

// searchFakeLibraries 是可见库集合替身：固定返回库列表。
type searchFakeLibraries struct {
	libraries []*biz.KnowledgeLibrary
}

func (l searchFakeLibraries) VisibleLibraries(context.Context, biz.Identity) ([]*biz.KnowledgeLibrary, error) {
	return l.libraries, nil
}

func newSearchTestService(t *testing.T, store biz.VectorStore, embedder biz.Embedder) *KnowledgeService {
	t.Helper()
	return newSearchTestServiceWithLibraries(t, store, embedder, searchFakeLibraries{
		libraries: []*biz.KnowledgeLibrary{{ID: 3}},
	})
}

func newSearchTestServiceWithLibraries(t *testing.T, store biz.VectorStore, embedder biz.Embedder, libraries biz.KnowledgeLibraryDirectory) *KnowledgeService {
	t.Helper()
	search, err := biz.NewKnowledgeSearchUsecase(store, embedder, searchFakeResolver{}, libraries)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	return NewKnowledgeService(nil, search)
}

func searchIdentityCtx() context.Context {
	return biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
}

// assertKratosError 断言错误的 HTTP 状态码与领域 reason。
func assertKratosError(t *testing.T, err error, code int, reason string) {
	t.Helper()
	e := kratoserrors.FromError(err)
	if e == nil || int(e.Code) != code || e.Reason != reason {
		t.Fatalf("error = %v, want %d %s", err, code, reason)
	}
}

func TestSearchDocuments_valid_request_returns_mapped_hits_and_total(t *testing.T) {
	store := &searchFakeStore{results: []*biz.ScoredVectorDocument{{
		Document: &biz.VectorDocument{ID: "12#0", Content: "年假 5 天", Metadata: map[string]any{
			"source_id": "12", "source_name": "员工手册", "anchor": "runes 0-6",
		}},
		Score: 0.8,
	}}}
	svc := newSearchTestService(t, store, searchFakeEmbedder{})

	reply, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "年假", LibraryId: 3, TopK: 4})
	if err != nil {
		t.Fatalf("SearchDocuments: %v", err)
	}
	if reply.GetTotal() != 1 || len(reply.GetItems()) != 1 {
		t.Fatalf("reply total/items = %d/%d, want 1/1", reply.GetTotal(), len(reply.GetItems()))
	}
	hit := reply.GetItems()[0]
	if hit.GetDocumentId() != "12#0" || hit.GetSourceId() != "12" || hit.GetSourceName() != "员工手册" ||
		hit.GetAnchor() != "runes 0-6" || hit.GetScore() != 0.8 || hit.GetContent() != "年假 5 天" {
		t.Fatalf("hit = %+v, want mapped fields", hit)
	}
}

func TestSearchDocuments_request_fields_reach_store_filter(t *testing.T) {
	store := &searchFakeStore{}
	svc := newSearchTestService(t, store, searchFakeEmbedder{})

	if _, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q", LibraryId: 3, TopK: 4}); err != nil {
		t.Fatalf("SearchDocuments: %v", err)
	}
	last := store.filter.Conditions[len(store.filter.Conditions)-1]
	if store.filter.Limit != 4 || last.Key != "kb_id" || last.Op != biz.VectorMetadataIn || last.Values[0] != "3" {
		t.Fatalf("filter = %+v, want limit 4 and kb_id IN [3]", store.filter)
	}
}

func TestSearchDocuments_invisible_library_returns_404(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{}, searchFakeEmbedder{})

	_, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q", LibraryId: 99})
	assertKratosError(t, err, http.StatusNotFound, knowledgev1.ErrorReason_LIBRARY_NOT_FOUND.String())
}

func TestSearchDocuments_no_hits_returns_empty_items(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{}, searchFakeEmbedder{})

	reply, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q"})
	if err != nil {
		t.Fatalf("SearchDocuments: %v", err)
	}
	if reply.GetTotal() != 0 || len(reply.GetItems()) != 0 {
		t.Fatalf("reply = %+v, want empty", reply)
	}
}

func TestSearchDocuments_without_identity_returns_401(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{}, searchFakeEmbedder{})

	_, err := svc.SearchDocuments(context.Background(), &knowledgev1.SearchDocumentsRequest{Query: "q"})
	assertKratosError(t, err, http.StatusUnauthorized, "SESSION_INVALID")
}

func TestSearchDocuments_invalid_argument_returns_400(t *testing.T) {
	for name, req := range map[string]*knowledgev1.SearchDocumentsRequest{
		"blank query":      {Query: "   "},
		"oversize query":   {Query: strings.Repeat("q", 513)},
		"negative top_k":   {Query: "q", TopK: -1},
		"negative library": {Query: "q", LibraryId: -2},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newSearchTestService(t, &searchFakeStore{}, searchFakeEmbedder{})

			_, err := svc.SearchDocuments(searchIdentityCtx(), req)
			assertKratosError(t, err, http.StatusBadRequest, knowledgev1.ErrorReason_SEARCH_INVALID_ARGUMENT.String())
		})
	}
}

func TestSearchDocuments_embedder_disabled_returns_503(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{}, searchFakeEmbedder{embedErr: biz.ErrEmbedderDisabled})

	_, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q"})
	assertKratosError(t, err, http.StatusServiceUnavailable, knowledgev1.ErrorReason_SEARCH_UNAVAILABLE.String())
}

func TestSearchDocuments_vector_store_disabled_returns_503(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{err: biz.ErrVectorStoreDisabled}, searchFakeEmbedder{})

	_, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q"})
	assertKratosError(t, err, http.StatusServiceUnavailable, knowledgev1.ErrorReason_SEARCH_UNAVAILABLE.String())
}

func TestSearchDocuments_store_failure_returns_500_without_detail(t *testing.T) {
	svc := newSearchTestService(t, &searchFakeStore{err: errors.New("pq: connection refused at 10.0.0.5")}, searchFakeEmbedder{})

	_, err := svc.SearchDocuments(searchIdentityCtx(), &knowledgev1.SearchDocumentsRequest{Query: "q"})
	assertKratosError(t, err, http.StatusInternalServerError, knowledgev1.ErrorReason_SEARCH_FAILED.String())
	if e := kratoserrors.FromError(err); e.Message != "knowledge search failed" {
		t.Fatalf("message = %q, want fixed text without storage detail", e.Message)
	}
}

// libraryFakeRepo 是知识库端点单测的仓储替身：写入原样回显，读取返回
// 预置行；未覆盖的方法沿用嵌入接口零值。
type libraryFakeRepo struct {
	biz.KnowledgeRepo
	existing *biz.KnowledgeLibrary
	doc      *biz.KnowledgeDocument
}

func (r *libraryFakeRepo) GetDocument(context.Context, biz.Identity, int64) (*biz.KnowledgeDocument, error) {
	if r.doc == nil {
		return nil, biz.ErrDocumentNotFound
	}
	return r.doc, nil
}

func (r *libraryFakeRepo) GetLibrary(context.Context, biz.Identity, int64) (*biz.KnowledgeLibrary, error) {
	return r.existing, nil
}

func (r *libraryFakeRepo) CreateLibrary(_ context.Context, _ biz.Identity, l *biz.KnowledgeLibrary) (*biz.KnowledgeLibrary, error) {
	return l, nil
}

func (r *libraryFakeRepo) UpdateLibrary(_ context.Context, _ biz.Identity, l *biz.KnowledgeLibrary) (*biz.KnowledgeLibrary, error) {
	return l, nil
}

func (r *libraryFakeRepo) ListDocumentsByLibrary(context.Context, biz.Identity, int64, int32, int32) (*biz.KnowledgeDocumentPage, error) {
	return &biz.KnowledgeDocumentPage{}, nil
}

func newLibraryTestService(repo biz.KnowledgeRepo) *KnowledgeService {
	return NewKnowledgeService(biz.NewKnowledgeUsecase(repo, nil, nil, nil), nil)
}

func libraryStatus(v int32) *int32 { return &v }

func libraryDelimiter(v string) *string { return &v }

func TestCreateLibrary_without_chunk_delimiter_returns_default(t *testing.T) {
	svc := newLibraryTestService(&libraryFakeRepo{})

	out, err := svc.CreateLibrary(searchIdentityCtx(), &knowledgev1.CreateLibraryRequest{Library: &knowledgev1.Library{Name: "hr"}})
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if out.ChunkDelimiter == nil || out.GetChunkDelimiter() != "\n\n" {
		t.Fatalf("chunk delimiter = %v, want default blank-line delimiter", out.ChunkDelimiter)
	}
}

func TestCreateLibrary_with_empty_chunk_delimiter_returns_explicit_empty(t *testing.T) {
	svc := newLibraryTestService(&libraryFakeRepo{})

	out, err := svc.CreateLibrary(searchIdentityCtx(), &knowledgev1.CreateLibraryRequest{Library: &knowledgev1.Library{
		Name: "hr", ChunkDelimiter: libraryDelimiter(""),
	}})
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if out.ChunkDelimiter == nil || out.GetChunkDelimiter() != "" {
		t.Fatalf("chunk delimiter = %v, want explicit empty", out.ChunkDelimiter)
	}
}

func TestCreateLibrary_with_oversize_chunk_delimiter_returns_400(t *testing.T) {
	svc := newLibraryTestService(&libraryFakeRepo{})

	_, err := svc.CreateLibrary(searchIdentityCtx(), &knowledgev1.CreateLibraryRequest{Library: &knowledgev1.Library{
		Name: "hr", ChunkDelimiter: libraryDelimiter(strings.Repeat("-", 33)),
	}})
	assertKratosError(t, err, http.StatusBadRequest, knowledgev1.ErrorReason_LIBRARY_INVALID_ARGUMENT.String())
}

func TestUpdateLibrary_without_chunk_delimiter_keeps_existing(t *testing.T) {
	svc := newLibraryTestService(&libraryFakeRepo{existing: &biz.KnowledgeLibrary{ID: 9, ChunkDelimiter: libraryDelimiter("---")}})

	out, err := svc.UpdateLibrary(searchIdentityCtx(), &knowledgev1.UpdateLibraryRequest{Library: &knowledgev1.Library{
		Id: 9, Name: "hr", Status: libraryStatus(1), ChunkSize: 800,
	}})
	if err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if out.GetChunkDelimiter() != "---" {
		t.Fatalf("chunk delimiter = %q, want existing ---", out.GetChunkDelimiter())
	}
}

// chunkListStore 是分片查询端点单测的向量存储替身：List 记录过滤条件并
// 返回可编程结果。
type chunkListStore struct {
	biz.VectorStore
	docs []*biz.VectorDocument
	err  error
}

func (s *chunkListStore) List(context.Context, *biz.VectorFilter) ([]*biz.VectorDocument, error) {
	return s.docs, s.err
}

func newChunksTestService(store biz.VectorStore) *KnowledgeService {
	repo := &libraryFakeRepo{existing: &biz.KnowledgeLibrary{ID: 1, Status: biz.KnowledgeStatusEnabled}, doc: &biz.KnowledgeDocument{
		ID: 2, LibraryID: 1, Kind: "text", Title: "t", Status: biz.KnowledgeStatusEnabled,
	}}
	return NewKnowledgeService(biz.NewKnowledgeUsecase(repo, nil, store, nil), nil)
}

func TestListDocumentChunks_maps_chunks_in_order(t *testing.T) {
	store := &chunkListStore{docs: []*biz.VectorDocument{
		{ID: "2#1", Embedder: "space@v1", Content: "second", Metadata: map[string]any{"anchor": "runes 10-16"}},
		{ID: "2#0", Embedder: "space@v1", Content: "first", Metadata: map[string]any{"anchor": "runes 0-6"}},
	}}
	svc := newChunksTestService(store)

	reply, err := svc.ListDocumentChunks(searchIdentityCtx(), &knowledgev1.ListDocumentChunksRequest{Id: 2})
	if err != nil {
		t.Fatalf("ListDocumentChunks: %v", err)
	}
	if reply.GetTotal() != 2 {
		t.Fatalf("total = %d, want 2", reply.GetTotal())
	}
	first := reply.GetItems()[0]
	if first.GetIndex() != 0 || first.GetChunkId() != "2#0" || first.GetAnchor() != "runes 0-6" ||
		first.GetContent() != "first" || first.GetEmbedder() != "space@v1" {
		t.Fatalf("first chunk = %+v, want mapped fields", first)
	}
	if reply.GetItems()[1].GetIndex() != 1 {
		t.Fatalf("second index = %d, want 1", reply.GetItems()[1].GetIndex())
	}
}

func TestListDocumentChunks_without_identity_returns_401(t *testing.T) {
	svc := newChunksTestService(&chunkListStore{})

	_, err := svc.ListDocumentChunks(context.Background(), &knowledgev1.ListDocumentChunksRequest{Id: 2})
	assertKratosError(t, err, http.StatusUnauthorized, "SESSION_INVALID")
}

func TestListDocumentChunks_invalid_id_returns_400(t *testing.T) {
	svc := newChunksTestService(&chunkListStore{})

	_, err := svc.ListDocumentChunks(searchIdentityCtx(), &knowledgev1.ListDocumentChunksRequest{Id: 0})
	assertKratosError(t, err, http.StatusBadRequest, knowledgev1.ErrorReason_DOCUMENT_INVALID_ARGUMENT.String())
}

func TestListDocumentChunks_vector_store_disabled_returns_503(t *testing.T) {
	svc := newChunksTestService(&chunkListStore{err: biz.ErrVectorStoreDisabled})

	_, err := svc.ListDocumentChunks(searchIdentityCtx(), &knowledgev1.ListDocumentChunksRequest{Id: 2})
	assertKratosError(t, err, http.StatusServiceUnavailable, knowledgev1.ErrorReason_DOCUMENT_CHUNKS_UNAVAILABLE.String())
}

func TestListDocumentChunks_store_failure_returns_500_without_detail(t *testing.T) {
	svc := newChunksTestService(&chunkListStore{err: errors.New("pq: password authentication failed for user grape_u")})

	_, err := svc.ListDocumentChunks(searchIdentityCtx(), &knowledgev1.ListDocumentChunksRequest{Id: 2})
	assertKratosError(t, err, http.StatusInternalServerError, knowledgev1.ErrorReason_DOCUMENT_CHUNKS_FAILED.String())
	if e := kratoserrors.FromError(err); e.Message != "knowledge document chunks failed" {
		t.Fatalf("message = %q, want fixed text without storage detail", e.Message)
	}
}
