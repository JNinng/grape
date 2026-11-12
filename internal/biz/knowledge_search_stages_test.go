package biz

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"grape/pkg/redact"
)

// 检索分阶段留痕（stages）编排单测（mock 仓储）：阶段序与执行序、变体
// query 脱敏、阶段失败行、候选截断、rerank 前后序还原、编排路径不产生
// 逐路留痕行（装饰器由 ctx 抑制标记跳过）与留痕写入失败不反噬检索。
// 落库形态（jsonb 列、RLS）在 data 层集成测试验证。

// noopSearchLogRepo 是通用检索单测的空转留痕仓储：行内容不被断言。
type noopSearchLogRepo struct{}

func (noopSearchLogRepo) Append(context.Context, *RetrievalLogRecord) error { return nil }

func (noopSearchLogRepo) List(context.Context, *RetrievalLogFilter) (*RetrievalLogList, error) {
	return &RetrievalLogList{}, nil
}

// stageRecordingLogRepo 记录编排层写入的留痕行，可注入写入失败。
type stageRecordingLogRepo struct {
	rows      []*RetrievalLogRecord
	appendErr error
}

func (r *stageRecordingLogRepo) Append(_ context.Context, rec *RetrievalLogRecord) error {
	if r.appendErr != nil {
		return r.appendErr
	}
	r.rows = append(r.rows, rec)
	return nil
}

func (r *stageRecordingLogRepo) List(context.Context, *RetrievalLogFilter) (*RetrievalLogList, error) {
	return &RetrievalLogList{}, nil
}

// newStageSearchUsecase 构造带留痕记录仓储的检索单测用例。
func newStageSearchUsecase(t *testing.T, store VectorStore, reranker Reranker, logs RetrievalLogRepo, registry *redact.Registry) (*KnowledgeSearchUsecase, *stageRecordingLogRepo) {
	t.Helper()
	rec, ok := logs.(*stageRecordingLogRepo)
	if !ok {
		t.Fatalf("logs must be *stageRecordingLogRepo for row assertions")
	}
	uc, err := NewKnowledgeSearchUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, reranker, defaultSearchLibraries(), logs, registry)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	uc.now = func() time.Time { return searchTestNow }
	return uc, rec
}

// stageNames 取留痕行的阶段取值序列（执行序）。
func stageNames(rec *RetrievalLogRecord) []string {
	var stages []retrievalStage
	if err := json.Unmarshal(rec.Stages, &stages); err != nil {
		return nil
	}
	names := make([]string, 0, len(stages))
	for _, s := range stages {
		names = append(names, s.Stage)
	}
	return names
}

// stageOf 取留痕行内第一个指定类型的阶段元素。
func stageOf(t *testing.T, rec *RetrievalLogRecord, name string) retrievalStage {
	t.Helper()
	var stages []retrievalStage
	if err := json.Unmarshal(rec.Stages, &stages); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	for _, s := range stages {
		if s.Stage == name {
			return s
		}
	}
	t.Fatalf("stage %q not found in %+v", name, stages)
	return retrievalStage{}
}

func TestKnowledgeSearchStages_records_stages_in_execution_order(t *testing.T) {
	// 单查询无重排：阶段序 = vector → lexical → fusion → pack；召回阶段
	// 记变体文本与 limit，融合/回包阶段不伪造查询文本。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("recorded rows = %d, want 1 (one operation, one row)", len(repo.rows))
	}
	rec := repo.rows[0]
	names := stageNames(rec)
	want := []string{RetrievalStageVector, RetrievalStageLexical, RetrievalStageFusion, RetrievalStagePack}
	if len(names) != len(want) {
		t.Fatalf("stages = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("stage %d = %s, want %s (execution order)", i, names[i], n)
		}
	}
	vectorStage := stageOf(t, rec, RetrievalStageVector)
	if vectorStage.QueryText != "grape" {
		t.Fatalf("vector stage query = %q, want the variant text", vectorStage.QueryText)
	}
	if vectorStage.Limit != DefaultKnowledgeSearchTopK*KnowledgeSearchRecallFactor {
		t.Fatalf("vector stage limit = %d, want 2*top_k recall width", vectorStage.Limit)
	}
	fusionStage := stageOf(t, rec, RetrievalStageFusion)
	if fusionStage.QueryText != "" || fusionStage.Model != "" {
		t.Fatalf("fusion stage = (%q, %q), want no query text and no model", fusionStage.QueryText, fusionStage.Model)
	}
	packStage := stageOf(t, rec, RetrievalStagePack)
	if packStage.Limit != KnowledgeSearchPackBudgetTokens {
		t.Fatalf("pack stage limit = %d, want the token budget", packStage.Limit)
	}
}

func TestKnowledgeSearchStages_variants_add_recall_pairs_in_order(t *testing.T) {
	// 主查询 + 1 变体：每条文本一对召回阶段，数组顺序即执行序；各召回
	// 阶段记自己的变体文本，不重复记录。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", Variants: []string{"grape rewrite"}}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	names := stageNames(repo.rows[0])
	want := []string{
		RetrievalStageVector, RetrievalStageLexical,
		RetrievalStageVector, RetrievalStageLexical,
		RetrievalStageFusion, RetrievalStagePack,
	}
	if len(names) != len(want) {
		t.Fatalf("stages = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("stage %d = %s, want %s", i, names[i], n)
		}
	}
	var stages []retrievalStage
	if err := json.Unmarshal(repo.rows[0].Stages, &stages); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	if stages[0].QueryText != "grape" || stages[2].QueryText != "grape rewrite" {
		t.Fatalf("recall stage queries = (%q, %q), want (main, variant) in execution order",
			stages[0].QueryText, stages[2].QueryText)
	}
}

func TestKnowledgeSearchStages_redacts_query_texts_before_persisting(t *testing.T) {
	// 脱敏义务随写入方迁移：主查询与变体查询文本（行级与阶段级）都按
	// retrieval 类别脱敏后落库。
	registry := redact.NewRegistry()
	if err := registry.Register(redact.CategoryRetrieval, redact.NewMask("[masked]")); err != nil {
		t.Fatalf("register: %v", err)
	}
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, registry)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", Variants: []string{"variant"}}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	rec := repo.rows[0]
	if rec.QueryText != "[masked]" {
		t.Fatalf("row query text = %q, want masked by the retrieval redactor", rec.QueryText)
	}
	var stages []retrievalStage
	if err := json.Unmarshal(rec.Stages, &stages); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	for i, s := range stages {
		if s.Stage == RetrievalStageVector || s.Stage == RetrievalStageLexical {
			if s.QueryText != "[masked]" {
				t.Fatalf("stage %d (%s) query = %q, want masked variant text", i, s.Stage, s.QueryText)
			}
			continue
		}
		if s.QueryText != "" {
			t.Fatalf("stage %d (%s) query = %q, want empty (non-recall stages carry no query text)", i, s.Stage, s.QueryText)
		}
	}
}

func TestKnowledgeSearchStages_lexical_failure_records_stage_and_keeps_success(t *testing.T) {
	// 词法路失败按质量增强降级：阶段记错误与空候选，整行 status 仍记
	// 操作终态（成功），失败行不反噬主流程。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}, lexicalErr: errors.New("lexical storage down")}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil || len(units) == 0 {
		t.Fatalf("Search = (%d units, %v), want degraded success", len(units), err)
	}
	rec := repo.rows[0]
	if rec.Status != RetrievalStatusSuccess {
		t.Fatalf("row status = %q, want success (degraded stage does not flip the operation)", rec.Status)
	}
	stage := stageOf(t, rec, RetrievalStageLexical)
	if stage.Error == "" || !strings.Contains(stage.Error, "lexical storage down") {
		t.Fatalf("lexical stage error = %q, want the failure summary", stage.Error)
	}
	if stage.Candidates != nil {
		t.Fatalf("lexical stage candidates = %+v, want empty on failure", stage.Candidates)
	}
}

func TestKnowledgeSearchStages_vector_failure_records_error_row(t *testing.T) {
	// 向量路失败让整次检索失败（正确性依赖）：行 status 记 error，向量
	// 阶段记错误摘要与空候选，失败行照常留痕。
	store := &recordingSearchStore{err: errors.New("vector storage down")}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err == nil {
		t.Fatal("Search with vector failure = nil error, want propagated")
	}
	rec := repo.rows[0]
	if rec.Status != RetrievalStatusError {
		t.Fatalf("row status = %q, want error", rec.Status)
	}
	stage := stageOf(t, rec, RetrievalStageVector)
	if stage.Error == "" || stage.Candidates != nil {
		t.Fatalf("vector stage = (error %q, candidates %+v), want error summary with empty candidates", stage.Error, stage.Candidates)
	}
	if stage.QueryText != "grape" {
		t.Fatalf("vector stage query = %q, want the failing variant text", stage.QueryText)
	}
}

func TestKnowledgeSearchStages_embed_failure_records_error_row(t *testing.T) {
	// 查询向量化失败让整次检索失败（正确性依赖）：该召回路的 vector 阶段
	// 记错误摘要与空候选（未触达向量存储），行 status 记 error 并留痕。
	store := &recordingSearchStore{}
	logs := &stageRecordingLogRepo{}
	uc, err := NewKnowledgeSearchUsecase(store, &stubEmbedder{dims: 4, stubErr: errors.New("embedding endpoint down")},
		&stubResolver{space: testEmbedderSpace, dim: 4}, nil, defaultSearchLibraries(), logs, nil)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	uc.now = func() time.Time { return searchTestNow }

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err == nil {
		t.Fatal("Search with embed failure = nil error, want propagated")
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0 (embed failed before recall)", store.calls)
	}
	if len(logs.rows) != 1 {
		t.Fatalf("recorded rows = %d, want 1 (failed operations are evidence too)", len(logs.rows))
	}
	rec := logs.rows[0]
	if rec.Status != RetrievalStatusError {
		t.Fatalf("row status = %q, want error", rec.Status)
	}
	if len(rec.Hits) != 0 {
		t.Fatalf("row hits = %+v, want empty on embed failure", rec.Hits)
	}
	names := stageNames(rec)
	if len(names) != 1 || names[0] != RetrievalStageVector {
		t.Fatalf("stages = %v, want exactly the failing vector stage", names)
	}
	stage := stageOf(t, rec, RetrievalStageVector)
	if stage.Error == "" || !strings.Contains(stage.Error, "embedding endpoint down") {
		t.Fatalf("vector stage error = %q, want the embed failure summary", stage.Error)
	}
	if stage.Candidates != nil {
		t.Fatalf("vector stage candidates = %+v, want empty on embed failure", stage.Candidates)
	}
	if stage.QueryText != "grape" {
		t.Fatalf("vector stage query = %q, want the failing query text", stage.QueryText)
	}
}

func TestKnowledgeSearchStages_rerank_mismatched_scores_records_error_stage(t *testing.T) {
	// 重排分数长度不符（契约破坏）降级为融合序：rerank 阶段记错误摘要、
	// 候选保持融合序且带模型键，行 status 仍为成功（质量增强层不反噬）。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"),
	}}
	reranker := &stubReranker{scores: []float64{0.9}}
	uc, repo := newStageSearchUsecase(t, store, reranker, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search with mismatched rerank scores = %v, want degraded success", err)
	}
	rec := repo.rows[0]
	if rec.Status != RetrievalStatusSuccess {
		t.Fatalf("row status = %q, want success (degraded layer)", rec.Status)
	}
	stage := stageOf(t, rec, RetrievalStageRerank)
	if !strings.Contains(stage.Error, "mismatched scores") {
		t.Fatalf("rerank stage error = %q, want the mismatch summary", stage.Error)
	}
	if stage.Model != "stub-reranker" {
		t.Fatalf("rerank stage model = %q, want the reported model key", stage.Model)
	}
	fusionCandidates := stageOf(t, rec, RetrievalStageFusion).Candidates
	if len(stage.Candidates) != len(fusionCandidates) {
		t.Fatalf("rerank stage candidates = %d, want %d (degraded to fusion order)", len(stage.Candidates), len(fusionCandidates))
	}
	for i := range fusionCandidates {
		if stage.Candidates[i].DocumentID != fusionCandidates[i].DocumentID {
			t.Fatalf("rerank candidate %d = %s, want fusion order %s", i, stage.Candidates[i].DocumentID, fusionCandidates[i].DocumentID)
		}
	}
}

func TestKnowledgeSearchStages_stage_candidates_capped_at_limit(t *testing.T) {
	// 每阶段候选上限 50（沿用 hits 截断口径）：召回命中 55 条时阶段候选
	// 截到 50。
	hits := make([]*ScoredVectorDocument, 0, RetrievalStageMaxCandidates+5)
	for i := 0; i < RetrievalStageMaxCandidates+5; i++ {
		hits = append(hits, hitDoc("d"+string(rune('a'+i))))
	}
	store := &recordingSearchStore{results: hits}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	stage := stageOf(t, repo.rows[0], RetrievalStageVector)
	if len(stage.Candidates) != RetrievalStageMaxCandidates {
		t.Fatalf("vector stage candidates = %d, want capped at %d", len(stage.Candidates), RetrievalStageMaxCandidates)
	}
}

func TestKnowledgeSearchStages_rerank_before_and_after_order_restorable(t *testing.T) {
	// 重排前后序还原：fusion 阶段候选 = 重排前序（RRF 分），rerank 阶段
	// 候选 = 重排后序（重排分）；行 hits 为最终命中（重排后取 top_k）。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{scores: []float64{0.42, 0.11, 0.97}}
	uc, repo := newStageSearchUsecase(t, store, reranker, &stageRecordingLogRepo{}, nil)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	rec := repo.rows[0]
	fusionCandidates := stageOf(t, rec, RetrievalStageFusion).Candidates
	wantBefore := []string{"d1", "d2", "d3"}
	for i, id := range wantBefore {
		if fusionCandidates[i].DocumentID != id {
			t.Fatalf("fusion candidate %d = %s, want %s (pre-rerank order)", i, fusionCandidates[i].DocumentID, id)
		}
	}
	rerankStage := stageOf(t, rec, RetrievalStageRerank)
	wantAfter := []string{"d3", "d1", "d2"}
	for i, id := range wantAfter {
		if rerankStage.Candidates[i].DocumentID != id {
			t.Fatalf("rerank candidate %d = %s, want %s (post-rerank order)", i, rerankStage.Candidates[i].DocumentID, id)
		}
	}
	if rerankStage.Candidates[0].Score != 0.97 {
		t.Fatalf("rerank candidate score = %f, want the rerank model score", rerankStage.Candidates[0].Score)
	}
	if rerankStage.Model != "stub-reranker" || rerankStage.Error != "" {
		t.Fatalf("rerank stage = (model %q, error %q), want model key without error", rerankStage.Model, rerankStage.Error)
	}
	if len(rec.Hits) != len(wantAfter) || rec.Hits[0].DocumentID != "d3" {
		t.Fatalf("row hits = %+v, want the final top_k hits in rerank order", rec.Hits)
	}
	if len(units) == 0 {
		t.Fatal("units = 0, want the packed reply")
	}
}

func TestKnowledgeSearchStages_rerank_failure_records_stage_with_fusion_order(t *testing.T) {
	// 重排调用失败降级为融合序：rerank 阶段记错误摘要、候选保持融合序
	//（与 fusion 阶段同序），行 status 仍为成功。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{err: errors.New("rerank endpoint unavailable")}
	uc, repo := newStageSearchUsecase(t, store, reranker, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	rec := repo.rows[0]
	stage := stageOf(t, rec, RetrievalStageRerank)
	if stage.Error == "" {
		t.Fatal("rerank stage error empty, want the failure summary")
	}
	fusionCandidates := stageOf(t, rec, RetrievalStageFusion).Candidates
	if len(stage.Candidates) != len(fusionCandidates) {
		t.Fatalf("rerank stage candidates = %d, want %d (degraded to fusion order)", len(stage.Candidates), len(fusionCandidates))
	}
	for i := range fusionCandidates {
		if stage.Candidates[i].DocumentID != fusionCandidates[i].DocumentID {
			t.Fatalf("rerank candidate %d = %s, want fusion order %s", i, stage.Candidates[i].DocumentID, fusionCandidates[i].DocumentID)
		}
	}
	if rec.Status != RetrievalStatusSuccess {
		t.Fatalf("row status = %q, want success (degraded layer)", rec.Status)
	}
}

func TestKnowledgeSearchStages_rerank_unavailable_records_no_stage(t *testing.T) {
	// 未配置重排模型按跳过该层处理：层未运行，不记 rerank 阶段（不伪造
	// 空阶段），阶段序直接 fusion → pack。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{
		hitDoc("d1"), hitDoc("d2"), hitDoc("d3"),
	}}
	reranker := &stubReranker{err: ErrRerankUnavailable}
	uc, repo := newStageSearchUsecase(t, store, reranker, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	names := stageNames(repo.rows[0])
	for _, n := range names {
		if n == RetrievalStageRerank {
			t.Fatalf("stages = %v, want no rerank stage (unavailable skips the layer)", names)
		}
	}
}

func TestKnowledgeSearchStages_pack_stage_units_cover_assembled_candidates(t *testing.T) {
	// pack 阶段 units 覆盖全部装配单元，admitted 与回包结果一一对应（容量
	// 充足场景：无截断）。真超预算的截断断言在下一个用例。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S1", 0, "", "chunk zero"), packHit("S1", 1, "", "chunk one")},
		lists: map[string][]*VectorDocument{
			"S1": {packDoc("S1", 0, "", "chunk zero"), packDoc("S1", 1, "", "chunk one")},
		},
	}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	stage := stageOf(t, repo.rows[0], RetrievalStagePack)
	if len(stage.Units) == 0 {
		t.Fatal("pack stage units empty, want the assembled units")
	}
	admittedCount := 0
	for _, u := range stage.Units {
		if u.Admitted {
			admittedCount++
		}
		if len(u.DocumentIDs) == 0 {
			t.Fatalf("pack unit %+v carries no member ids", u)
		}
		if u.Tokens <= 0 {
			t.Fatalf("pack unit %+v carries no token estimate", u)
		}
	}
	if admittedCount != len(units) {
		t.Fatalf("admitted units = %d, want %d (the returned reply)", admittedCount, len(units))
	}
}

func TestKnowledgeSearchStages_pack_stage_records_budget_truncated_unit(t *testing.T) {
	// 真超预算：来源整文档（两节拼接）放不进预算，广度段收下更小的整节，
	// 另一节被预算截断——pack 阶段留下 admitted=false 的单元，其 mode /
	// heading_path / anchor / 成员 ID 如实记录，且成员 ID 对得上 fusion 序
	// 中该节最高分命中，据此可看出预算截断掉了谁。
	bigA := strings.Repeat("a", 6000) // 1500 tokens
	bigB := strings.Repeat("b", 4000) // 1000 tokens
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S1", 0, "A", bigA),
			packHit("S1", 1, "A", bigA),
			packHit("S1", 2, "B", bigB),
			packHit("S1", 3, "B", bigB),
		},
		lists: map[string][]*VectorDocument{
			"S1": {
				packDoc("S1", 0, "A", bigA),
				packDoc("S1", 1, "A", bigA),
				packDoc("S1", 2, "B", bigB),
				packDoc("S1", 3, "B", bigB),
			},
		},
	}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	rec := repo.rows[0]
	packStage := stageOf(t, rec, RetrievalStagePack)

	var sectionA, sectionB *retrievalPackUnit
	for i := range packStage.Units {
		u := &packStage.Units[i]
		switch {
		case u.Mode == KnowledgeSearchUnitModeSection && u.HeadingPath == "A":
			sectionA = u
		case u.Mode == KnowledgeSearchUnitModeSection && u.HeadingPath == "B":
			sectionB = u
		}
	}
	if sectionA == nil || sectionB == nil {
		t.Fatalf("pack units = %+v, want both section entries recorded", packStage.Units)
	}
	if !sectionA.Admitted || sectionB.Admitted {
		t.Fatalf("admission = (A %v, B %v), want A admitted and B truncated by budget", sectionA.Admitted, sectionB.Admitted)
	}
	if sectionB.Anchor != "runes 200-299" {
		t.Fatalf("truncated unit anchor = %q, want the section head anchor", sectionB.Anchor)
	}
	wantMembers := []string{"S1#2", "S1#3"}
	if len(sectionB.DocumentIDs) != len(wantMembers) || sectionB.DocumentIDs[0] != wantMembers[0] || sectionB.DocumentIDs[1] != wantMembers[1] {
		t.Fatalf("truncated unit members = %v, want %v", sectionB.DocumentIDs, wantMembers)
	}
	// 对照 fusion 序：被截断节的成员首块即 fusion 序里该节最高分命中。
	fusion := stageOf(t, rec, RetrievalStageFusion)
	if len(fusion.Candidates) < 3 || fusion.Candidates[2].DocumentID != sectionB.DocumentIDs[0] {
		t.Fatalf("fusion candidates = %+v, want fusion order to locate the truncated section head", fusion.Candidates)
	}
	if len(units) == 0 {
		t.Fatal("units = 0, want the admitted section in the reply")
	}
}

func TestKnowledgeSearchStages_orchestration_suppresses_per_route_rows(t *testing.T) {
	// 编排路径不产生逐路留痕行：store 收到的 ctx 带抑制标记（装饰器据此
	// 跳过），多路调用后编排层恰好写一行。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	if _, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !store.suppressed {
		t.Fatal("store ctx carried no suppression flag, want WithRetrievalLogSuppressed on the orchestration path")
	}
	if len(repo.rows) != 1 {
		t.Fatalf("recorded rows = %d, want exactly one orchestration row", len(repo.rows))
	}
}

func TestKnowledgeSearchStages_row_carries_operation_facts(t *testing.T) {
	// 整行事实：来源与身份载体、嵌入空间、top_k 两值、过滤摘要（kb_ids）、
	// 最终命中与总耗时。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{}, nil)

	ctx := WithRetrievalSource(searchCtx(), RetrievalSourceTool)
	ctx = WithSessionID(ctx, "sess-stage")
	if _, err := uc.Search(ctx, &KnowledgeSearchQuery{Query: "grape", TopK: 7}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	rec := repo.rows[0]
	switch {
	case rec.Source != RetrievalSourceTool:
		t.Fatalf("source = %q, want the declared tool source", rec.Source)
	case rec.SessionID != "sess-stage":
		t.Fatalf("session id = %q, want the ctx fact", rec.SessionID)
	case rec.Embedder != testEmbedderSpace:
		t.Fatalf("embedder = %q, want the resolved space", rec.Embedder)
	case rec.RequestedTopK != 7 || rec.EffectiveTopK != 7:
		t.Fatalf("top_k = (%d, %d), want the normalized top_k on both", rec.RequestedTopK, rec.EffectiveTopK)
	case rec.DurationMS < 0:
		t.Fatalf("duration = %d, want non-negative", rec.DurationMS)
	}
	ids, ok := rec.FilterSummary["kb_ids"].([]int64)
	if !ok || len(ids) != 2 {
		t.Fatalf("filter summary = %+v, want the visible library id list", rec.FilterSummary)
	}
}

func TestKnowledgeSearchStages_no_row_before_recall_pipeline(t *testing.T) {
	// 不进入召回管线的路径不产生留痕行（与装饰器只在检索方法被调用时记
	// 行的口径一致）：无可见库提前返回零行。
	store := &recordingSearchStore{}
	logs := &stageRecordingLogRepo{}
	uc, err := NewKnowledgeSearchUsecase(store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, &stubLibraryDirectory{}, logs, nil)
	if err != nil {
		t.Fatalf("NewKnowledgeSearchUsecase: %v", err)
	}
	uc.now = func() time.Time { return searchTestNow }

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil || len(units) != 0 {
		t.Fatalf("Search with no visible libraries = (%d units, %v), want empty success", len(units), err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
	if len(logs.rows) != 0 {
		t.Fatalf("recorded rows = %d, want 0 (no recall pipeline, no row)", len(logs.rows))
	}
}

func TestKnowledgeSearchStages_log_write_failure_does_not_break_search(t *testing.T) {
	// 留痕写入失败只记日志、不反噬检索（旁路观察面纪律）。
	store := &recordingSearchStore{results: []*ScoredVectorDocument{hitDoc("d1")}}
	uc, repo := newStageSearchUsecase(t, store, nil, &stageRecordingLogRepo{appendErr: errors.New("log storage down")}, nil)

	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape"})
	if err != nil || len(units) == 0 {
		t.Fatalf("Search with failing log repo = (%d units, %v), must succeed", len(units), err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("recorded rows = %d, want 0", len(repo.rows))
	}
}

func TestNewKnowledgeSearchUsecase_requires_retrieval_log_repo(t *testing.T) {
	if _, err := NewKnowledgeSearchUsecase(&recordingSearchStore{}, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, nil, defaultSearchLibraries(), nil, nil); err == nil {
		t.Fatal("NewKnowledgeSearchUsecase(nil logs) = nil error, want rejection")
	}
}
