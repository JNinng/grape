package biz

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// 检索回包聚合（pack）单测：分档决策矩阵（整节 / 相邻窗口 / 整文档 / 直回
// 与多来源混合、来源数截断）、预算封顶的两段装配（广度降级、深度跳过、
// 整文档降级与同源收编）、命中块到回包单元的映射（Content 连接 / Anchor /
// Score / Hits 与 DocumentIDs 差集）与 token 估算精确值。全部走内存替身
//（recordingSearchStore 的 lists 编程来源全量块），断言具体值。

// flattenUnitHits 把回包单元展平回命中切片序列（console 消费口径）：按单
// 元序拼接各单元的 Hits。
func flattenUnitHits(units []*KnowledgeSearchUnit) []*KnowledgeSearchHit {
	hits := make([]*KnowledgeSearchHit, 0)
	for _, u := range units {
		if u == nil {
			continue
		}
		hits = append(hits, u.Hits...)
	}
	return hits
}

// packDoc 构造来源全量块的替身文档：metadata 口径与摄取侧 chunkMetadata
// 一致（chunk_index 写入形态 int、anchor rune 区间、heading_path 标题链）。
func packDoc(sourceID string, index int, heading, content string) *VectorDocument {
	return &VectorDocument{
		ID:      fmt.Sprintf("%s#%d", sourceID, index),
		Content: content,
		Metadata: map[string]any{
			"source_id":    sourceID,
			"source_name":  "name-" + sourceID,
			"chunk_index":  index,
			"anchor":       fmt.Sprintf("runes %d-%d", index*100, index*100+99),
			"heading_path": heading,
		},
	}
}

// packHit 是命中替身：与 packDoc 同 ID 同 metadata（命中分由融合按名次
// 重算，替身 Score 不参与）。
func packHit(sourceID string, index int, heading, content string) *ScoredVectorDocument {
	return &ScoredVectorDocument{Document: packDoc(sourceID, index, heading, content)}
}

// packSearch 是 pack 编排单测的检索入口：单一向量路（词法路恒空），命中
// 分按 results 次序为 1/(k+rank)。
func packSearch(t *testing.T, store *recordingSearchStore, topK int) []*KnowledgeSearchUnit {
	t.Helper()
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())
	units, err := uc.Search(searchCtx(), &KnowledgeSearchQuery{Query: "grape", TopK: topK})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return units
}

// packUnitIDs 是单元成员切片 ID 简写（DocumentIDs，含兄弟块）。
func packUnitIDs(u *KnowledgeSearchUnit) []string { return u.DocumentIDs }

// packHitIDs 是单元命中成员切片 ID 简写（Hits 的 DocumentID）。
func packHitIDs(u *KnowledgeSearchUnit) []string {
	ids := make([]string, 0, len(u.Hits))
	for _, h := range u.Hits {
		ids = append(ids, h.DocumentID)
	}
	return ids
}

func TestKnowledgeSearchPack_same_section_multi_hit_aggregates_full_section_with_siblings(t *testing.T) {
	// 双来源命中使整文档档不适用（独占判定要求来源唯一），S1 落节聚合档。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S1", 2, "部署 > 数据库", "section chunk two"),
			packHit("S1", 3, "部署 > 数据库", "section chunk three"),
			packHit("S1b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S1": {
				packDoc("S1", 1, "部署 > 数据库", "section chunk one"),
				packDoc("S1", 2, "部署 > 数据库", "section chunk two"),
				packDoc("S1", 3, "部署 > 数据库", "section chunk three"),
				packDoc("S1", 4, "部署 > 备份", "other section"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S1 section + S1b direct chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeSection || u.HeadingPath != "部署 > 数据库" {
		t.Fatalf("unit mode/heading = (%s, %q), want (section, 部署 > 数据库)", u.Mode, u.HeadingPath)
	}
	if u.SourceID != "S1" || u.SourceName != "name-S1" {
		t.Fatalf("source = (%s, %s), want (S1, name-S1)", u.SourceID, u.SourceName)
	}
	// 成员 = 节全量块（含未命中兄弟块 #1），不含隔壁节的 #4。
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S1#1", "S1#2", "S1#3"}) {
		t.Fatalf("document ids = %v, want section members [S1#1 S1#2 S1#3]", packUnitIDs(u))
	}
	// Hits 只含命中成员，按 chunk_index 序。
	if !reflect.DeepEqual(packHitIDs(u), []string{"S1#2", "S1#3"}) {
		t.Fatalf("hit ids = %v, want hit members [S1#2 S1#3]", packHitIDs(u))
	}
	if u.Content != "section chunk one\n\nsection chunk two\n\nsection chunk three" {
		t.Fatalf("content = %q, want member contents joined by blank line in chunk order", u.Content)
	}
	// Anchor 取最小索引成员（兄弟块 #1），Score 取成员最高分（rank-1 命中）。
	if u.Anchor != "runes 100-199" {
		t.Fatalf("anchor = %q, want min-index member anchor", u.Anchor)
	}
	k := float64(DefaultKnowledgeSearchFusionRRFK)
	if u.Score != 1/(k+1) {
		t.Fatalf("score = %f, want max member score (rank-1 hit)", u.Score)
	}
	// 来源全量块拉取口径：同空间同固定条件，按 source_id 等值收敛，单次
	// 拉取以防线封顶。
	var f *VectorFilter
	for _, got := range store.listFs {
		if got.Metadata["source_id"] == "S1" {
			f = got
		}
	}
	if f == nil || f.Embedder != testEmbedderSpace {
		t.Fatalf("list filter = %+v, want embedder-scoped source_id enumeration", f)
	}
	if f.Limit != KnowledgeSearchPackListGuardChunks+1 {
		t.Fatalf("list limit = %d, want fetch guard %d", f.Limit, KnowledgeSearchPackListGuardChunks+1)
	}
	if conditionByKey(f.Conditions, "kb_id").Key != "kb_id" ||
		conditionByKey(f.Conditions, VectorMetadataSupersededAt).Op != VectorMetadataIsNull {
		t.Fatalf("list conditions = %+v, want the fixed retrieval conditions carried (including superseded is-null)", f.Conditions)
	}
}

func TestKnowledgeSearchPack_no_heading_adjacent_hits_window_expands_with_siblings(t *testing.T) {
	// 双来源命中使整文档档不适用，无 heading 结构的 S2 落相邻窗口档。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S2", 1, "", "window chunk one"),
			packHit("S2", 2, "", "window chunk two"),
			packHit("S2b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S2": {
				packDoc("S2", 0, "", "window chunk zero"),
				packDoc("S2", 1, "", "window chunk one"),
				packDoc("S2", 2, "", "window chunk two"),
				packDoc("S2", 3, "", "window chunk three"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S2 window + S2b direct chunk)", len(units))
	}
	u := units[0]
	// 窗口范围 = 命中索引 [1,2] 各向外扩 1 块 → [0,3]，成员含未命中兄弟块。
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S2#0", "S2#1", "S2#2", "S2#3"}) {
		t.Fatalf("window members = %v, want expanded range [0,3]", packUnitIDs(u))
	}
	if !reflect.DeepEqual(packHitIDs(u), []string{"S2#1", "S2#2"}) {
		t.Fatalf("hit ids = %v, want hits [S2#1 S2#2]", packHitIDs(u))
	}
	if u.HeadingPath != "" {
		t.Fatalf("heading path = %q, want empty (window tier)", u.HeadingPath)
	}
}

func TestKnowledgeSearchPack_distant_hits_windows_merge_after_expansion(t *testing.T) {
	// 命中 #0 与 #3：索引差 3 > WindowGap 分两窗；外扩后 [0,1] 与 [2,4]
	// 相接，合并为 [0,4] 单一窗口。伴生来源使整文档档不适用。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S3", 0, "", "head chunk"),
			packHit("S3", 3, "", "far chunk"),
			packHit("S3b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S3": {
				packDoc("S3", 0, "", "head chunk"),
				packDoc("S3", 1, "", "sibling one"),
				packDoc("S3", 2, "", "sibling two"),
				packDoc("S3", 3, "", "far chunk"),
				packDoc("S3", 4, "", "sibling three"),
				packDoc("S3", 5, "", "outside chunk"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S3 merged window + S3b direct chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeWindow {
		t.Fatalf("mode = %s, want window", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S3#0", "S3#1", "S3#2", "S3#3", "S3#4"}) {
		t.Fatalf("window members = %v, want merged range [0,4]", packUnitIDs(u))
	}
	if !reflect.DeepEqual(packHitIDs(u), []string{"S3#0", "S3#3"}) {
		t.Fatalf("hit ids = %v, want hits [S3#0 S3#3]", packHitIDs(u))
	}
}

func TestKnowledgeSearchPack_window_expansion_clamps_at_source_boundary(t *testing.T) {
	// 命中来源末块 #2：外扩 [1,3] 夹取到来源边界 [1,2]。伴生来源使整文档
	// 档不适用。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S4", 2, "", "last chunk"),
			packHit("S4b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S4": {
				packDoc("S4", 0, "", "first chunk"),
				packDoc("S4", 1, "", "middle chunk"),
				packDoc("S4", 2, "", "last chunk"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S4 window + S4b direct chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeWindow {
		t.Fatalf("mode = %s, want window", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(units[0]), []string{"S4#1", "S4#2"}) {
		t.Fatalf("window members = %v, want clamped range [1,2]", packUnitIDs(units[0]))
	}
}

func TestKnowledgeSearchPack_single_short_source_returns_document_unit(t *testing.T) {
	// 命中来源唯一且全量 3 块 ≤ DocumentMaxChunks：整文档档，document 单元
	// 入包后同源基础档位（窗口）收编——整源只出一条。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S5", 1, "", "hit chunk")},
		lists: map[string][]*VectorDocument{
			"S5": {
				packDoc("S5", 0, "", "first chunk"),
				packDoc("S5", 1, "", "hit chunk"),
				packDoc("S5", 2, "", "last chunk"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 (document unit subsumes base tier)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeDocument {
		t.Fatalf("mode = %s, want document", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S5#0", "S5#1", "S5#2"}) {
		t.Fatalf("document members = %v, want the whole source", packUnitIDs(u))
	}
	if !reflect.DeepEqual(packHitIDs(u), []string{"S5#1"}) {
		t.Fatalf("hit ids = %v, want hits [S5#1]", packHitIDs(u))
	}
	if u.HeadingPath != "" {
		t.Fatalf("heading path = %q, want first chunk value (empty for plain text)", u.HeadingPath)
	}
}

func TestKnowledgeSearchPack_document_unit_carries_first_chunk_heading(t *testing.T) {
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S6", 1, "安装", "install steps")},
		lists: map[string][]*VectorDocument{
			"S6": {
				packDoc("S6", 0, "总述", "intro"),
				packDoc("S6", 1, "安装", "install steps"),
				packDoc("S6", 2, "配置", "config"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 1 || units[0].Mode != KnowledgeSearchUnitModeDocument {
		t.Fatalf("units = %+v, want one document unit", units)
	}
	if units[0].HeadingPath != "总述" {
		t.Fatalf("heading path = %q, want first chunk value", units[0].HeadingPath)
	}
}

func TestKnowledgeSearchPack_long_source_without_heading_splits_windows(t *testing.T) {
	// 9 块 > DocumentMaxChunks：不按整文档；命中 #0 与 #8 索引差超
	// WindowGap 分两窗，外扩夹取后 [0,1] 与 [7,8]。
	list := make([]*VectorDocument, 0, 9)
	for i := 0; i < 9; i++ {
		list = append(list, packDoc("S7", i, "", fmt.Sprintf("chunk %d", i)))
	}
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S7", 0, "", "chunk 0"),
			packHit("S7", 8, "", "chunk 8"),
		},
		lists: map[string][]*VectorDocument{"S7": list},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 windows", len(units))
	}
	if !reflect.DeepEqual(packUnitIDs(units[0]), []string{"S7#0", "S7#1"}) {
		t.Fatalf("first window = %v, want [S7#0 S7#1]", packUnitIDs(units[0]))
	}
	if !reflect.DeepEqual(packUnitIDs(units[1]), []string{"S7#7", "S7#8"}) {
		t.Fatalf("second window = %v, want [S7#7 S7#8]", packUnitIDs(units[1]))
	}
	if units[0].Mode != KnowledgeSearchUnitModeWindow || units[1].Mode != KnowledgeSearchUnitModeWindow {
		t.Fatalf("modes = (%s, %s), want (window, window)", units[0].Mode, units[1].Mode)
	}
}

func TestKnowledgeSearchPack_section_with_single_hit_returns_chunk_directly(t *testing.T) {
	// 有 heading 结构但命中节仅 1 块：直回档，不扩节成员。伴生来源使整文
	// 档档不适用。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S8", 2, "安装", "lone hit"),
			packHit("S8b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S8": {
				packDoc("S8", 1, "安装", "sibling"),
				packDoc("S8", 2, "安装", "lone hit"),
				packDoc("S8", 3, "配置", "other section"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S8 chunk + S8b chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeChunk || u.SourceID != "S8" {
		t.Fatalf("first unit = (%s, %s), want (chunk, S8)", u.Mode, u.SourceID)
	}
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S8#2"}) {
		t.Fatalf("members = %v, want the hit chunk only", packUnitIDs(u))
	}
}

func TestKnowledgeSearchPack_multi_source_mixed_hits_pack_one_unit_per_source_by_tier(t *testing.T) {
	// 来源 A 同节两命中 → section；来源 B 无标题相邻命中 → window；来源 D
	// 命中块 chunk_index 缺失 → 直回。单元按来源最高分排序，命中全部入包。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("A", 1, "年假", "a-one"),
			packHit("A", 2, "年假", "a-two"),
			packHit("B", 0, "", "b-zero"),
			packHit("B", 1, "", "b-one"),
			{Document: &VectorDocument{ID: "D#5", Content: "d-five", Metadata: map[string]any{
				"source_id": "D", "source_name": "name-D", "anchor": "runes 500-599",
			}}},
		},
		lists: map[string][]*VectorDocument{
			"A": {
				packDoc("A", 0, "考勤", "a-zero"),
				packDoc("A", 1, "年假", "a-one"),
				packDoc("A", 2, "年假", "a-two"),
			},
			"B": {
				packDoc("B", 0, "", "b-zero"),
				packDoc("B", 1, "", "b-one"),
				packDoc("B", 2, "", "b-two"),
				packDoc("B", 3, "", "b-three"),
			},
			"D": {
				packDoc("D", 4, "", "d-four"),
				packDoc("D", 5, "", "d-five"),
				packDoc("D", 6, "", "d-six"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 3 {
		t.Fatalf("units = %d, want 3 (one per source)", len(units))
	}
	wantModes := []string{KnowledgeSearchUnitModeSection, KnowledgeSearchUnitModeWindow, KnowledgeSearchUnitModeChunk}
	wantSources := []string{"A", "B", "D"}
	for i, u := range units {
		if u.Mode != wantModes[i] || u.SourceID != wantSources[i] {
			t.Fatalf("unit %d = (%s, %s), want (%s, %s)", i, u.Mode, u.SourceID, wantModes[i], wantSources[i])
		}
	}
	if !reflect.DeepEqual(packUnitIDs(units[0]), []string{"A#1", "A#2"}) {
		t.Fatalf("section members = %v, want [A#1 A#2]", packUnitIDs(units[0]))
	}
	if !reflect.DeepEqual(packUnitIDs(units[1]), []string{"B#0", "B#1", "B#2"}) {
		t.Fatalf("window members = %v, want [B#0 B#1 B#2]", packUnitIDs(units[1]))
	}
	if !reflect.DeepEqual(packUnitIDs(units[2]), []string{"D#5"}) {
		t.Fatalf("chunk members = %v, want [D#5] (chunk_index missing stays direct)", packUnitIDs(units[2]))
	}
	if got := flattenUnitHits(units); len(got) != 5 {
		t.Fatalf("flattened hits = %d, want all 5 hits covered", len(got))
	}
}

func TestKnowledgeSearchPack_more_than_five_sources_drops_lowest_scored_source(t *testing.T) {
	// 6 来源各一命中：来源按最高分截前 5，rank-6 来源整源消失。
	results := make([]*ScoredVectorDocument, 0, 6)
	for i := 1; i <= 6; i++ {
		results = append(results, packHit(fmt.Sprintf("P%d", i), 0, "", fmt.Sprintf("chunk %d", i)))
	}
	store := &recordingSearchStore{results: results}

	units := packSearch(t, store, 10)
	if len(units) != KnowledgeSearchPackMaxSources {
		t.Fatalf("units = %d, want %d (source cap)", len(units), KnowledgeSearchPackMaxSources)
	}
	for _, u := range units {
		if u.SourceID == "P6" {
			t.Fatalf("lowest-scored source P6 must be dropped entirely, got %s", u.SourceID)
		}
	}
	if got := flattenUnitHits(units); len(got) != KnowledgeSearchPackMaxSources {
		t.Fatalf("flattened hits = %d, want %d (one per kept source)", len(got), KnowledgeSearchPackMaxSources)
	}
}

// packEntryFor 构造直回档装配条目（来源独立、单成员）。
func packEntryFor(sourceID string, score float64, content string) *packEntry {
	return buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{{
		id: sourceID + "#0", index: 0, sourceID: sourceID, sourceName: "name-" + sourceID,
		content: content, score: score, hit: true,
	}})
}

func TestKnowledgeSearchPack_assembly_drops_low_score_units_over_budget(t *testing.T) {
	// 预算 45：s1（25 tokens）入包后剩 20；s2 的 2000-token 单元放不下且无
	// 降级档，整源丢弃并告警；s3（1 token）入包。分数最高的 s1 与能放下的
	// s3 保留，超预算的 s2 被截掉。
	entries := []*packEntry{
		packEntryFor("s1", 0.9, strings.Repeat("a", 100)),  // 25 tokens
		packEntryFor("s2", 0.5, strings.Repeat("a", 8000)), // 2000 tokens
		packEntryFor("s3", 0.3, "b"),                       // 1 token
	}

	units := assemblePackUnits(entries, 45)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (over-budget source dropped)", len(units))
	}
	if units[0].SourceID != "s1" || units[1].SourceID != "s3" {
		t.Fatalf("kept sources = (%s, %s), want (s1, s3) by score order", units[0].SourceID, units[1].SourceID)
	}
}

func TestKnowledgeSearchPack_breadth_pass_downgrades_to_fitting_unit_per_source(t *testing.T) {
	// 预算 100：s1（90）入包后剩 10；s2 的最高分单元（100 tokens）放不下，
	// 降级到能放下的次高分单元（5 tokens）——每源至少一块。
	entries := []*packEntry{
		packEntryFor("s1", 0.9, strings.Repeat("a", 360)), // 90 tokens
		packEntryFor("s2", 0.8, strings.Repeat("a", 400)), // 100 tokens
		packEntryFor("s2", 0.4, strings.Repeat("a", 20)),  // 5 tokens
	}

	units := assemblePackUnits(entries, 100)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (one per source)", len(units))
	}
	if units[0].SourceID != "s1" || units[0].Score != 0.9 {
		t.Fatalf("first unit = (%s, %f), want (s1, 0.9)", units[0].SourceID, units[0].Score)
	}
	if units[1].SourceID != "s2" || units[1].Score != 0.4 {
		t.Fatalf("second unit = (%s, %f), want the downgraded s2 unit (0.4)", units[1].SourceID, units[1].Score)
	}
}

func TestKnowledgeSearchPack_depth_pass_skips_oversized_and_continues(t *testing.T) {
	// 预算 100：广度 D1（50）、E1（30）入包后剩 20；深度段 D2（50 tokens）
	// 放不下被跳过，后续更小的 E2（10 tokens）仍收入——跳过不终止。
	entries := []*packEntry{
		packEntryFor("s1", 0.9, strings.Repeat("a", 200)), // 50 tokens
		packEntryFor("s1", 0.8, strings.Repeat("a", 200)), // 50 tokens
		packEntryFor("s2", 0.3, strings.Repeat("a", 120)), // 30 tokens
		packEntryFor("s2", 0.1, strings.Repeat("a", 40)),  // 10 tokens
	}

	units := assemblePackUnits(entries, 100)
	if len(units) != 3 {
		t.Fatalf("units = %d, want 3 (oversized depth unit skipped, smaller later one kept)", len(units))
	}
	wantScores := []float64{0.9, 0.3, 0.1}
	for i, u := range units {
		if u.Score != wantScores[i] {
			t.Fatalf("unit %d score = %f, want %f", i, u.Score, wantScores[i])
		}
	}
}

func TestKnowledgeSearchPack_document_unit_subsumes_sibling_units_of_same_source(t *testing.T) {
	// document 单元入包后同源 chunk 单元收编（整源内容不重复出包），他源
	// 照常。
	docMembers := []*packChunk{
		{id: "s1#0", index: 0, sourceID: "s1", content: "a", score: 0.9, hit: true},
		{id: "s1#1", index: 1, sourceID: "s1", content: "b"},
		{id: "s1#2", index: 2, sourceID: "s1", content: "c"},
	}
	entries := []*packEntry{
		buildPackEntry(KnowledgeSearchUnitModeDocument, docMembers),
		buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{
			{id: "s1#1", index: 1, sourceID: "s1", content: "b", score: 0.8, hit: true},
		}),
		packEntryFor("s2", 0.7, "d"),
	}

	units := assemblePackUnits(entries, 4096)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (document subsumes same-source chunk)", len(units))
	}
	if units[0].Mode != KnowledgeSearchUnitModeDocument || units[0].SourceID != "s1" {
		t.Fatalf("first unit = (%s, %s), want (document, s1)", units[0].Mode, units[0].SourceID)
	}
	if units[1].SourceID != "s2" {
		t.Fatalf("second unit source = %s, want s2", units[1].SourceID)
	}
}

func TestKnowledgeSearchPack_fitting_base_unit_kills_document_unit_of_same_source(t *testing.T) {
	// document 单元超预算降级到同源直回块后，document 单元被收编（后续不
	// 再以整文档重复出包）。
	docMembers := []*packChunk{
		{id: "s1#0", index: 0, sourceID: "s1", content: strings.Repeat("a", 36000)},
		{id: "s1#1", index: 1, sourceID: "s1", content: strings.Repeat("a", 36000), score: 0.9, hit: true},
	}
	entries := []*packEntry{
		buildPackEntry(KnowledgeSearchUnitModeDocument, docMembers),
		buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{
			{id: "s1#1", index: 1, sourceID: "s1", content: "b", score: 0.8, hit: true},
		}),
		packEntryFor("s2", 0.7, "d"),
	}

	units := assemblePackUnits(entries, 30)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (document dropped after downgrade)", len(units))
	}
	if units[0].Mode != KnowledgeSearchUnitModeChunk || units[0].SourceID != "s1" {
		t.Fatalf("first unit = (%s, %s), want the downgraded s1 chunk", units[0].Mode, units[0].SourceID)
	}
	if units[1].SourceID != "s2" {
		t.Fatalf("second unit source = %s, want s2", units[1].SourceID)
	}
}

func TestKnowledgeSearchPack_document_over_budget_downgrades_to_window(t *testing.T) {
	// 端到端：单源 6 块（≤ DocumentMaxChunks）整文档档，单块 6000 ASCII 字
	// 符（估算 1500 tokens）——整文档 9000 超预算，降级到窗口 [0,1]
	//（3000 tokens ≤ 4096）。
	list := make([]*VectorDocument, 0, 6)
	for i := 0; i < 6; i++ {
		list = append(list, packDoc("S9", i, "", strings.Repeat("a", 6000)))
	}
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S9", 0, "", strings.Repeat("a", 6000))},
		lists:   map[string][]*VectorDocument{"S9": list},
	}

	units := packSearch(t, store, 5)
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 (document downgraded to window)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeWindow {
		t.Fatalf("mode = %s, want window (document over budget)", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S9#0", "S9#1"}) {
		t.Fatalf("window members = %v, want [S9#0 S9#1]", packUnitIDs(u))
	}
	if estimatePackTokens(u.Content) > KnowledgeSearchPackBudgetTokens {
		t.Fatalf("unit tokens = %d, want within budget %d", estimatePackTokens(u.Content), KnowledgeSearchPackBudgetTokens)
	}
}

func TestKnowledgeSearchPack_document_over_budget_downgrades_to_chunk(t *testing.T) {
	// 端到端：单块 9000 ASCII 字符（估算 2250 tokens）时窗口两块 4500 仍超
	// 预算，降级到直回档（2250 ≤ 4096）。
	list := make([]*VectorDocument, 0, 6)
	for i := 0; i < 6; i++ {
		list = append(list, packDoc("S10", i, "", strings.Repeat("a", 9000)))
	}
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S10", 0, "", strings.Repeat("a", 9000))},
		lists:   map[string][]*VectorDocument{"S10": list},
	}

	units := packSearch(t, store, 5)
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 (document downgraded to chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeChunk {
		t.Fatalf("mode = %s, want chunk (window over budget too)", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S10#0"}) {
		t.Fatalf("members = %v, want [S10#0]", packUnitIDs(u))
	}
}

func TestKnowledgeSearchPack_list_failure_degrades_to_direct_chunk_units(t *testing.T) {
	// 来源全量块拉取失败：聚合按质量增强降级，命中块逐条直回，检索不失败。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S11", 1, "年假", "hit one"),
			packHit("S11", 2, "年假", "hit two"),
		},
		listErr: errors.New("source enumeration failed"),
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (degraded to direct chunks)", len(units))
	}
	for i, u := range units {
		if u.Mode != KnowledgeSearchUnitModeChunk {
			t.Fatalf("unit %d mode = %s, want chunk (list failure degradation)", i, u.Mode)
		}
	}
	if got := flattenUnitHits(units); len(got) != 2 {
		t.Fatalf("flattened hits = %d, want both hits kept", len(got))
	}
}

func TestKnowledgeSearchPack_oversized_source_degrades_to_direct_chunk_units(t *testing.T) {
	// 来源全量块 1025 > 防线（1024）：不做任何聚合（含窗口），命中逐条直回。
	list := make([]*VectorDocument, 0, KnowledgeSearchPackListGuardChunks+1)
	for i := 0; i < KnowledgeSearchPackListGuardChunks+1; i++ {
		list = append(list, packDoc("S12", i, "", fmt.Sprintf("chunk %d", i)))
	}
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S12", 0, "", "chunk 0"),
			packHit("S12", 2, "", "chunk 2"),
		},
		lists: map[string][]*VectorDocument{"S12": list},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (guard-exceeded source stays direct)", len(units))
	}
	for i, u := range units {
		if u.Mode != KnowledgeSearchUnitModeChunk {
			t.Fatalf("unit %d mode = %s, want chunk (fetch guard degradation)", i, u.Mode)
		}
	}
	var last *VectorFilter
	if len(store.listFs) > 0 {
		last = store.listFs[len(store.listFs)-1]
	}
	if last == nil || last.Limit != KnowledgeSearchPackListGuardChunks+1 {
		t.Fatalf("list limit = %+v, want fetch guard %d", last, KnowledgeSearchPackListGuardChunks+1)
	}
}

func TestKnowledgeSearchPack_jsonb_float64_chunk_index_joins_window(t *testing.T) {
	// jsonb 反序列化形态（float64）的 chunk_index 与写入形态（int）同路：
	// 命中块 #1 以 float64 携带索引，与整型兄弟块同窗。伴生来源使整文档
	// 档不适用。
	hit := packHit("S13", 1, "", "float index hit")
	hit.Document.Metadata["chunk_index"] = float64(1)
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			hit,
			packHit("S13b", 0, "", "other source chunk"),
		},
		lists: map[string][]*VectorDocument{
			"S13": {
				packDoc("S13", 0, "", "int sibling"),
				func() *VectorDocument {
					d := packDoc("S13", 1, "", "float index hit")
					d.Metadata["chunk_index"] = float64(1)
					return d
				}(),
				packDoc("S13", 2, "", "int sibling two"),
			},
		},
	}

	units := packSearch(t, store, 5)
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2 (S13 window + S13b direct chunk)", len(units))
	}
	u := units[0]
	if u.Mode != KnowledgeSearchUnitModeWindow {
		t.Fatalf("mode = %s, want window", u.Mode)
	}
	if !reflect.DeepEqual(packUnitIDs(units[0]), []string{"S13#0", "S13#1", "S13#2"}) {
		t.Fatalf("window members = %v, want [S13#0 S13#1 S13#2] across index encodings", packUnitIDs(units[0]))
	}
}

func TestKnowledgeSearchPack_list_missing_hit_chunk_is_added_back_with_full_hit_coverage(t *testing.T) {
	// 来源全量块缺漏一个命中块（List 未返回 S14#2）：缺漏命中补进宇宙，
	// 回包仍完整覆盖全部命中且不丢失该块。
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{
			packHit("S14", 1, "年假", "hit one"),
			packHit("S14", 2, "年假", "hit two"),
		},
		lists: map[string][]*VectorDocument{
			"S14": {
				packDoc("S14", 0, "年假", "sibling zero"),
				packDoc("S14", 1, "年假", "hit one"),
				// S14#2 缺漏。
			},
		},
	}

	units := packSearch(t, store, 5)
	// 单来源全量块（补回后 3 块）≤ 8：整文档档。
	if len(units) != 1 || units[0].Mode != KnowledgeSearchUnitModeDocument {
		t.Fatalf("units = %+v, want one document unit", units)
	}
	u := units[0]
	if !reflect.DeepEqual(packUnitIDs(u), []string{"S14#0", "S14#1", "S14#2"}) {
		t.Fatalf("members = %v, want missing hit added back [S14#0 S14#1 S14#2]", packUnitIDs(u))
	}
	if !reflect.DeepEqual(packHitIDs(u), []string{"S14#1", "S14#2"}) {
		t.Fatalf("hit ids = %v, want both hits covered [S14#1 S14#2]", packHitIDs(u))
	}
}

func TestKnowledgeSearchPack_list_rows_from_other_source_are_excluded_from_members(t *testing.T) {
	// 来源全量块含一条异 source_id 行（List 过滤失守的替身形态）：异源行
	// 不入任何回包单元的成员 DocumentIDs。
	alien := packDoc("S15-other", 3, "", "alien row")
	store := &recordingSearchStore{
		results: []*ScoredVectorDocument{packHit("S15", 1, "", "hit chunk")},
		lists: map[string][]*VectorDocument{
			"S15": {
				packDoc("S15", 0, "", "sibling zero"),
				packDoc("S15", 1, "", "hit chunk"),
				packDoc("S15", 2, "", "sibling two"),
				alien,
			},
		},
	}

	units := packSearch(t, store, 5)
	// 单来源全量块（异源行丢弃后 3 块）≤ 8：整文档档，成员精确等于本源
	// 三块——异源行不出现。
	if len(units) != 1 || units[0].Mode != KnowledgeSearchUnitModeDocument {
		t.Fatalf("units = %+v, want one document unit", units)
	}
	if !reflect.DeepEqual(packUnitIDs(units[0]), []string{"S15#0", "S15#1", "S15#2"}) {
		t.Fatalf("members = %v, want source-own chunks only (%s excluded)",
			packUnitIDs(units[0]), alien.ID)
	}
}

// packStageReasonFor 取 pack 阶段留痕里指定成员切片 ID 的未入包原因。
func packStageReasonFor(units []retrievalPackUnit, documentID string) retrievalPackUnit {
	for _, u := range units {
		for _, id := range u.DocumentIDs {
			if id == documentID {
				return u
			}
		}
	}
	return retrievalPackUnit{}
}

func TestKnowledgeSearchPackStageUnits_reason_marks_same_source_merge_as_merged(t *testing.T) {
	// document 单元入包后同源 chunk 单元被收编：留痕 reason 必须是 merged
	//（不是截断），入包单元省略 reason。
	docMembers := []*packChunk{
		{id: "s1#0", index: 0, sourceID: "s1", content: "a", score: 0.9, hit: true},
		{id: "s1#1", index: 1, sourceID: "s1", content: "b"},
	}
	entries := []*packEntry{
		buildPackEntry(KnowledgeSearchUnitModeDocument, docMembers),
		buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{
			{id: "s1#1", index: 1, sourceID: "s1", content: "b", score: 0.8, hit: true},
		}),
	}
	units := assemblePackUnits(entries, 4096)

	stages := packStageUnits(entries, units)
	if len(stages) != 2 {
		t.Fatalf("stage units = %d, want 2", len(stages))
	}
	if !stages[0].Admitted || stages[0].Reason != "" {
		t.Fatalf("document unit = %+v, want admitted without reason", stages[0])
	}
	if stages[1].Admitted || stages[1].Reason != RetrievalPackUnitReasonMerged {
		t.Fatalf("same-source chunk unit = %+v, want reason merged", stages[1])
	}
}

func TestKnowledgeSearchPackStageUnits_reason_marks_fallback_only_unit_as_redundant(t *testing.T) {
	// 来源已有入包单元时保底直回单元不参与深度补深：留痕 reason 必须是
	// redundant（不是截断）。
	regular := packEntryFor("s1", 0.9, strings.Repeat("a", 200)) // 50 tokens, broad pass admits
	fallback := packEntryFor("s1", 0.1, "b")
	fallback.fallbackOnly = true
	entries := []*packEntry{regular, fallback}
	units := assemblePackUnits(entries, 100)
	if len(units) != 1 {
		t.Fatalf("units = %d, want only the regular unit admitted", len(units))
	}

	stages := packStageUnits(entries, units)
	if got := packStageReasonFor(stages, "s1#0"); !got.Admitted || got.Reason != "" {
		t.Fatalf("regular unit = %+v, want admitted without reason", got)
	}
	// 两个条目成员切片 ID 相同（s1#0），按顺序取第二个（保底）单元。
	if stages[1].Admitted || stages[1].Reason != RetrievalPackUnitReasonRedundant {
		t.Fatalf("fallback unit = %+v, want reason redundant", stages[1])
	}
}

func TestKnowledgeSearchPackStageUnits_reason_marks_budget_overflow_as_truncated(t *testing.T) {
	// 预算放不下且无收编关系的单元：留痕 reason 是 truncated。
	entries := []*packEntry{
		packEntryFor("s1", 0.9, "a"),                       // 1 token
		packEntryFor("s2", 0.5, strings.Repeat("a", 8000)), // 2000 tokens, budget 45
	}
	units := assemblePackUnits(entries, 45)
	if len(units) != 1 {
		t.Fatalf("units = %d, want only s1 admitted", len(units))
	}

	stages := packStageUnits(entries, units)
	if got := packStageReasonFor(stages, "s1#0"); !got.Admitted || got.Reason != "" {
		t.Fatalf("admitted unit = %+v, want no reason", got)
	}
	if got := packStageReasonFor(stages, "s2#0"); got.Admitted || got.Reason != RetrievalPackUnitReasonTruncated {
		t.Fatalf("over-budget unit = %+v, want reason truncated", got)
	}
}

func TestPackUnitReason_dead_beats_fallback_only(t *testing.T) {
	// 同时被同源收编且是保底单元时按收编归因（merged 优先于 redundant，
	// 与装配期先跳 dead 的跳过顺序一致）。
	e := packEntryFor("s1", 0.5, "a")
	e.dead = true
	e.fallbackOnly = true
	if got := packUnitReason(e, false, true); got != RetrievalPackUnitReasonMerged {
		t.Fatalf("packUnitReason(dead+fallbackOnly) = %q, want merged", got)
	}
	if got := packUnitReason(e, true, true); got != "" {
		t.Fatalf("packUnitReason(admitted) = %q, want empty", got)
	}
}

func TestPackUnitReason_fallback_only_without_admitted_source_is_truncated(t *testing.T) {
	// 来源整体被预算丢弃（无任何单元入包）时，保底直回单元与同源其余单元
	// 同为预算截断，不能标成冗余。
	e := packEntryFor("s1", 0.5, "a")
	e.fallbackOnly = true
	if got := packUnitReason(e, false, false); got != RetrievalPackUnitReasonTruncated {
		t.Fatalf("packUnitReason(fallbackOnly, source dropped) = %q, want truncated", got)
	}
	if got := packUnitReason(e, false, true); got != RetrievalPackUnitReasonRedundant {
		t.Fatalf("packUnitReason(fallbackOnly, source admitted) = %q, want redundant", got)
	}
}

func TestKnowledgeSearchPackStageUnits_dropped_source_fallback_is_truncated_end_to_end(t *testing.T) {
	// 真实装配路径（packSearchUnits → packLastResortEntry → assemblePackUnits
	// → packStageUnits）：5 个来源各约 1000 token，预算 4096 只装得下 4 源，
	// 第 5 源整体被丢弃——其保底直回单元与窗口单元都应归因 truncated；正常
	// 入包来源的保底单元才是 redundant。
	store := &recordingSearchStore{lists: map[string][]*VectorDocument{}}
	hits := make([]*KnowledgeSearchHit, 0, 5)
	for i := 1; i <= 5; i++ {
		sourceID := fmt.Sprintf("s%d", i)
		content := strings.Repeat("a", 4000) // 1000 tokens
		store.lists[sourceID] = []*VectorDocument{packDoc(sourceID, 0, "", content)}
		hits = append(hits, &KnowledgeSearchHit{
			DocumentID: sourceID + "#0",
			SourceID:   sourceID,
			SourceName: "name-" + sourceID,
			ChunkIndex: 0,
			Score:      1 / float64(i),
			Content:    content,
		})
	}
	uc := newSearchUsecaseForTest(t, store, &stubEmbedder{dims: 4}, &stubResolver{space: testEmbedderSpace, dim: 4}, defaultSearchLibraries())

	units, stageUnits := uc.packSearchUnits(searchCtx(), hits, testEmbedderSpace, nil)
	if len(units) != 4 {
		t.Fatalf("units = %d, want 4 sources within budget", len(units))
	}
	admittedSources := map[string]bool{}
	for _, u := range units {
		admittedSources[u.SourceID] = true
	}
	if !admittedSources["s1"] || admittedSources["s5"] {
		t.Fatalf("admitted sources = %v, want s1 admitted and s5 dropped", admittedSources)
	}

	var droppedReasons, admittedFallbackReasons []string
	for _, u := range stageUnits {
		switch {
		case u.SourceID == "s5":
			droppedReasons = append(droppedReasons, u.Reason)
		case u.SourceID == "s1" && !u.Admitted:
			admittedFallbackReasons = append(admittedFallbackReasons, u.Reason)
		}
	}
	// 被丢弃来源的两个单元（窗口 + 保底直回）都必须是 truncated。
	if !reflect.DeepEqual(droppedReasons, []string{
		RetrievalPackUnitReasonTruncated,
		RetrievalPackUnitReasonTruncated,
	}) {
		t.Fatalf("dropped source reasons = %v, want both truncated", droppedReasons)
	}
	// 正常入包来源的保底直回单元是 redundant。
	if !reflect.DeepEqual(admittedFallbackReasons, []string{RetrievalPackUnitReasonRedundant}) {
		t.Fatalf("admitted source fallback reasons = %v, want redundant", admittedFallbackReasons)
	}
}

func TestKnowledgeSearchPack_token_estimator_exact_values(t *testing.T) {
	for _, tc := range []struct {
		name   string
		text   string
		tokens int
	}{
		{"empty_text_yields_0", "", 0},
		{"pure_cjk_8_runes_yield_8", "检检检检检检检检", 8},
		{"pure_ascii_8_runes_yield_2", "abcdefgh", 2},
		{"ascii_5_runes_round_remainder_up_to_2", "abcde", 2},
		{"mixed_2_cjk_2_ascii_yield_3", "检a倉b", 3},
		{"kana_2_runes_yield_2", "かな", 2},
		{"hangul_2_runes_yield_2", "한글", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimatePackTokens(tc.text); got != tc.tokens {
				t.Fatalf("estimatePackTokens(%q) = %d, want %d", tc.text, got, tc.tokens)
			}
		})
	}
}
