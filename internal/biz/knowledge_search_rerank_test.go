package biz

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// 检索管线重排层编排单测（mock Reranker）：重排序还原、候选 2×top_k 截断、
// 无 reranker / 未配置 / 调用失败的降级路径与同分稳定性。分数断言用精确值。

// stubReranker 是检索编排单测的重排替身：记录 query / documents / ctx 场景
// 声明，返回可编程分数或错误；ModelKey 默认回报替身模型键（与真实实现
// 「解析出端点事实后随结果报告」的口径一致）。
type stubReranker struct {
	scores   []float64
	err      error
	modelKey string

	queries   []string
	documents [][]string
	scenes    []string
}

func (r *stubReranker) Rerank(ctx context.Context, query string, documents []string) (*RerankResult, error) {
	r.queries = append(r.queries, query)
	r.documents = append(r.documents, documents)
	r.scenes = append(r.scenes, ModelCallSceneFromContext(ctx))
	if r.err != nil {
		return nil, r.err
	}
	key := r.modelKey
	if key == "" {
		key = "stub-reranker"
	}
	return &RerankResult{Scores: r.scores, ModelKey: key}, nil
}

// newSearchUsecaseWithReranker 构造装配指定重排层的检索单测用例。
func newSearchUsecaseWithReranker(t *testing.T, store VectorStore, reranker Reranker) *KnowledgeSearchUsecase {
	t.Helper()
	uc, err := NewKnowledgeSearchUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, reranker, defaultSearchLibraries(), noopSearchLogRepo{}, nil)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	uc.now = func() time.Time { return searchTestNow }
	return uc
}

func TestNewKnowledgeSearchUsecase_accepts_nil_reranker(t *testing.T) {
	uc, err := NewKnowledgeSearchUsecase(&recordingSearchStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, defaultSearchLibraries(), noopSearchLogRepo{}, nil)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase(nil reranker) = %v, want accepted", err)
	}
	if uc.reranker != nil {
		t.Fatal("reranker field set, want nil (skip the rerank layer)")
	}
}

func TestKnowledgeSearch_rerank_reorders_candidates_by_scores(t *testing.T) {
	// 融合序 [d1, d2, d3]；重排分判定 d3 > d1 > d2：最终顺序按分数还原，
	// Score 改写为 rerank 相关性分（精确值断言），场景经 ctx 声明 rerank。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{scores: []float64{0.42, 0.11, 0.97}}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits := flattenUnitHits(units)
	wantOrder := []string{"d3", "d1", "d2"}
	for i, id := range wantOrder {
		if hits[i].DocumentID != id {
			t.Fatalf("hit %d = %s, want %s (rerank order)", i, hits[i].DocumentID, id)
		}
	}
	if hits[0].Score != 0.97 || hits[1].Score != 0.42 || hits[2].Score != 0.11 {
		t.Fatalf("scores = (%f, %f, %f), want rerank scores (0.97, 0.42, 0.11)",
			hits[0].Score, hits[1].Score, hits[2].Score)
	}
	if len(reranker.queries) != 1 || reranker.queries[0] != "grape" {
		t.Fatalf("rerank query = %v, want [grape] once", reranker.queries)
	}
	if !reflect.DeepEqual(reranker.documents[0], []string{"content of d1", "content of d2", "content of d3"}) {
		t.Fatalf("rerank documents = %v, want fusion order contents", reranker.documents[0])
	}
	if reranker.scenes[0] != ModelCallScenarioRerank {
		t.Fatalf("rerank scene = %q, want %q (declared by the pipeline)", reranker.scenes[0], ModelCallScenarioRerank)
	}
}

func TestKnowledgeSearch_rerank_truncates_candidates_to_recall_window(t *testing.T) {
	// top_k=1：融合候选截到 2×top_k=2，重排只收到 2 份文档；最终取 top_k=1。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"), hitDoc("d4"),
	}}
	reranker := &stubReranker{scores: []float64{0.4, 0.9}}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", TopK: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits := flattenUnitHits(units)
	if len(reranker.documents) != 1 || len(reranker.documents[0]) != 2 {
		t.Fatalf("rerank documents = %v, want exactly 2 (top 2*top_k of fusion)", reranker.documents)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1 (final top_k)", len(hits))
	}
	if hits[0].DocumentID != "d2" || hits[0].Score != 0.9 {
		t.Fatalf("hit = (%s, %f), want (d2, 0.9)", hits[0].DocumentID, hits[0].Score)
	}
}

func TestKnowledgeSearch_rerank_single_candidate_skips_layer(t *testing.T) {
	// 融合候选只有 1 条：重排层不触发（调用次数 0），返回恰好 1 条命中，
	// Score 保持 RRF 融合分（重排是候选间相对质量的增强面，单候选无可比）。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	reranker := &stubReranker{scores: []float64{0.99}}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits := flattenUnitHits(units)
	if len(reranker.queries) != 0 {
		t.Fatalf("rerank calls = %d, want 0 (single candidate skips the layer)", len(reranker.queries))
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if hits[0].DocumentID != "d1" || hits[0].Score != 1/(k+1) {
		t.Fatalf("hit = (%s, %f), want (d1, RRF fusion score)", hits[0].DocumentID, hits[0].Score)
	}
}

func TestKnowledgeSearch_rerank_ties_keep_fusion_order(t *testing.T) {
	// 同分保持融合序（稳定排序）：分数全部并列时顺序不变，Score 仍改写。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{scores: []float64{0.5, 0.5, 0.5}}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits := flattenUnitHits(units)
	wantOrder := []string{"d1", "d2", "d3"}
	for i, id := range wantOrder {
		if hits[i].DocumentID != id {
			t.Fatalf("hit %d = %s, want %s (ties keep fusion order)", i, hits[i].DocumentID, id)
		}
		if hits[i].Score != 0.5 {
			t.Fatalf("hit %d score = %f, want 0.5", i, hits[i].Score)
		}
	}
}

func TestKnowledgeSearch_without_reranker_keeps_fusion_order(t *testing.T) {
	// 未装配重排层：行为与既有口径一致——融合序、RRF 分、最终 top_k 截断。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", TopK: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits := flattenUnitHits(units)
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (final top_k)", len(hits))
	}
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if hits[0].DocumentID != "d1" || hits[1].DocumentID != "d2" {
		t.Fatalf("order = (%s, %s), want fusion order (d1, d2)", hits[0].DocumentID, hits[1].DocumentID)
	}
	if hits[0].Score != 1/(k+1) || hits[1].Score != 1/(k+2) {
		t.Fatalf("scores = (%f, %f), want RRF fusion scores", hits[0].Score, hits[1].Score)
	}
}

func TestKnowledgeSearch_rerank_unavailable_skips_layer_silently(t *testing.T) {
	// 未配置重排模型（ErrRerankUnavailable）：跳过该层，行为与未装配一致
	// ——不失败、保持融合序与 RRF 分。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{err: ErrRerankUnavailable}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search with ErrRerankUnavailable = %v, want degraded success", err)
	}
	hits := flattenUnitHits(units)
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	wantOrder := []string{"d1", "d2", "d3"}
	for i, id := range wantOrder {
		if hits[i].DocumentID != id {
			t.Fatalf("hit %d = %s, want fusion order %s", i, hits[i].DocumentID, id)
		}
		if hits[i].Score != 1/(k+float64(i+1)) {
			t.Fatalf("hit %d score = %f, want RRF fusion score", i, hits[i].Score)
		}
	}
}

func TestKnowledgeSearch_rerank_failure_degrades_to_fusion_order(t *testing.T) {
	// 重排调用失败（非未配置）：降级为融合序继续检索，不失败，分数保持
	// RRF 融合分。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{err: errors.New("rerank endpoint unavailable")}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search with rerank failure = %v, want degraded success", err)
	}
	hits := flattenUnitHits(units)
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	wantOrder := []string{"d1", "d2", "d3"}
	for i, id := range wantOrder {
		if hits[i].DocumentID != id {
			t.Fatalf("hit %d = %s, want fusion order %s", i, hits[i].DocumentID, id)
		}
		if hits[i].Score != 1/(k+float64(i+1)) {
			t.Fatalf("hit %d score = %f, want RRF fusion score", i, hits[i].Score)
		}
	}
}

func TestKnowledgeSearch_rerank_mismatched_scores_degrade_to_fusion_order(t *testing.T) {
	// 契约要求等长返回：长度不符按层失败降级（不错序、不失败）。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"),
	}}
	reranker := &stubReranker{scores: []float64{0.9}}
	uc := newSearchUsecaseWithReranker(t, store, reranker)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search with mismatched scores = %v, want degraded success", err)
	}
	hits := flattenUnitHits(units)
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if len(hits) != 2 || hits[0].Score != 1/(k+1) || hits[1].Score != 1/(k+2) {
		t.Fatalf("hits = %+v, want fusion order with RRF scores", hits)
	}
}
