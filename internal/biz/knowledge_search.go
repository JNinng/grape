package biz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	knowledgev1 "grape/api/knowledge/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// 知识检索的错误语义：入参非法为 400；嵌入/向量设施未启用为能力缺失
// （503）；其余执行故障对外只给固定文案（500），细节留在服务端日志。
// 后两者由入口层按 ErrEmbedderDisabled / ErrVectorStoreDisabled 判定映射，
// 用例本身原样透传设施错误。
var (
	ErrKnowledgeSearchInvalidArgument = kratoserrors.BadRequest(knowledgev1.ErrorReason_SEARCH_INVALID_ARGUMENT.String(), "invalid knowledge search argument")
	ErrKnowledgeSearchUnavailable     = kratoserrors.ServiceUnavailable(knowledgev1.ErrorReason_SEARCH_UNAVAILABLE.String(), "knowledge search is unavailable")
	ErrKnowledgeSearchFailed          = kratoserrors.InternalServer(knowledgev1.ErrorReason_SEARCH_FAILED.String(), "knowledge search failed")
)

// 检索入参边界：查询限短（embedding 输入无必要过长）；top_k 缺省 5，
// 服务端统一上限 20（agent 检索与前端检索都经本用例，上限在此钳制；
// 框架适配器的 maxSearchTopK 是同值兜底，只约束框架侧直接消费方）。
// 检索质量参数属代码级默认，不进 conf。查询变体（multi-query 的 biz
// 入口）逐条受同一长度上限，条数上限 4：召回集随变体数线性膨胀，改写
// 质量靠 agent 侧 LLM 决策，用例只防滥用。
const (
	DefaultKnowledgeSearchTopK       = 5
	MaxKnowledgeSearchTopK           = 20
	MaxKnowledgeSearchQueryRunes     = 512
	MaxKnowledgeSearchQueryVariants  = 4
	DefaultKnowledgeSearchFusionRRFK = 60
	KnowledgeSearchRecallFactor      = 2
)

// KnowledgeSearchQuery 是检索入参。LibraryID 为 0 时检索调用方可见的全部
// 知识库，大于 0 时收窄到该库（必须命中可见库集合，否则按
// ErrLibraryNotFound 拒绝——不可见与不存在不区分），负值非法；TopK 为 0
// 取缺省，负值非法，超上限钳制。Variants 是可选的查询变体列表
// （multi-query：改写由 agent 侧 LLM 决策，本用例只提供召回入口）：逐条
// trim 后空白条目跳过，超长或条数超限非法；主查询与变体共同构成召回集，
// 各自独立召回后按 RRF 融合。
type KnowledgeSearchQuery struct {
	Query     string
	Variants  []string
	LibraryID int64
	TopK      int
}

// KnowledgeSearchHit 是单条命中：DocumentID 是切片 ID（与留痕命中对账），
// SourceID 是来源条目 ID（可反查条目原文）；SourceID/SourceName/Anchor
// 取自切片 metadata，缺失为空。Score 是 RRF 融合分（Σ 1/(k+rank)，仅
// 同次检索内可比，不代表相似度），Content 为切片原文（不做围栏包装——
// 围栏是模型面语义）。
type KnowledgeSearchHit struct {
	DocumentID string
	SourceID   string
	SourceName string
	Anchor     string
	Score      float64
	Content    string
}

// KnowledgeLibraryDirectory 是可见库集合的解析接缝：*KnowledgeUsecase
// 实现（存储层按调用方身份求交 fn_accessible_* 三件套）。检索用例与 agent
// 列库工具共用同一实现，避免「列得出、查不到」的口径漂移。
type KnowledgeLibraryDirectory interface {
	// VisibleLibraries 返回调用方数据范围内可见的未软删知识库。
	VisibleLibraries(ctx context.Context, auth Identity) ([]*KnowledgeLibrary, error)
}

// KnowledgeSearchUsecase 是租户内知识检索用例：入参校验、可见库集合现场
// 解析、默认空间解析、查询向量化、固定检索条件组装后直调 VectorStore
// 契约。租户隔离由契约实现从认证 ctx 强制；库级可见性由本用例解析可见
// 库集合并注入 kb_id IN 条件。留痕由 data 层装饰器完成，本用例只经 ctx
// 声明查询文本与来源（缺省 console，工具链路可先声明 tool）。
type KnowledgeSearchUsecase struct {
	store     VectorStore
	embedder  Embedder
	resolver  EmbedderResolver
	libraries KnowledgeLibraryDirectory
	now       func() time.Time
}

// NewKnowledgeSearchUsecase 构造检索用例（与 DocumentUsecase 同形依赖，
// 另加可见库解析接缝）。
func NewKnowledgeSearchUsecase(store VectorStore, embedder Embedder, resolver EmbedderResolver, libraries KnowledgeLibraryDirectory) (*KnowledgeSearchUsecase, error) {
	if store == nil {
		return nil, errors.New("biz: knowledge search usecase requires a vector store")
	}
	if embedder == nil {
		return nil, errors.New("biz: knowledge search usecase requires an embedder")
	}
	if resolver == nil {
		return nil, errors.New("biz: knowledge search usecase requires an embedder resolver")
	}
	if libraries == nil {
		return nil, errors.New("biz: knowledge search usecase requires a library directory")
	}
	return &KnowledgeSearchUsecase{store: store, embedder: embedder, resolver: resolver, libraries: libraries, now: time.Now}, nil
}

// Search 在默认嵌入空间内检索调用方可见的知识库：主查询与可选变体各自
// 独立召回（向量路与词法路同走，两路各取 2×top_k），按 Reciprocal Rank
// Fusion 融合后取 top_k 返回；无可见库或无命中返回空切片。词法路与向量
// 路共享同一 conditions 口径与文档宇宙（同租户同空间）；融合只在用例层，
// 存储两路互不感知。可见库集合每次检索现场解析（权限运行时可变，不做
// 长生命周期缓存）；身份缺失 fail-closed。错误语义：可见库解析、嵌入、
// 向量路失败让整次检索失败（正确性依赖）；词法路失败按质量增强降级为
// 仅向量路，记服务日志不失败。设施未启用的错误原样透传（不降级为空
// 结果）。
func (uc *KnowledgeSearchUsecase) Search(ctx context.Context, q *KnowledgeSearchQuery) ([]*KnowledgeSearchHit, error) {
	auth, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, ErrSessionInvalid
	}
	query, variants, topK, err := normalizeKnowledgeSearch(q)
	if err != nil {
		return nil, err
	}
	libraryIDs, err := uc.searchLibraryIDs(ctx, auth, q.LibraryID)
	if err != nil {
		return nil, err
	}
	if len(libraryIDs) == 0 {
		// 无可见库：检索结果必然为空，不触达嵌入与向量设施。
		return []*KnowledgeSearchHit{}, nil
	}
	space, dimension, err := uc.resolver.ResolveDefaultEmbedder(ctx)
	if err != nil {
		return nil, fmt.Errorf("biz: resolve default embedder for search: %w", err)
	}
	// 模型调用留痕场景在此声明（embed_query）：检索查询向量化无文档
	// 事实，document_id 恒 NULL。
	embedCtx := WithModelCallScene(ctx, ModelCallScenarioEmbedQuery)
	ctx = WithRetrievalQuery(ctx, query)
	if RetrievalSourceFromContext(ctx) == "" {
		ctx = WithRetrievalSource(ctx, RetrievalSourceConsole)
	}
	conditions := knowledgeRetrievalConditions(libraryIDs, uc.now())

	// 召回集 = 主查询 + 变体：每条独立向量化走向量路，同一条文本走词法
	// 路；每路 Limit = 2×top_k（融合只看排名，取宽一点保住边界召回）。
	queries := append([]string{query}, variants...)
	routes := make([][]*ScoredVectorDocument, 0, len(queries)*2)
	for _, text := range queries {
		vector, err := uc.embedder.Embed(embedCtx, space, text)
		if err != nil {
			return nil, fmt.Errorf("biz: embed search query: %w", err)
		}
		vectorHits, err := uc.store.Search(ctx, vector, &VectorFilter{
			Embedder:   space,
			Dimension:  dimension,
			Conditions: conditions,
			Limit:      topK * KnowledgeSearchRecallFactor,
		})
		if err != nil {
			return nil, fmt.Errorf("biz: search knowledge: %w", err)
		}
		routes = append(routes, vectorHits)
		lexicalHits, err := uc.store.SearchLexical(ctx, text, &VectorFilter{
			Embedder:   space,
			Dimension:  dimension,
			Conditions: conditions,
			Limit:      topK * KnowledgeSearchRecallFactor,
		})
		if err != nil {
			// 词法路失败按质量增强降级：记服务日志后仅用向量路结果，
			// 不让整次检索失败（检索可用性优先于召回质量）。
			slog.WarnContext(ctx, "lexical search failed; falling back to vector-only recall", slog.Any("error", err))
			continue
		}
		routes = append(routes, lexicalHits)
	}

	results := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, topK)
	hits := make([]*KnowledgeSearchHit, 0, len(results))
	for _, r := range results {
		if r == nil || r.Document == nil {
			continue
		}
		hits = append(hits, &KnowledgeSearchHit{
			DocumentID: r.Document.ID,
			SourceID:   metadataText(r.Document.Metadata, "source_id"),
			SourceName: metadataText(r.Document.Metadata, "source_name"),
			Anchor:     metadataText(r.Document.Metadata, "anchor"),
			Score:      r.Score,
			Content:    r.Document.Content,
		})
	}
	return hits, nil
}

// reciprocalRankFusion 是 RRF 融合的纯函数：按各路排名累加 1/(k+rank)
// （rank 从 1 起），同一文档跨路累加，文档记录取首次出现者，得分降序、
// 同分按首次出现序（确定性）取前 topK 条。输入各路必须是已按名次排好的
// 命中序列（存储层两路的排序契约）；nil 命中跳过不计名次。
func reciprocalRankFusion(routes [][]*ScoredVectorDocument, k float64, topK int) []*ScoredVectorDocument {
	type fusionEntry struct {
		doc   *VectorDocument
		score float64
		order int // 首次出现序（同分 tie-break）
	}
	merged := map[string]*fusionEntry{}
	order := 0
	for _, route := range routes {
		rank := 0
		for _, hit := range route {
			if hit == nil || hit.Document == nil || hit.Document.ID == "" {
				continue
			}
			rank++
			entry, ok := merged[hit.Document.ID]
			if !ok {
				entry = &fusionEntry{doc: hit.Document, order: order}
				merged[hit.Document.ID] = entry
				order++
			}
			entry.score += 1 / (k + float64(rank))
		}
	}
	out := make([]*ScoredVectorDocument, 0, len(merged))
	for _, entry := range merged {
		out = append(out, &ScoredVectorDocument{Document: entry.doc, Score: entry.score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return merged[out[i].Document.ID].order < merged[out[j].Document.ID].order
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out
}

// VisibleLibraries 返回调用方可见的知识库（agent 列库工具与检索过滤共用
// 同一解析实现）；身份缺失 fail-closed。
func (uc *KnowledgeSearchUsecase) VisibleLibraries(ctx context.Context) ([]*KnowledgeLibrary, error) {
	auth, ok := IdentityFromContext(ctx)
	if !ok {
		return nil, ErrSessionInvalid
	}
	libs, err := uc.libraries.VisibleLibraries(ctx, auth)
	if err != nil {
		return nil, fmt.Errorf("biz: resolve visible libraries: %w", err)
	}
	return libs, nil
}

// searchLibraryIDs 解析本次检索的库范围：libraryID 为 0 取全部可见库；
// 大于 0 时必须命中可见集合，不可见与不存在统一 ErrLibraryNotFound
// （不区分存在性、不泄露有无）。返回切片恒非 nil，空即无可见库。
func (uc *KnowledgeSearchUsecase) searchLibraryIDs(ctx context.Context, auth Identity, libraryID int64) ([]int64, error) {
	visible, err := uc.libraries.VisibleLibraries(ctx, auth)
	if err != nil {
		return nil, fmt.Errorf("biz: resolve visible libraries for search: %w", err)
	}
	if libraryID > 0 {
		for _, lib := range visible {
			if lib != nil && lib.ID == libraryID {
				return []int64{libraryID}, nil
			}
		}
		return nil, ErrLibraryNotFound
	}
	ids := make([]int64, 0, len(visible))
	for _, lib := range visible {
		if lib != nil {
			ids = append(ids, lib.ID)
		}
	}
	return ids, nil
}

// normalizeKnowledgeSearch 校验并归一入参，返回去空白后的查询文本、归一
// 后的变体列表（trim 去空白条目；超长或条数超限非法）与生效 top_k（缺省
// 落 5、超限钳制到 20，保证 Limit 恒为正——Limit 为 0 时存储实现会回落
// 默认页大小）。
func normalizeKnowledgeSearch(q *KnowledgeSearchQuery) (string, []string, int, error) {
	if q == nil {
		return "", nil, 0, ErrKnowledgeSearchInvalidArgument
	}
	query := strings.TrimSpace(q.Query)
	if query == "" || utf8.RuneCountInString(query) > MaxKnowledgeSearchQueryRunes {
		return "", nil, 0, ErrKnowledgeSearchInvalidArgument
	}
	if q.LibraryID < 0 || q.TopK < 0 {
		return "", nil, 0, ErrKnowledgeSearchInvalidArgument
	}
	variants := make([]string, 0, len(q.Variants))
	for _, v := range q.Variants {
		trimmed := strings.TrimSpace(v)
		// 空白条目跳过（不占变体名额）；trim 后超长非法（变体是查询，
		// 与主查询同一长度上限）。
		if trimmed == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed) > MaxKnowledgeSearchQueryRunes {
			return "", nil, 0, ErrKnowledgeSearchInvalidArgument
		}
		variants = append(variants, trimmed)
	}
	if len(variants) > MaxKnowledgeSearchQueryVariants {
		return "", nil, 0, ErrKnowledgeSearchInvalidArgument
	}
	topK := q.TopK
	if topK == 0 {
		topK = DefaultKnowledgeSearchTopK
	}
	if topK > MaxKnowledgeSearchTopK {
		topK = MaxKnowledgeSearchTopK
	}
	return query, variants, topK, nil
}

// knowledgeRetrievalConditions 组装检索固定条件：条目参与（库与条目状态
// 取与后写入 chunk metadata）且 now 落在有效期 [from, to) 内；库范围
// 恒以 kb_id IN 收窄到可见库集合（调用方保证非空）。时间为秒级 unix，
// 与摄取侧 effectiveChunkMetadata 的写入口径一致。
func knowledgeRetrievalConditions(libraryIDs []int64, now time.Time) []MetadataCondition {
	unix := now.Unix()
	ids := make([]any, 0, len(libraryIDs))
	for _, id := range libraryIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return []MetadataCondition{
		{Key: "doc_status", Op: VectorMetadataEq, Values: []any{int(KnowledgeStatusEnabled)}},
		{Key: "valid_from_unix", Op: VectorMetadataLte, Values: []any{unix}},
		{Key: "valid_to_unix", Op: VectorMetadataGt, Values: []any{unix}},
		{Key: "kb_id", Op: VectorMetadataIn, Values: ids},
	}
}

// metadataText 安全读取切片 metadata 的字符串键：缺失或类型不符返回空串
// （metadata 来自摄取侧写入，形状异常不致命）。
func metadataText(metadata map[string]any, key string) string {
	s, _ := metadata[key].(string)
	return s
}
