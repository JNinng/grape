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
	"grape/pkg/redact"

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

// KnowledgeSearchHit 是单条命中切片：作为聚合成员随 KnowledgeSearchUnit
// 返回（console 展平各单元的 Hits，与聚合前命中形状一致）。DocumentID 是
// 切片 ID，SourceID 是来源条目 ID；Source*/Anchor 取自切片 metadata，缺失
// 为空。ChunkIndex/HeadingPath 是回包聚合（pack）的分档依据。Score 装配重排
// 层且本次成功时为 rerank 相关性分，否则为 RRF 融合分——仅同次检索内可比、
// 不代表相似度；候选不足 2 条跳过重排，Score 保持融合分。Content 为切片原文
//（不做围栏包装——围栏是模型面语义）。
type KnowledgeSearchHit struct {
	DocumentID  string
	SourceID    string
	SourceName  string
	Anchor      string
	ChunkIndex  int
	HeadingPath string
	Score       float64
	Content     string
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
// 库集合并注入 kb_id IN 条件。混合检索编排路径自写一行完整留痕（经
// WithRetrievalLogSuppressed 让 data 层装饰器跳过逐路留痕，stages 承载
// 阶段数组），查询文本与来源经 ctx 事实载体声明（缺省 console，工具链路
// 可先声明 tool）。reranker 是可选的重排层挂点（未配置 ai.rerank.model
// 的部署可不装配）。
type KnowledgeSearchUsecase struct {
	store     VectorStore
	embedder  Embedder
	resolver  EmbedderResolver
	reranker  Reranker
	libraries KnowledgeLibraryDirectory
	logs      RetrievalLogRepo
	registry  *redact.Registry
	now       func() time.Time
}

// NewKnowledgeSearchUsecase 构造检索用例（与 DocumentUsecase 同形依赖，另加
// 可见库解析接缝与留痕写入面）。reranker 允许 nil：nil 即跳过重排层，行为与
// 无重排一致；非 nil 时可用性由重排模型解析决定（未配置按跳过处理，见
// Reranker 契约）。logs 必填：编排路径留痕行由本用例自写（脱敏义务随写入方），
// registry 为共用脱敏注册表（nil 按「放行原值 + 截断兜底」降级）。
func NewKnowledgeSearchUsecase(store VectorStore, embedder Embedder, resolver EmbedderResolver, reranker Reranker, libraries KnowledgeLibraryDirectory, logs RetrievalLogRepo, registry *redact.Registry) (*KnowledgeSearchUsecase, error) {
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
	if logs == nil {
		return nil, errors.New("biz: knowledge search usecase requires a retrieval log repo")
	}
	return &KnowledgeSearchUsecase{store: store, embedder: embedder, resolver: resolver, reranker: reranker, libraries: libraries, logs: logs, registry: registry, now: time.Now}, nil
}

// Search 在默认嵌入空间内检索调用方可见的知识库：可见库集合每次检索现场
// 解析（权限运行时可变，不做长生命周期缓存；身份缺失 fail-closed）、固定
// 检索条件、双路召回 → RRF 融合 → 可选重排 → 取 top_k → 回包聚合（pack）。
// 正确性依赖失败让整次检索失败；词法路、重排层与聚合的来源全量块拉取失败
// 按质量增强降级，降级矩阵与留痕口径见 knowledge_search.md；设施未启用原样
// 透传。编排路径自写一行完整留痕（WithRetrievalLogSuppressed 抑制逐路行，
// stages 承载阶段数组）。
func (uc *KnowledgeSearchUsecase) Search(ctx context.Context, q *KnowledgeSearchQuery) ([]*KnowledgeSearchUnit, error) {
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
		// 无可见库：检索结果必然为空，不触达嵌入与向量设施（也未进入
		// 召回管线，不产生留痕行）。
		return []*KnowledgeSearchUnit{}, nil
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
	// 编排路径的留痕由本用例负责：打上抑制标记让装饰器跳过逐路留痕，
	// 多路调用共用该 ctx。
	ctx = WithRetrievalLogSuppressed(ctx)
	conditions := knowledgeRetrievalConditions(libraryIDs, uc.now())

	tracker := &retrievalStageTracker{}
	start := uc.now()
	run := &knowledgeSearchRun{
		query:      query,
		variants:   variants,
		topK:       topK,
		space:      space,
		dimension:  dimension,
		conditions: conditions,
		embedCtx:   embedCtx,
	}
	units, hits, runErr := uc.runSearch(ctx, tracker, run)

	// 完成处一次写完整行：status 记操作终态，stages 承载阶段时间线，
	// hits 仍是最终命中；留痕写入失败不反噬检索（旁路观察面）。
	uc.appendSearchLog(ctx, uc.buildSearchLogRecord(ctx, tracker, run, libraryIDs, hits, runErr, uc.now().Sub(start)))

	if runErr != nil {
		return nil, runErr
	}
	return units, nil
}

// knowledgeSearchRun 是一次检索编排的入参事实：归一后的主查询与变体、
// 生效 top_k、嵌入空间与固定检索条件，以及嵌入调用的场景 ctx。
type knowledgeSearchRun struct {
	query      string
	variants   []string
	topK       int
	space      string
	dimension  int
	conditions []MetadataCondition
	embedCtx   context.Context
}

// runSearch 执行召回 → 融合 → 重排 → 回包聚合管线并沿途记录阶段留痕：
// 返回回包单元、最终命中（top_k 截断后）与操作错误。每个阶段在其执行点
// 记录输入 query 变体、limit、候选（截 50）、错误与耗时。
func (uc *KnowledgeSearchUsecase) runSearch(ctx context.Context, tracker *retrievalStageTracker, run *knowledgeSearchRun) ([]*KnowledgeSearchUnit, []*KnowledgeSearchHit, error) {
	// 召回集 = 主查询 + 变体：每条独立向量化走向量路，同一条文本走词法
	// 路；每路 Limit = 2×top_k（融合只看排名，取宽一点保住边界召回）。
	queries := append([]string{run.query}, run.variants...)
	routes := make([][]*ScoredVectorDocument, 0, len(queries)*2)
	for _, text := range queries {
		stageQuery := uc.redactRetrievalText(text)
		stageStart := uc.now()
		vector, err := uc.embedder.Embed(run.embedCtx, run.space, text)
		if err != nil {
			// 查询向量化失败让整次检索失败（正确性依赖）：该召回阶段记
			// 错误与空候选，失败行照常留痕。
			tracker.add(retrievalStage{
				Stage:      RetrievalStageVector,
				QueryText:  stageQuery,
				Error:      uc.redactRetrievalText("embed query failed: " + err.Error()),
				DurationMS: uc.now().Sub(stageStart).Milliseconds(),
			})
			return nil, nil, fmt.Errorf("biz: embed search query: %w", err)
		}
		vectorHits, err := uc.store.Search(ctx, vector, &VectorFilter{
			Embedder:   run.space,
			Dimension:  run.dimension,
			Conditions: run.conditions,
			Limit:      run.topK * KnowledgeSearchRecallFactor,
		})
		if err != nil {
			// 向量路失败让整次检索失败（正确性依赖），阶段照常留痕。
			tracker.add(retrievalStage{
				Stage:      RetrievalStageVector,
				QueryText:  stageQuery,
				Limit:      run.topK * KnowledgeSearchRecallFactor,
				Error:      uc.redactRetrievalText("vector search failed: " + err.Error()),
				DurationMS: uc.now().Sub(stageStart).Milliseconds(),
			})
			return nil, nil, fmt.Errorf("biz: search knowledge: %w", err)
		}
		tracker.add(retrievalStage{
			Stage:      RetrievalStageVector,
			QueryText:  stageQuery,
			Limit:      run.topK * KnowledgeSearchRecallFactor,
			Candidates: retrievalStageCandidates(vectorHits),
			DurationMS: uc.now().Sub(stageStart).Milliseconds(),
		})
		routes = append(routes, vectorHits)

		lexicalStart := uc.now()
		lexicalHits, err := uc.store.SearchLexical(ctx, text, &VectorFilter{
			Embedder:   run.space,
			Dimension:  run.dimension,
			Conditions: run.conditions,
			Limit:      run.topK * KnowledgeSearchRecallFactor,
		})
		if err != nil {
			// 词法路失败按质量增强降级：记服务日志与阶段失败留痕后仅用
			// 向量路结果，不让整次检索失败（检索可用性优先于召回质量）。
			slog.WarnContext(ctx, "lexical search failed; falling back to vector-only recall", slog.Any("error", err))
			tracker.add(retrievalStage{
				Stage:      RetrievalStageLexical,
				QueryText:  stageQuery,
				Limit:      run.topK * KnowledgeSearchRecallFactor,
				Error:      uc.redactRetrievalText("lexical search failed: " + err.Error()),
				DurationMS: uc.now().Sub(lexicalStart).Milliseconds(),
			})
			continue
		}
		tracker.add(retrievalStage{
			Stage:      RetrievalStageLexical,
			QueryText:  stageQuery,
			Limit:      run.topK * KnowledgeSearchRecallFactor,
			Candidates: retrievalStageCandidates(lexicalHits),
			DurationMS: uc.now().Sub(lexicalStart).Milliseconds(),
		})
		routes = append(routes, lexicalHits)
	}

	// 融合序：全量融合后取前 2×top_k 作为重排候选（融合只看排名，取宽
	// 一点给重排层留足边界召回）；重排层之后最终取 top_k。
	fusionStart := uc.now()
	fused := reciprocalRankFusion(routes, DefaultKnowledgeSearchFusionRRFK, run.topK*KnowledgeSearchRecallFactor)
	tracker.add(retrievalStage{
		Stage:      RetrievalStageFusion,
		Limit:      run.topK * KnowledgeSearchRecallFactor,
		Candidates: retrievalStageCandidates(fused),
		DurationMS: uc.now().Sub(fusionStart).Milliseconds(),
	})
	fused = uc.applyRerank(ctx, tracker, run.query, run.topK, fused)
	if len(fused) > run.topK {
		fused = fused[:run.topK]
	}

	hits := make([]*KnowledgeSearchHit, 0, len(fused))
	for _, r := range fused {
		if r == nil || r.Document == nil {
			continue
		}
		metadata := r.Document.Metadata
		hits = append(hits, &KnowledgeSearchHit{
			DocumentID:  r.Document.ID,
			SourceID:    metadataText(metadata, "source_id"),
			SourceName:  metadataText(metadata, "source_name"),
			Anchor:      metadataText(metadata, "anchor"),
			ChunkIndex:  packChunkIndex(metadata),
			HeadingPath: metadataText(metadata, "heading_path"),
			Score:       r.Score,
			Content:     r.Document.Content,
		})
	}
	// 回包聚合（pack）：命中块按来源自动分档聚合并以固定预算封顶，返回
	// 粒度从逐切片命中变为逐回包单元（见 KnowledgeSearchUnit 契约注释）；
	// 全部装配单元（含被预算截断者）进 pack 阶段留痕。
	packStart := uc.now()
	units, packUnits := uc.packSearchUnits(ctx, hits, run.space, run.conditions)
	tracker.add(retrievalStage{
		Stage:      RetrievalStagePack,
		Limit:      KnowledgeSearchPackBudgetTokens,
		Units:      packUnits,
		DurationMS: uc.now().Sub(packStart).Milliseconds(),
	})
	return units, hits, nil
}

// buildSearchLogRecord 组装编排路径的完整留痕行：身份/租户不进行（仓储
// 从认证 ctx 提取经 IdentityTx 注入），trace/session/来源取 ctx 事实，
// 查询文本与阶段内文本已按 retrieval 类别脱敏。requested/effective top_k
// 均取归一化后的 top_k（最终命中上限）；过滤摘要记可见库集合；hits 是
// 最终命中；status 按操作终态取值。
func (uc *KnowledgeSearchUsecase) buildSearchLogRecord(ctx context.Context, tracker *retrievalStageTracker, run *knowledgeSearchRun, libraryIDs []int64, hits []*KnowledgeSearchHit, opErr error, elapsed time.Duration) *RetrievalLogRecord {
	loggedHits := make([]RetrievalHit, 0, len(hits))
	for _, h := range hits {
		if h == nil {
			continue
		}
		loggedHits = append(loggedHits, RetrievalHit{
			DocumentID: h.DocumentID,
			Score:      h.Score,
			Anchor:     h.Anchor,
		})
	}
	rec := &RetrievalLogRecord{
		TraceID:       TraceIDFromContext(ctx),
		SessionID:     SessionIDFromContext(ctx),
		Source:        RetrievalSourceFromContext(ctx),
		Embedder:      run.space,
		QueryText:     uc.redactRetrievalText(run.query),
		RequestedTopK: run.topK,
		EffectiveTopK: run.topK,
		FilterSummary: map[string]any{"kb_ids": libraryIDs},
		Hits:          loggedHits,
		Stages:        tracker.payload(),
		DurationMS:    elapsed.Milliseconds(),
		Status:        RetrievalStatusSuccess,
	}
	if opErr != nil {
		rec.Status = RetrievalStatusError
	}
	return rec
}

// applyRerank 是检索管线的重排层：对融合序候选（前 2×top_k）按切片内容打
// 相关性分并降序稳定重排（同分保持融合序），Score 改写为重排分（无重排保持
// RRF 分，仅同次检索内各自可比）。重排是质量增强非正确性依赖：未装配、候选
// 不足 2 条、模型未配置（ErrRerankUnavailable）时层未运行、不记 rerank 阶段；
// 调用失败与分数长度不符降级为融合序继续检索，rerank 阶段照常留痕（候选
// 保持融合序 + 错误摘要，与 fusion 阶段候选对照即还原重排前后序）。
func (uc *KnowledgeSearchUsecase) applyRerank(ctx context.Context, tracker *retrievalStageTracker, query string, topK int, fused []*ScoredVectorDocument) []*ScoredVectorDocument {
	if uc.reranker == nil || len(fused) < 2 {
		return fused
	}
	documents := make([]string, 0, len(fused))
	for _, r := range fused {
		documents = append(documents, r.Document.Content)
	}
	// 阶段留痕口径：rerank 的 limit 记输出上限（最终 top_k），candidates
	// 记重排输入候选的重排后序（条数为 2×top_k 的召回窗口，不因输出上限
	// 截断）——见 retrievalStage.Limit 注释与 schema/97 列注释。
	stageStart := uc.now()
	// 模型调用留痕场景在此声明（rerank）：重排无 kb_document 行上下文，
	// 文档 ID 恒缺省。
	res, err := uc.reranker.Rerank(WithModelCallScene(ctx, ModelCallScenarioRerank), query, documents)
	if err != nil {
		if errors.Is(err, ErrRerankUnavailable) {
			return fused
		}
		slog.WarnContext(ctx, "rerank failed; falling back to fusion order", slog.Any("error", err))
		tracker.add(retrievalStage{
			Stage:      RetrievalStageRerank,
			Limit:      topK,
			Model:      rerankModelKey(res),
			Candidates: retrievalStageCandidates(fused),
			Error:      uc.redactRetrievalText("rerank failed: " + err.Error()),
			DurationMS: uc.now().Sub(stageStart).Milliseconds(),
		})
		return fused
	}
	if len(res.Scores) != len(fused) {
		// 契约要求等长返回：长度不符属实现缺陷，按层失败降级而非错序。
		slog.WarnContext(ctx, "rerank returned mismatched scores; falling back to fusion order",
			slog.Int("documents", len(fused)), slog.Int("scores", len(res.Scores)))
		tracker.add(retrievalStage{
			Stage:      RetrievalStageRerank,
			Limit:      topK,
			Model:      res.ModelKey,
			Candidates: retrievalStageCandidates(fused),
			Error:      uc.redactRetrievalText("rerank returned mismatched scores"),
			DurationMS: uc.now().Sub(stageStart).Milliseconds(),
		})
		return fused
	}
	for i, s := range res.Scores {
		fused[i].Score = s
	}
	sort.SliceStable(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	tracker.add(retrievalStage{
		Stage:      RetrievalStageRerank,
		Limit:      topK,
		Model:      res.ModelKey,
		Candidates: retrievalStageCandidates(fused),
		DurationMS: uc.now().Sub(stageStart).Milliseconds(),
	})
	return fused
}

// rerankModelKey 安全读取重排结果中的模型键：调用失败且无结果事实时为空
// （留痕缺失如实留空，不伪造）。
func rerankModelKey(res *RerankResult) string {
	if res == nil {
		return ""
	}
	return res.ModelKey
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
// 恒以 kb_id IN 收窄到可见库集合（调用方保证非空）；重摄淘汰的历史行
//（superseded）由 is-null 条件排除——检索只召回现行切片。时间为秒级
// unix，与摄取侧 effectiveChunkMetadata 的写入口径一致。
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
		{Key: VectorMetadataSupersededAt, Op: VectorMetadataIsNull},
	}
}

// metadataText 安全读取切片 metadata 的字符串键：缺失或类型不符返回空串
// （metadata 来自摄取侧写入，形状异常不致命）。
func metadataText(metadata map[string]any, key string) string {
	s, _ := metadata[key].(string)
	return s
}
