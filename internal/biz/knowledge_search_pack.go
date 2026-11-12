package biz

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
)

// 检索回包聚合（pack）：命中小块后按来源自动分档，聚合成面向模型的回包
// 单元——同节多块命中拼整节、无 heading 结构的来源按 chunk_index 相邻窗
// 口、独占命中的短文档整文档、其余小块直回。聚合与预算封顶都在检索编排
// 层（与 RRF / rerank 同层），工具层只消费已聚合的回包单元做围栏包装——
// 留痕行在 store.Search 调用时已写完，pack 事实只有落到编排层才有安放点。
//
// 分档与预算不暴露任何模型/调用方参数：模型自选粒度没有判断依据，且会打
// 开爆 token 预算的口子，与 top_k 硬钳同一纪律（参数属代码级默认，不进
// conf）。

// 回包单元的四档粒度（与留痕 stages 规划的词汇对齐）。
const (
	KnowledgeSearchUnitModeChunk    = "chunk"
	KnowledgeSearchUnitModeWindow   = "window"
	KnowledgeSearchUnitModeSection  = "section"
	KnowledgeSearchUnitModeDocument = "document"
)

// 回包聚合纪律（代码级默认，与 top_k 硬钳同纪律，不进 conf）：
//   - BudgetTokens：回包总预算（按确定性估算函数计 token 量级，非精确
//     分词），超预算按分数截断；
//   - MaxSources：参与聚合的来源数上限，超出来源整体丢弃；
//   - SectionMinHits：同 heading_path 命中块数达到该值才整节返回；
//   - WindowGap：相邻窗口的命中 chunk_index 差上限；
//   - DocumentMaxChunks：整文档档的来源全量块数上限（超过不按整文档处理）；
//   - ListGuardChunks：来源全量块经 store.List 拉取的防线上限，超线来源
//     不做任何聚合、全部直回（成员完整性优先于聚合粒度）。
const (
	KnowledgeSearchPackBudgetTokens      = 4096
	KnowledgeSearchPackMaxSources        = 5
	KnowledgeSearchPackSectionMinHits    = 2
	KnowledgeSearchPackWindowGap         = 2
	KnowledgeSearchPackDocumentMaxChunks = 8
	KnowledgeSearchPackListGuardChunks   = 1024
)

// KnowledgeSearchUnit 是检索回包的聚合单元，KnowledgeSearchUsecase.Search
// 的返回粒度。消费方两类：console 检索展平各单元的 Hits（条目集合即命中
// 切片集合，与聚合前的命中形状一致），agent 检索工具逐单元一条出参（mode
// 声明聚合粒度，content 为单元整段正文）。
//
// 各档语义（Mode）：
//   - chunk：成员 = 单个命中切片（无结构可聚合、单节单命中或聚合降级）。
//   - window：无 heading 结构来源的相邻窗口，成员 = 来源全量块中落在窗口
//     索引区间的所有块（含未命中兄弟块）。
//   - section：同 heading_path 命中达到 SectionMinHits 的整节，成员 = 该节
//     全量块（含未命中兄弟块）。
//   - document：命中来源唯一且来源全量块数 ≤ DocumentMaxChunks 的整文档。
//
// Score 是单元分 = 成员最高分：装配了重排层且本次调用成功时为 rerank 相关
// 性分，否则（未配置重排、重排失败降级或未装配）为 RRF 融合分——与
// KnowledgeSearchHit.Score 同口径，两种分仅同次检索内可比、不代表相似度。
// Content 是成员正文按 chunk_index 序以 "\n\n" 连接（不做围栏包装——围栏
// 是模型面语义）。DocumentIDs 是全部成员切片 ID（含聚合进来的兄弟块），
// Hits 仅本次命中的成员块——两者差集即兄弟块。HeadingPath 仅 section 档
// 承载节路径、document 档承载首块值；chunk/window 恒空。Anchor 是单元内
// chunk_index 最小成员的锚点。单元按 Score 降序返回（同分按来源序与最小
// 块索引，确定性全序）。
type KnowledgeSearchUnit struct {
	Mode        string
	SourceID    string
	SourceName  string
	HeadingPath string
	Anchor      string
	Score       float64
	Content     string
	DocumentIDs []string
	Hits        []*KnowledgeSearchHit
}

// packChunk 是聚合成员的统一投影：命中块与来源全量块同形参与分档、定序
// 与拼接；命中块额外带命中分与 KnowledgeSearchHit 引用（unit.Hits 复用
// 同一对象，不重复映射）。
type packChunk struct {
	id         string
	index      int // chunk_index；metadata 缺失/不可解析为 -1（不参与窗口区间，恒直回）
	heading    string
	anchor     string
	sourceID   string
	sourceName string
	content    string
	score      float64
	hit        bool
	ref        *KnowledgeSearchHit
}

// packEntry 是装配期的单元承载：unit 之外带分档派生的排序键（最小块索引）
// 与预算装配期的分组/记账状态。全序 =（Score 降序，来源首现序，最小块索引
// 升序）。fallbackOnly 标记来源的保底直回单元：只参与广度段的降级兜底，
// 深度段恒跳过（来源已有入包单元时它只会重复内容）。
type packEntry struct {
	unit     *KnowledgeSearchUnit
	minIndex int

	key          string // 来源分组键（装配期填充；空 source_id 的单元各自成组）
	seq          int    // 来源首现序（装配期填充）
	tokens       int
	admitted     bool
	dead         bool
	fallbackOnly bool
}

// packSearchUnits 是回包聚合的编排入口：命中块按来源分组、按最高分截前
// MaxSources（超出来源整体丢弃），逐来源取全量块分档聚合，最后按固定预
// 算两段装配。来源全量块与命中块同宇宙（同空间同固定检索条件），聚合粒
// 度不越过可见性边界。
func (uc *KnowledgeSearchUsecase) packSearchUnits(ctx context.Context, hits []*KnowledgeSearchHit, space string, conditions []MetadataCondition) []*KnowledgeSearchUnit {
	groups := groupPackSources(hits)
	if len(groups) == 0 {
		return []*KnowledgeSearchUnit{}
	}
	singleSource := len(groups) == 1
	if len(groups) > KnowledgeSearchPackMaxSources {
		// 回包广度纪律：来源数上限之外的整源丢弃（来源按最高分排序）。
		slog.WarnContext(ctx, "pack source cap exceeded; dropping lowest-scored sources",
			slog.Int("dropped", len(groups)-KnowledgeSearchPackMaxSources),
			slog.Int("cap", KnowledgeSearchPackMaxSources))
		groups = groups[:KnowledgeSearchPackMaxSources]
	}
	entries := make([]*packEntry, 0, len(groups))
	for _, src := range groups {
		universe := mergePackUniverse(src.chunks, uc.fetchPackSourceChunks(ctx, src, space, conditions))
		entries = append(entries, packSourceUnits(src, universe, singleSource)...)
		if e := packLastResortEntry(src, universe); e != nil {
			entries = append(entries, e)
		}
	}
	return assemblePackUnits(entries, KnowledgeSearchPackBudgetTokens)
}

// packLastResortEntry 是来源的保底直回单元（命中分最高的命中块单块直回）：
// 广度段在整文档/整节/窗口都放不下时以此保证「每源至少一块」。直回档来源
//（来源全量块不可得）的成员已是保底形态，不再重复保底。
func packLastResortEntry(src *packSource, universe []*packChunk) *packEntry {
	if universe == nil {
		return nil
	}
	best := src.chunks[0]
	for _, c := range src.chunks[1:] {
		if c.score > best.score {
			best = c
		}
	}
	e := buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{best})
	e.fallbackOnly = true
	return e
}

// groupPackSources 把命中块按 metadata 的 source_id 分组：空 source_id 的
// 命中各自成组（来源缺失无法取回兄弟块），组内保首现序。返回组按最高分
// 降序、同分按首现序（确定性）。
func groupPackSources(hits []*KnowledgeSearchHit) []*packSource {
	byKey := map[string]*packSource{}
	sources := make([]*packSource, 0, len(hits))
	for _, h := range hits {
		if h == nil {
			continue
		}
		key := h.SourceID
		if key == "" {
			key = "\x00hit:" + h.DocumentID
		}
		src, ok := byKey[key]
		if !ok {
			src = &packSource{firstSeen: len(sources)}
			byKey[key] = src
			sources = append(sources, src)
		}
		src.chunks = append(src.chunks, packChunkFromHit(h))
		if h.Score > src.maxScore {
			src.maxScore = h.Score
		}
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].maxScore != sources[j].maxScore {
			return sources[i].maxScore > sources[j].maxScore
		}
		return sources[i].firstSeen < sources[j].firstSeen
	})
	return sources
}

// packSource 是按来源分组的命中集合。
type packSource struct {
	chunks    []*packChunk // 本次命中的成员块
	maxScore  float64      // 成员最高分（来源排序依据）
	firstSeen int          // 首现序（同分 tie-break）
}

// fetchPackSourceChunks 经 store.List 取回来源全量块（source_id 等值 + 固
// 定检索条件 + 检索空间收敛，单次拉取以防线封顶），按 chunk_index 升序排。
// 来源缺失、拉取失败或超过防线返回 nil——聚合降级为仅命中块直回，不让整
// 次检索失败（聚合是质量增强非正确性依赖；超防线是合法的大文档形态，不
// 告警）。
func (uc *KnowledgeSearchUsecase) fetchPackSourceChunks(ctx context.Context, src *packSource, space string, conditions []MetadataCondition) []*packChunk {
	sourceID := src.chunks[0].sourceID
	if sourceID == "" {
		return nil
	}
	docs, err := uc.store.List(ctx, &VectorFilter{
		Embedder:   space,
		Metadata:   map[string]any{"source_id": sourceID},
		Conditions: conditions,
		Limit:      KnowledgeSearchPackListGuardChunks + 1,
	})
	if err != nil {
		slog.WarnContext(ctx, "pack source chunk listing failed; falling back to hit chunks",
			slog.String("source_id", sourceID), slog.Any("error", err))
		return nil
	}
	if len(docs) > KnowledgeSearchPackListGuardChunks {
		return nil
	}
	universe := make([]*packChunk, 0, len(docs))
	for _, d := range docs {
		if d == nil {
			continue
		}
		universe = append(universe, packChunkFromDocument(d))
	}
	sortPackChunks(universe)
	return universe
}

// mergePackUniverse 把命中块对齐进来源全量块：同 ID 即同一块，命中分与
// 命中引用并入全量块；不属于本来源的枚举行丢弃（List 过滤失守时不跨源聚
// 合），全量块缺漏的命中块补进宇宙（替身或边界实现不完整时任何命中不得因
// 聚合丢失）。universe 为 nil（来源全量块不可得）时保持 nil，调用方按仅
// 命中块直回降级。
func mergePackUniverse(hits, universe []*packChunk) []*packChunk {
	if universe == nil {
		return nil
	}
	sourceID := hits[0].sourceID
	byID := make(map[string]*packChunk, len(hits))
	for _, h := range hits {
		byID[h.id] = h
	}
	kept := make([]*packChunk, 0, len(universe)+len(byID))
	for _, f := range universe {
		if f.sourceID != sourceID {
			continue
		}
		if h, ok := byID[f.id]; ok {
			f.hit = true
			f.score = h.score
			f.ref = h.ref
			delete(byID, f.id)
		}
		kept = append(kept, f)
	}
	for _, h := range byID {
		kept = append(kept, h)
	}
	sortPackChunks(kept)
	return kept
}

// packSourceUnits 对单个来源做自动分档：独占命中的短来源整文档（另产出
// 基础档位作为预算降级备份），有 heading 结构的按节聚合，无 heading 结构
// 的按相邻窗口，其余直回。返回单元按（分数降序，最小块索引升序）排列。
func packSourceUnits(src *packSource, universe []*packChunk, singleSource bool) []*packEntry {
	base := packBaseEntries(src.chunks, universe)
	if singleSource && len(universe) > 0 && len(universe) <= KnowledgeSearchPackDocumentMaxChunks {
		return append([]*packEntry{buildPackEntry(KnowledgeSearchUnitModeDocument, universe)}, base...)
	}
	return base
}

// packBaseEntries 是来源的基础档位（也是整文档档的预算降级备份）：来源
// 全量块不可得（List 失败/超防线/来源缺失）时全部直回；有 heading 结构
//（任一命中块 heading_path 非空）按节聚合，否则相邻窗口。
func packBaseEntries(hits, universe []*packChunk) []*packEntry {
	if len(hits) == 0 {
		return nil
	}
	if universe == nil {
		return packChunkEntries(hits)
	}
	for _, c := range hits {
		if c.heading != "" {
			return packHeadingEntries(hits, universe)
		}
	}
	return packWindowEntries(hits, universe)
}

// packHeadingEntries 是有 heading 结构来源的节聚合：同 heading_path 命中
// 达到 SectionMinHits → 整节（成员 = 该节全量块，含未命中兄弟块）；单命中
// 节与无标题命中块（首个标题之前的内容）直回。
func packHeadingEntries(hits, universe []*packChunk) []*packEntry {
	groups := map[string][]*packChunk{}
	headings := make([]string, 0, len(hits))
	for _, c := range hits {
		if c.heading == "" {
			continue
		}
		if _, ok := groups[c.heading]; !ok {
			headings = append(headings, c.heading)
		}
		groups[c.heading] = append(groups[c.heading], c)
	}
	entries := make([]*packEntry, 0, len(headings)+len(hits))
	direct := make([]*packChunk, 0, len(hits))
	for _, heading := range headings {
		if len(groups[heading]) >= KnowledgeSearchPackSectionMinHits {
			if e := buildSectionEntry(universe, heading); e != nil {
				entries = append(entries, e)
				continue
			}
		}
		direct = append(direct, groups[heading]...)
	}
	for _, c := range hits {
		if c.heading == "" {
			direct = append(direct, c)
		}
	}
	entries = append(entries, packChunkEntries(direct)...)
	sortPackEntries(entries)
	return entries
}

// buildSectionEntry 取该节全部全量块装配整节单元；节块缺失（宇宙已对齐
// 命中块，不应发生）返回 nil 由调用方回落直回。
func buildSectionEntry(universe []*packChunk, heading string) *packEntry {
	members := make([]*packChunk, 0, len(universe))
	for _, c := range universe {
		if c.heading == heading {
			members = append(members, c)
		}
	}
	if len(members) == 0 {
		return nil
	}
	return buildPackEntry(KnowledgeSearchUnitModeSection, members)
}

// packWindowEntries 是无 heading 结构来源的相邻窗口档：命中按 chunk_index
// 排序，索引差 ≤ WindowGap 的命中并入同一窗口；窗口范围向两侧各外扩 1 块
//（夹取到来源边界），外扩后重叠/相接的窗口合并；窗口成员 = 来源全量块中
// 落在索引区间内的所有块。索引不可定位（metadata 缺失或越界）的命中直回。
func packWindowEntries(hits, universe []*packChunk) []*packEntry {
	maxIndex := -1
	for _, c := range universe {
		if c.index > maxIndex {
			maxIndex = c.index
		}
	}
	var indexed, direct []*packChunk
	for _, c := range hits {
		if c.index >= 0 && c.index <= maxIndex {
			indexed = append(indexed, c)
		} else {
			direct = append(direct, c)
		}
	}
	entries := make([]*packEntry, 0, len(hits))
	if len(indexed) > 0 {
		sortPackChunks(indexed)
		for _, w := range packWindowSpans(indexed, maxIndex) {
			members := make([]*packChunk, 0, w.hi-w.lo+1)
			for _, c := range universe {
				if c.index >= w.lo && c.index <= w.hi {
					members = append(members, c)
				}
			}
			if len(members) == 0 {
				continue // 窗口锚在命中索引上，不应发生
			}
			entries = append(entries, buildPackEntry(KnowledgeSearchUnitModeWindow, members))
		}
	}
	entries = append(entries, packChunkEntries(direct)...)
	sortPackEntries(entries)
	return entries
}

// packWindowSpan 是窗口的成员索引区间（闭区间）。
type packWindowSpan struct{ lo, hi int }

// packWindowSpans 由命中索引分窗、外扩并合并：索引差 ≤ WindowGap 的命中
// 同窗；每窗向两侧外扩 1 块并夹取到 [0, maxIndex]；外扩后重叠或相接的窗
// 口合并。
func packWindowSpans(indexed []*packChunk, maxIndex int) []packWindowSpan {
	spans := []packWindowSpan{{indexed[0].index, indexed[0].index}}
	for _, c := range indexed[1:] {
		if c.index-spans[len(spans)-1].hi <= KnowledgeSearchPackWindowGap {
			spans[len(spans)-1].hi = c.index
			continue
		}
		spans = append(spans, packWindowSpan{c.index, c.index})
	}
	expanded := make([]packWindowSpan, 0, len(spans))
	for _, s := range spans {
		e := packWindowSpan{s.lo - 1, s.hi + 1}
		if e.lo < 0 {
			e.lo = 0
		}
		if e.hi > maxIndex {
			e.hi = maxIndex
		}
		if n := len(expanded); n > 0 && e.lo <= expanded[n-1].hi+1 {
			if e.hi > expanded[n-1].hi {
				expanded[n-1].hi = e.hi
			}
			continue
		}
		expanded = append(expanded, e)
	}
	return expanded
}

// packChunkEntries 把命中块逐个装配为直回档单元。
func packChunkEntries(chunks []*packChunk) []*packEntry {
	entries := make([]*packEntry, 0, len(chunks))
	for _, c := range chunks {
		entries = append(entries, buildPackEntry(KnowledgeSearchUnitModeChunk, []*packChunk{c}))
	}
	return entries
}

// buildPackEntry 由聚合成员装配回包单元：成员按（chunk_index 升序，切片
// ID 升序）定序——Content 按该序以 "\n\n" 连接，DocumentIDs 收全部成员
// 切片 ID（含未命中兄弟块），Hits 只收本次命中的成员块；Anchor 取最小索
// 引成员，Score 取成员最高分；HeadingPath 仅 section/document 档承载
//（section 为节路径，document 为首块值），chunk/window 恒空。
func buildPackEntry(mode string, members []*packChunk) *packEntry {
	sortPackChunks(members)
	unit := &KnowledgeSearchUnit{
		Mode:        mode,
		SourceID:    members[0].sourceID,
		SourceName:  members[0].sourceName,
		Anchor:      members[0].anchor,
		DocumentIDs: make([]string, 0, len(members)),
	}
	if mode == KnowledgeSearchUnitModeSection || mode == KnowledgeSearchUnitModeDocument {
		unit.HeadingPath = members[0].heading
	}
	contents := make([]string, 0, len(members))
	for _, m := range members {
		unit.DocumentIDs = append(unit.DocumentIDs, m.id)
		contents = append(contents, m.content)
		if m.score > unit.Score {
			unit.Score = m.score
		}
		if m.ref != nil {
			unit.Hits = append(unit.Hits, m.ref)
		}
	}
	unit.Content = strings.Join(contents, "\n\n")
	return &packEntry{unit: unit, minIndex: members[0].index}
}

// sortPackChunks 成员定序：chunk_index 升序（缺失按 -1 沉底），同索引按切
// 片 ID 升序（确定性兜底，正常数据索引唯一）。
func sortPackChunks(chunks []*packChunk) {
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].index != chunks[j].index {
			return chunks[i].index < chunks[j].index
		}
		return chunks[i].id < chunks[j].id
	})
}

// sortPackEntries 来源内单元序：分数降序，同分按最小块索引升序。
func sortPackEntries(entries []*packEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].unit.Score != entries[j].unit.Score {
			return entries[i].unit.Score > entries[j].unit.Score
		}
		return entries[i].minIndex < entries[j].minIndex
	})
}

// assemblePackUnits 是回包的两段预算装配（无 I/O，仅装配侧告警）：先广度
// 段——按来源序逐源取第一个放得下的单元（整文档超预算时自然降级到同源的
// 窗口/直回档，一档都放不下再落到保底直回块，保证每源至少一块；保底也放
// 不下才丢源并记警告）；后深度段——剩余单元按全序逐个尝试，放得下就收、
// 放不下跳过继续，预算用尽即止（保底直回单元不参与深度段）。document 单
// 元入包后收编同源其余单元（内容已被整文档包含），同源非 document 单元入
// 包后整文档单元同样收编——同源聚合粒度互斥，内容不重复出包。返回单元按
// 全序（Score 降序，来源首现序，最小块索引升序）排列。
func assemblePackUnits(entries []*packEntry, budget int) []*KnowledgeSearchUnit {
	sorted := make([]*packEntry, len(entries))
	copy(sorted, entries)
	firstSeen := map[string]int{}
	for i, e := range sorted {
		if e == nil || e.unit == nil {
			continue
		}
		if e.unit.SourceID != "" {
			e.key = e.unit.SourceID
		} else {
			e.key = strconv.Itoa(i) // 来源缺失的直回块各自成组
		}
		e.tokens = estimatePackTokens(e.unit.Content)
		if _, ok := firstSeen[e.key]; !ok {
			firstSeen[e.key] = i
		}
	}
	for _, e := range sorted {
		if e != nil {
			e.seq = firstSeen[e.key]
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.unit.Score != b.unit.Score {
			return a.unit.Score > b.unit.Score
		}
		if a.seq != b.seq {
			return a.seq < b.seq
		}
		return a.minIndex < b.minIndex
	})

	remaining := budget
	// 广度段：每来源按全序取第一个放得下的单元。
	visited := map[string]bool{}
	for _, e := range sorted {
		if e == nil || visited[e.key] {
			continue
		}
		visited[e.key] = true
		placed := false
		for _, t := range sorted {
			if t.key != e.key || t.dead || t.admitted {
				continue
			}
			if t.tokens <= remaining {
				admitPackEntry(t, sorted)
				remaining -= t.tokens
				placed = true
				break
			}
		}
		if !placed {
			slog.Warn("pack source dropped: no unit fits the reply budget",
				slog.String("source_id", e.unit.SourceID))
		}
	}
	// 深度段：剩余单元按全序逐个尝试，放不下跳过继续；保底直回单元不参
	// 与深度段（来源已有入包单元时它只会重复内容，来源被丢弃时预算必然已
	// 容不下它）。
	for _, t := range sorted {
		if t == nil || t.dead || t.admitted || t.fallbackOnly {
			continue
		}
		if t.tokens <= remaining {
			admitPackEntry(t, sorted)
			remaining -= t.tokens
		}
	}
	out := make([]*KnowledgeSearchUnit, 0, len(sorted))
	for _, e := range sorted {
		if e != nil && e.admitted {
			out = append(out, e.unit)
		}
	}
	return out
}

// admitPackEntry 收一个单元入包并执行同源收编：document 单元使同源其余单
// 元降为冗余，非 document 单元入包使同源未入包的 document 单元降为冗余。
func admitPackEntry(t *packEntry, all []*packEntry) {
	t.admitted = true
	for _, o := range all {
		if o == nil || o == t || o.key != t.key || o.admitted {
			continue
		}
		if t.unit.Mode == KnowledgeSearchUnitModeDocument || o.unit.Mode == KnowledgeSearchUnitModeDocument {
			o.dead = true
		}
	}
}

// estimatePackTokens 是回包 token 量级的确定性估算（不引入分词器依赖）：
// CJK 汉字/假名/谚文每个 rune 计 1，其余 rune 每 4 个计 1（向上取整）。
// 只求量级用于预算封顶，不追求与任何分词器对齐。
func estimatePackTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isPackCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// isPackCJK 判定 CJK 汉字/假名/谚文 rune（估算口径的计 1 集合；全角标点
// 等符号不在内，按其余 rune 计）。
func isPackCJK(r rune) bool {
	return (r >= 0x3400 && r <= 0x4DBF) || // 汉字扩展 A
		(r >= 0x4E00 && r <= 0x9FFF) || // 汉字统一表意文字
		(r >= 0xF900 && r <= 0xFAFF) || // 兼容表意文字
		(r >= 0x20000 && r <= 0x2FA1F) || // 扩展 B 及以后
		(r >= 0x3040 && r <= 0x30FF) || // 平假名与片假名
		(r >= 0x31F0 && r <= 0x31FF) || // 片假名注音扩展
		(r >= 0x1100 && r <= 0x11FF) || // 谚文字母
		(r >= 0x3130 && r <= 0x318F) || // 谚文兼容字母
		(r >= 0xAC00 && r <= 0xD7AF) // 谚文音节
}

// packChunkFromHit 由命中切片装配聚合成员（命中引用随行，供 unit.Hits 复
// 用）。
func packChunkFromHit(h *KnowledgeSearchHit) *packChunk {
	return &packChunk{
		id:         h.DocumentID,
		index:      h.ChunkIndex,
		heading:    h.HeadingPath,
		anchor:     h.Anchor,
		sourceID:   h.SourceID,
		sourceName: h.SourceName,
		content:    h.Content,
		score:      h.Score,
		hit:        true,
		ref:        h,
	}
}

// packChunkFromDocument 由来源全量块装配聚合成员：metadata 口径与摄取侧
// chunkMetadata 一致，数字按 jsonb 反序列化形态断言。
func packChunkFromDocument(d *VectorDocument) *packChunk {
	return &packChunk{
		id:         d.ID,
		index:      packChunkIndex(d.Metadata),
		heading:    metadataText(d.Metadata, "heading_path"),
		anchor:     metadataText(d.Metadata, "anchor"),
		sourceID:   metadataText(d.Metadata, "source_id"),
		sourceName: metadataText(d.Metadata, "source_name"),
		content:    d.Content,
	}
}

// metadataChunkIndex 安全读取切片 metadata 的 chunk_index：jsonb 反序列化
// 形态是 float64（摄取写入是 int，内存替身可能直接写整型），缺失或类型不
// 符返回 false——调用方以 -1 表达「索引不可定位」。
func metadataChunkIndex(metadata map[string]any) (int, bool) {
	switch v := metadata["chunk_index"].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

// packChunkIndex 读取命中侧的 chunk_index：缺失按 -1（索引不可定位）。
func packChunkIndex(metadata map[string]any) int {
	index, ok := metadataChunkIndex(metadata)
	if !ok {
		return -1
	}
	return index
}
