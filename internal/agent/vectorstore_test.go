package agent

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// 向量适配器单测（纯内存，契约用 fake 替身）：框架形状 ↔ 领域形状的
// 映射、显式 unsupported 的拒绝点、未配置契约的 fail-closed。租户隔离
// 行为属契约实现的职责，由 data 层集成测试覆盖，适配器不重复验证。

// fakeVectorStore 捕获适配器传入的参数，返回预置结果。
type fakeVectorStore struct {
	addDoc    *biz.VectorDocument
	updateDoc *biz.VectorDocument
	getID     string
	getDoc    *biz.VectorDocument
	getErr    error
	deleteID  string
	deleteErr error
	searchEmb []float32
	searchF   *biz.VectorFilter
	searchErr error
	hits      []*biz.ScoredVectorDocument
	delByF    *biz.VectorFilter
	deleted   int64
	countF    *biz.VectorFilter
	count     int64
	metaF     *biz.VectorFilter
	metadata  map[string]map[string]any
}

func (f *fakeVectorStore) Add(_ context.Context, doc *biz.VectorDocument) error {
	f.addDoc = doc
	return nil
}

func (f *fakeVectorStore) Get(_ context.Context, id string) (*biz.VectorDocument, error) {
	f.getID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getDoc, nil
}

func (f *fakeVectorStore) Update(_ context.Context, doc *biz.VectorDocument) error {
	f.updateDoc = doc
	return nil
}

func (f *fakeVectorStore) Delete(_ context.Context, id string) error {
	f.deleteID = id
	return f.deleteErr
}

func (f *fakeVectorStore) Search(_ context.Context, embedding []float32, filter *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	f.searchEmb = embedding
	f.searchF = filter
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.hits, nil
}

func (f *fakeVectorStore) DeleteByFilter(_ context.Context, filter *biz.VectorFilter) (int64, error) {
	f.delByF = filter
	return f.deleted, nil
}

func (f *fakeVectorStore) Count(_ context.Context, filter *biz.VectorFilter) (int64, error) {
	f.countF = filter
	return f.count, nil
}

func (f *fakeVectorStore) GetMetadata(_ context.Context, filter *biz.VectorFilter) (map[string]map[string]any, error) {
	f.metaF = filter
	return f.metadata, nil
}

func TestVectorStoreAdapter_add_maps_document_and_embedding(t *testing.T) {
	fake := &fakeVectorStore{}
	adapter := NewVectorStore(fake)
	err := adapter.Add(context.Background(), &document.Document{
		ID:      "doc-1",
		Name:    "policy",
		Content: "text",
		Metadata: map[string]any{
			"category": "hr",
		},
	}, []float64{1.5, -2.25})
	if err != nil {
		t.Fatalf("Add = %v", err)
	}
	if fake.addDoc == nil || fake.addDoc.ID != "doc-1" || fake.addDoc.Name != "policy" || fake.addDoc.Content != "text" {
		t.Fatalf("document not mapped: %+v", fake.addDoc)
	}
	if got := fake.addDoc.Embedding; len(got) != 2 || got[0] != 1.5 || got[1] != -2.25 {
		t.Fatalf("embedding = %v, want float32 conversion of [1.5 -2.25]", got)
	}
	if fake.addDoc.Metadata["category"] != "hr" {
		t.Fatalf("metadata not mapped: %+v", fake.addDoc.Metadata)
	}

	if err := adapter.Add(context.Background(), nil, []float64{1}); err == nil {
		t.Fatal("Add(nil document) = nil error, want rejection")
	}
	if err := adapter.Add(context.Background(), &document.Document{ID: "doc-1"}, nil); err == nil {
		t.Fatal("Add(empty embedding) = nil error, want rejection")
	}
}

func TestVectorStoreAdapter_get_maps_back_to_framework_document(t *testing.T) {
	fake := &fakeVectorStore{
		getDoc: &biz.VectorDocument{
			ID:        "doc-1",
			Name:      "policy",
			Content:   "text",
			Embedding: []float32{1, 2},
			Metadata:  map[string]any{"tenant_id": "t-a"},
		},
	}
	adapter := NewVectorStore(fake)
	doc, embedding, err := adapter.Get(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	if fake.getID != "doc-1" {
		t.Fatalf("Get id = %q, want doc-1", fake.getID)
	}
	if doc == nil || doc.ID != "doc-1" || doc.Content != "text" {
		t.Fatalf("framework document not mapped: %+v", doc)
	}
	if len(embedding) != 2 || embedding[0] != 1 || embedding[1] != 2 {
		t.Fatalf("embedding = %v, want float64 conversion of [1 2]", embedding)
	}

	fake.getErr = biz.ErrVectorDocNotFound
	if _, _, err := adapter.Get(context.Background(), "missing"); !errors.Is(err, biz.ErrVectorDocNotFound) {
		t.Fatalf("Get(not found) = %v, want biz.ErrVectorDocNotFound passthrough", err)
	}
}

func TestVectorStoreAdapter_search_maps_vector_mode_and_rejects_other_modes(t *testing.T) {
	fake := &fakeVectorStore{
		hits: []*biz.ScoredVectorDocument{
			{Document: &biz.VectorDocument{ID: "doc-1"}, Score: 0.75},
		},
	}
	adapter := NewVectorStore(fake)
	result, err := adapter.Search(context.Background(), &vectorstore.SearchQuery{
		Vector:     []float64{0.5, 0.5},
		Limit:      5,
		MinScore:   0.5,
		SearchMode: vectorstore.SearchModeVector,
		Filter: &vectorstore.SearchFilter{
			IDs:      []string{"doc-1"},
			Metadata: map[string]any{"category": "hr"},
		},
	})
	if err != nil {
		t.Fatalf("Search(vector mode) = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Document.ID != "doc-1" || result.Results[0].Score != 0.75 {
		t.Fatalf("results not mapped: %+v", result.Results)
	}
	if len(fake.searchEmb) != 2 || fake.searchEmb[0] != 0.5 {
		t.Fatalf("query vector not converted: %v", fake.searchEmb)
	}
	if fake.searchF == nil || fake.searchF.Limit != 5 || fake.searchF.MinScore != 0.5 ||
		len(fake.searchF.IDs) != 1 || fake.searchF.Metadata["category"] != "hr" {
		t.Fatalf("filter not mapped: %+v", fake.searchF)
	}

	// keyword/hybrid/filter-only 模式需要 tsvector 与 embedder 管线（D2），
	// 现阶段必须显式拒绝而非静默降级为纯向量检索。
	for _, mode := range []int{vectorstore.SearchModeHybrid, vectorstore.SearchModeKeyword, vectorstore.SearchModeFilter} {
		if _, err := adapter.Search(context.Background(), &vectorstore.SearchQuery{
			Vector:     []float64{1},
			SearchMode: mode,
		}); !errors.Is(err, errVectorUnsupported) {
			t.Fatalf("Search(mode=%d) = %v, want errVectorUnsupported", mode, err)
		}
	}
	// searchfilter 条件 DSL 的翻译归 D2 检索工具，现阶段显式拒绝。
	if _, err := adapter.Search(context.Background(), &vectorstore.SearchQuery{
		Vector:     []float64{1},
		SearchMode: vectorstore.SearchModeVector,
		Filter: &vectorstore.SearchFilter{FilterCondition: &searchfilter.UniversalFilterCondition{
			Field: "category", Operator: "eq", Value: "hr",
		}},
	}); !errors.Is(err, errVectorUnsupported) {
		t.Fatalf("Search(FilterCondition) = %v, want errVectorUnsupported", err)
	}

	if _, err := adapter.Search(context.Background(), &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
	}); err == nil {
		t.Fatal("Search(empty vector) = nil error, want rejection")
	}
	if _, err := adapter.Search(context.Background(), nil); err == nil {
		t.Fatal("Search(nil query) = nil error, want rejection")
	}
}

func TestVectorStoreAdapter_delete_and_delete_by_filter_mapping(t *testing.T) {
	fake := &fakeVectorStore{}
	adapter := NewVectorStore(fake)
	if err := adapter.Delete(context.Background(), "doc-1"); err != nil || fake.deleteID != "doc-1" {
		t.Fatalf("Delete mapping: err=%v id=%q", err, fake.deleteID)
	}

	if err := adapter.DeleteByFilter(context.Background(),
		vectorstore.WithDeleteDocumentIDs([]string{"doc-1", "doc-2"}),
		vectorstore.WithDeleteFilter(map[string]any{"category": "hr"})); err != nil {
		t.Fatalf("DeleteByFilter = %v", err)
	}
	if len(fake.delByF.IDs) != 2 || fake.delByF.Metadata["category"] != "hr" {
		t.Fatalf("delete filter not mapped: %+v", fake.delByF)
	}

	// 租户级全量清理是 super 管理口径，不适配。
	if err := adapter.DeleteByFilter(context.Background(), vectorstore.WithDeleteAll(true)); !errors.Is(err, errVectorUnsupported) {
		t.Fatalf("DeleteByFilter(delete all) = %v, want errVectorUnsupported", err)
	}
	if err := adapter.DeleteByFilter(context.Background()); err == nil {
		t.Fatal("DeleteByFilter(no conditions) = nil error, want rejection")
	}
}

func TestVectorStoreAdapter_update_by_filter_unsupported(t *testing.T) {
	adapter := NewVectorStore(&fakeVectorStore{})
	if _, err := adapter.UpdateByFilter(context.Background(),
		vectorstore.WithUpdateByFilterDocumentIDs([]string{"doc-1"}),
		vectorstore.WithUpdateByFilterUpdates(map[string]any{"name": "x"})); !errors.Is(err, errVectorUnsupported) {
		t.Fatalf("UpdateByFilter = %v, want errVectorUnsupported", err)
	}
}

func TestVectorStoreAdapter_count_and_get_metadata_map_options(t *testing.T) {
	fake := &fakeVectorStore{count: 3, metadata: map[string]map[string]any{
		"doc-1": {"category": "hr"},
	}}
	adapter := NewVectorStore(fake)

	total, err := adapter.Count(context.Background(), vectorstore.WithCountFilter(map[string]any{"category": "hr"}))
	if err != nil || total != 3 {
		t.Fatalf("Count = (%d, %v), want (3, nil)", total, err)
	}
	if fake.countF == nil || fake.countF.Metadata["category"] != "hr" {
		t.Fatalf("count filter not mapped: %+v", fake.countF)
	}

	metadata, err := adapter.GetMetadata(context.Background(),
		vectorstore.WithGetMetadataIDs([]string{"doc-1"}),
		vectorstore.WithGetMetadataLimit(10),
		vectorstore.WithGetMetadataOffset(5))
	if err != nil {
		t.Fatalf("GetMetadata = %v", err)
	}
	if got, ok := metadata["doc-1"]; !ok || got.Metadata["category"] != "hr" {
		t.Fatalf("metadata not mapped: %+v", metadata)
	}
	if fake.metaF == nil || len(fake.metaF.IDs) != 1 || fake.metaF.Limit != 10 || fake.metaF.Offset != 5 {
		t.Fatalf("metadata filter not mapped: %+v", fake.metaF)
	}

	// 框架侧选项校验保留：limit=0 非法。
	if _, err := adapter.GetMetadata(context.Background(), vectorstore.WithGetMetadataLimit(0)); err == nil {
		t.Fatal("GetMetadata(limit=0) = nil error, want framework validation failure")
	}
}

func TestVectorStoreAdapter_unconfigured_store_rejected(t *testing.T) {
	adapter := NewVectorStore(nil)
	ctx := context.Background()
	if err := adapter.Add(ctx, &document.Document{ID: "d"}, []float64{1}); err == nil {
		t.Fatal("Add on unconfigured adapter = nil error, want rejection")
	}
	if _, _, err := adapter.Get(ctx, "d"); err == nil {
		t.Fatal("Get on unconfigured adapter = nil error, want rejection")
	}
	if err := adapter.Delete(ctx, "d"); err == nil {
		t.Fatal("Delete on unconfigured adapter = nil error, want rejection")
	}
	if _, err := adapter.Search(ctx, &vectorstore.SearchQuery{Vector: []float64{1}, SearchMode: vectorstore.SearchModeVector}); err == nil {
		t.Fatal("Search on unconfigured adapter = nil error, want rejection")
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close = %v, want nil (lifecycle owned by data pool)", err)
	}
}
