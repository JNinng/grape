package biz

import (
	"reflect"
	"testing"
)

// RRF 融合纯函数单测：并列名次、路数缺失（文档只出现在一路）、多路累加、
// 排序稳定性与 top_k 截断。输入各路是已按名次排好的命中序列（与存储层
// 排序契约一致）；融合分断言用精确公式值，不比较原始向量分。

// hitDoc 是构造命中的简写。
func hitDoc(id string) *ScoredVectorDocument {
	return &ScoredVectorDocument{Document: &VectorDocument{ID: id, Content: "content of " + id}}
}

// rrfScore 按公式计算理论分：文档在名次 ranks（1 起）的每一路各累加
// 1/(k+rank)。
func rrfScore(k float64, ranks ...int) float64 {
	total := 0.0
	for _, rank := range ranks {
		total += 1 / (k + float64(rank))
	}
	return total
}

func TestReciprocalRankFusion_accumulates_scores_across_routes(t *testing.T) {
	// d2 同时出现在两路（向量路 rank 1、词法路 rank 2）：分数两路累加，
	// 文档记录取首次出现者；单路文档只累加自己那一路。
	routes := [][]*ScoredVectorDocument{
		{hitDoc("d1"), hitDoc("d2")},
		{hitDoc("d2"), hitDoc("d3")},
	}
	got := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 5)

	if len(got) != 3 {
		t.Fatalf("fused hits = %d, want 3", len(got))
	}
	// 排序：d2（0.0326）> d1（0.0164）> d3（0.0161）。
	wantOrder := []string{"d2", "d1", "d3"}
	for i, id := range wantOrder {
		if got[i].Document.ID != id {
			t.Fatalf("hit %d = %s, want %s", i, got[i].Document.ID, id)
		}
	}
	want := rrfScore(DefaultKnowledgeSearchFusionRRFK, 1, 2)
	if got[0].Score != want {
		t.Fatalf("d2 score = %f, want %f (two-route accumulation)", got[0].Score, want)
	}
	if got[1].Score != rrfScore(DefaultKnowledgeSearchFusionRRFK, 1) {
		t.Fatalf("d1 score = %f, want single-route rank-1", got[1].Score)
	}
	if got[2].Score != rrfScore(DefaultKnowledgeSearchFusionRRFK, 2) {
		t.Fatalf("d3 score = %f, want single-route rank-2", got[2].Score)
	}
	// 文档记录取首次出现（向量路）的指针：metadata/content 不被后路覆盖。
	if got[0].Document.Content != "content of d2" {
		t.Fatalf("d2 document = %+v, want first occurrence", got[0].Document)
	}
}

func TestReciprocalRankFusion_tied_scores_break_by_first_occurrence(t *testing.T) {
	// d1/d2 各自只在一路 rank 1：分数并列，按首次出现序稳定排序。
	routes := [][]*ScoredVectorDocument{
		{hitDoc("d1")},
		{hitDoc("d2")},
	}
	got := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 5)
	if len(got) != 2 {
		t.Fatalf("fused hits = %d, want 2", len(got))
	}
	if got[0].Document.ID != "d1" || got[1].Document.ID != "d2" {
		t.Fatalf("order = (%s, %s), want first-occurrence tie-break (d1, d2)",
			got[0].Document.ID, got[1].Document.ID)
	}
	want := rrfScore(DefaultKnowledgeSearchFusionRRFK, 1)
	if got[0].Score != want || got[1].Score != want {
		t.Fatalf("scores = (%f, %f), want tied %f", got[0].Score, got[1].Score, want)
	}
	// 重复调用产出同一序列（确定性）。
	again := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 5)
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("repeated fusion differs: %+v vs %+v", got, again)
	}
}

func TestReciprocalRankFusion_missing_route_counts_once(t *testing.T) {
	// 路数缺失：d2 只出现在第二路（rank 1），不得按全路数摊薄或惩罚，
	// 分数恰为该路的 1/(k+1)。
	routes := [][]*ScoredVectorDocument{
		{hitDoc("d1")},
		{hitDoc("d2")},
	}
	got := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 5)
	want := rrfScore(DefaultKnowledgeSearchFusionRRFK, 1)
	if got[1].Score != want {
		t.Fatalf("single-route document score = %f, want %f", got[1].Score, want)
	}
}

func TestReciprocalRankFusion_truncates_to_top_k(t *testing.T) {
	routes := [][]*ScoredVectorDocument{
		{hitDoc("d1"), hitDoc("d2"), hitDoc("d3")},
	}
	got := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 2)
	if len(got) != 2 {
		t.Fatalf("fused hits = %d, want truncated to 2", len(got))
	}
	if got[0].Document.ID != "d1" || got[1].Document.ID != "d2" {
		t.Fatalf("truncated order = (%s, %s), want rank order (d1, d2)",
			got[0].Document.ID, got[1].Document.ID)
	}
}

func TestReciprocalRankFusion_skips_nil_hits_without_consuming_rank(t *testing.T) {
	// nil 命中不占名次：d3 在 nil 之后仍按 rank 2 计分。
	routes := [][]*ScoredVectorDocument{
		{hitDoc("d1"), nil, hitDoc("d3")},
	}
	got := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, 5)
	if len(got) != 2 {
		t.Fatalf("fused hits = %d, want 2 (nil skipped)", len(got))
	}
	want := rrfScore(DefaultKnowledgeSearchFusionRRFK, 2)
	if got[1].Score != want {
		t.Fatalf("post-nil score = %f, want rank-2 %f", got[1].Score, want)
	}
}

func TestReciprocalRankFusion_empty_routes_return_empty_slice(t *testing.T) {
	got := reciprocalRankFusion(nil, DefaultKnowledgeSearchFusionRRFK, 5)
	if got == nil || len(got) != 0 {
		t.Fatalf("fusion(nil routes) = %#v, want empty non-nil slice", got)
	}
	got = reciprocalRankFusion([][]*ScoredVectorDocument{{}, {}}, DefaultKnowledgeSearchFusionRRFK, 5)
	if got == nil || len(got) != 0 {
		t.Fatalf("fusion(empty routes) = %#v, want empty non-nil slice", got)
	}
}
