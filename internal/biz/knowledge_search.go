package biz

import (
	"context"
	"errors"
	"fmt"
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
// 服务端统一上限 20（与 agent 向量适配器的 maxSearchTopK 同值，适配器
// 钳制保留为模型面第二道防线）。检索质量参数属代码级默认，不进 conf。
const (
	DefaultKnowledgeSearchTopK   = 5
	MaxKnowledgeSearchTopK       = 20
	MaxKnowledgeSearchQueryRunes = 512
)

// KnowledgeSearchQuery 是检索入参。LibraryID 大于 0 时收窄到该库，0 检索
// 租户内全部知识库，负值非法；TopK 为 0 取缺省，负值非法，超上限钳制。
type KnowledgeSearchQuery struct {
	Query     string
	LibraryID int64
	TopK      int
}

// KnowledgeSearchHit 是单条命中：DocumentID 是切片 ID（与留痕命中对账），
// SourceID 是来源条目 ID（可反查条目原文）；SourceID/SourceName/Anchor
// 取自切片 metadata，缺失为空。Score 为契约归一化相似度，Content 为
// 切片原文（不做围栏包装——围栏是模型面语义）。
type KnowledgeSearchHit struct {
	DocumentID string
	SourceID   string
	SourceName string
	Anchor     string
	Score      float64
	Content    string
}

// KnowledgeSearchUsecase 是租户内知识检索用例：入参校验、默认空间解析、
// 查询向量化、固定检索条件组装后直调 VectorStore 契约。租户隔离由契约
// 实现从认证 ctx 强制；留痕由 data 层装饰器完成，本用例只经 ctx 声明
// 查询文本与来源（console）。
type KnowledgeSearchUsecase struct {
	store    VectorStore
	embedder Embedder
	resolver EmbedderResolver
	now      func() time.Time
}

// NewKnowledgeSearchUsecase 构造检索用例（与 DocumentUsecase 同形依赖）。
func NewKnowledgeSearchUsecase(store VectorStore, embedder Embedder, resolver EmbedderResolver) (*KnowledgeSearchUsecase, error) {
	if store == nil {
		return nil, errors.New("biz: knowledge search usecase requires a vector store")
	}
	if embedder == nil {
		return nil, errors.New("biz: knowledge search usecase requires an embedder")
	}
	if resolver == nil {
		return nil, errors.New("biz: knowledge search usecase requires an embedder resolver")
	}
	return &KnowledgeSearchUsecase{store: store, embedder: embedder, resolver: resolver, now: time.Now}, nil
}

// Search 在默认嵌入空间内检索，按相似度降序返回至多 top_k 条命中；无命中
// 返回空切片。身份缺失 fail-closed；设施未启用的错误原样透传（不降级为
// 空结果）。
func (uc *KnowledgeSearchUsecase) Search(ctx context.Context, q *KnowledgeSearchQuery) ([]*KnowledgeSearchHit, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	query, topK, err := normalizeKnowledgeSearch(q)
	if err != nil {
		return nil, err
	}
	space, dimension, err := uc.resolver.ResolveDefaultEmbedder(ctx)
	if err != nil {
		return nil, fmt.Errorf("biz: resolve default embedder for search: %w", err)
	}
	vector, err := uc.embedder.Embed(ctx, space, query)
	if err != nil {
		return nil, fmt.Errorf("biz: embed search query: %w", err)
	}
	ctx = WithRetrievalQuery(ctx, query)
	ctx = WithRetrievalSource(ctx, RetrievalSourceConsole)
	results, err := uc.store.Search(ctx, vector, &VectorFilter{
		Embedder:   space,
		Dimension:  dimension,
		Conditions: knowledgeRetrievalConditions(q.LibraryID, uc.now()),
		Limit:      topK,
	})
	if err != nil {
		return nil, fmt.Errorf("biz: search knowledge: %w", err)
	}
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

// normalizeKnowledgeSearch 校验并归一入参，返回去空白后的查询文本与生效
// top_k（缺省落 5、超限钳制到 20，保证 Limit 恒为正——Limit 为 0 时存储
// 实现会回落默认页大小）。
func normalizeKnowledgeSearch(q *KnowledgeSearchQuery) (string, int, error) {
	if q == nil {
		return "", 0, ErrKnowledgeSearchInvalidArgument
	}
	query := strings.TrimSpace(q.Query)
	if query == "" || utf8.RuneCountInString(query) > MaxKnowledgeSearchQueryRunes {
		return "", 0, ErrKnowledgeSearchInvalidArgument
	}
	if q.LibraryID < 0 || q.TopK < 0 {
		return "", 0, ErrKnowledgeSearchInvalidArgument
	}
	topK := q.TopK
	if topK == 0 {
		topK = DefaultKnowledgeSearchTopK
	}
	if topK > MaxKnowledgeSearchTopK {
		topK = MaxKnowledgeSearchTopK
	}
	return query, topK, nil
}

// knowledgeRetrievalConditions 组装检索固定条件：条目参与（库与条目状态
// 取与后写入 chunk metadata 的 doc_status）且 now 落在有效期 [from, to)
// 内；指定知识库时追加 kb_id 等值。时间为秒级 unix，与摄取侧
// effectiveChunkMetadata 的写入口径一致。
func knowledgeRetrievalConditions(libraryID int64, now time.Time) []MetadataCondition {
	unix := now.Unix()
	conditions := []MetadataCondition{
		{Key: "doc_status", Op: VectorMetadataEq, Values: []any{int(KnowledgeStatusEnabled)}},
		{Key: "valid_from_unix", Op: VectorMetadataLte, Values: []any{unix}},
		{Key: "valid_to_unix", Op: VectorMetadataGt, Values: []any{unix}},
	}
	if libraryID > 0 {
		conditions = append(conditions, MetadataCondition{
			Key: "kb_id", Op: VectorMetadataEq, Values: []any{strconv.FormatInt(libraryID, 10)},
		})
	}
	return conditions
}

// metadataText 安全读取切片 metadata 的字符串键：缺失或类型不符返回空串
// （metadata 来自摄取侧写入，形状异常不致命）。
func metadataText(metadata map[string]any, key string) string {
	s, _ := metadata[key].(string)
	return s
}
