package agent

import (
	"context"
	"errors"
	"fmt"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// errVectorUnsupported 是适配器未映射操作的统一错误：每个拒绝点注明
// 理由（见各方法）。框架接口的完整面中，暂无消费方的操作不预设语义，
// 显式拒绝优于静默降级或猜测。
var errVectorUnsupported = errors.New("agent: vector store operation not supported")

// vectorStoreAdapter 把 biz.VectorStore 契约适配为框架
// knowledge/vectorstore.VectorStore。这是 TODO 决策 6 的强制应用层适配
// 器：框架 pgvector store 自建连接池、启动建表建索引且无法接 grape 的
// pgx 池与身份事务，不引入；租户隔离由 biz 契约的 data 实现从认证 ctx
// 强制，本适配器只做形状映射，不自行取用身份（避免第二套隔离逻辑）。
type vectorStoreAdapter struct {
	store biz.VectorStore
}

var _ vectorstore.VectorStore = (*vectorStoreAdapter)(nil)

// requireStore 校验内部契约非 nil：nil 契约是装配错误，显式报错而非
// panic。
func (a *vectorStoreAdapter) requireStore() (biz.VectorStore, error) {
	if a.store == nil {
		return nil, errors.New("agent: vector store is not configured")
	}
	return a.store, nil
}

// Add 落库文档与向量：形状映射，身份与维度校验都在契约实现内。
func (a *vectorStoreAdapter) Add(ctx context.Context, doc *document.Document, embedding []float64) error {
	store, err := a.requireStore()
	if err != nil {
		return err
	}
	if doc == nil {
		return errors.New("agent: document is required")
	}
	if len(embedding) == 0 {
		return errors.New("agent: embedding is required")
	}
	return store.Add(ctx, &biz.VectorDocument{
		ID:        doc.ID,
		Name:      doc.Name,
		Content:   doc.Content,
		Embedding: toFloat32(embedding),
		Metadata:  doc.Metadata,
	})
}

// Get 按 ID 取文档与向量；不存在/跨租户错误由契约实现给出
// （biz.ErrVectorDocNotFound 透传，不二次包装丢失判定能力）。
func (a *vectorStoreAdapter) Get(ctx context.Context, id string) (*document.Document, []float64, error) {
	store, err := a.requireStore()
	if err != nil {
		return nil, nil, err
	}
	doc, err := store.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return toFrameworkDocument(doc), toFloat64(doc.Embedding), nil
}

// Update 整体覆盖文档与向量，映射语义与契约一致。
func (a *vectorStoreAdapter) Update(ctx context.Context, doc *document.Document, embedding []float64) error {
	store, err := a.requireStore()
	if err != nil {
		return err
	}
	if doc == nil {
		return errors.New("agent: document is required")
	}
	if len(embedding) == 0 {
		return errors.New("agent: embedding is required")
	}
	return store.Update(ctx, &biz.VectorDocument{
		ID:        doc.ID,
		Name:      doc.Name,
		Content:   doc.Content,
		Embedding: toFloat32(embedding),
		Metadata:  doc.Metadata,
	})
}

// Delete 按 ID 删除，直接映射。
func (a *vectorStoreAdapter) Delete(ctx context.Context, id string) error {
	store, err := a.requireStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, id)
}

// Search 只落向量检索（SearchModeVector）：keyword/hybrid 需要 tsvector
// 列与 embedder 管线配合（D2 范围），filter-only 检索没有相似度语义，
// 其余模式显式拒绝，不静默降级成纯向量而改变检索行为。searchfilter 的
// UniversalFilterCondition 翻译同样归 D2 检索工具落地时按需映射。
func (a *vectorStoreAdapter) Search(ctx context.Context, query *vectorstore.SearchQuery) (*vectorstore.SearchResult, error) {
	store, err := a.requireStore()
	if err != nil {
		return nil, err
	}
	if query == nil {
		return nil, errors.New("agent: search query is required")
	}
	if query.SearchMode != vectorstore.SearchModeVector {
		return nil, fmt.Errorf("%w: search mode %d needs the keyword/hybrid pipeline planned for D2", errVectorUnsupported, query.SearchMode)
	}
	if query.Filter != nil && query.Filter.FilterCondition != nil {
		return nil, fmt.Errorf("%w: universal filter condition translation is planned with the D2 retrieval tool", errVectorUnsupported)
	}
	if len(query.Vector) == 0 {
		return nil, errors.New("agent: query vector is required")
	}
	filter := &biz.VectorFilter{Limit: query.Limit, MinScore: query.MinScore}
	if query.Filter != nil {
		filter.IDs = query.Filter.IDs
		filter.Metadata = query.Filter.Metadata
	}
	hits, err := store.Search(ctx, toFloat32(query.Vector), filter)
	if err != nil {
		return nil, err
	}
	results := make([]*vectorstore.ScoredDocument, 0, len(hits))
	for _, hit := range hits {
		results = append(results, &vectorstore.ScoredDocument{
			Document: toFrameworkDocument(hit.Document),
			Score:    hit.Score,
		})
	}
	return &vectorstore.SearchResult{Results: results}, nil
}

// DeleteByFilter 映射 IDs 与 metadata 过滤；DeleteAll（租户级全量清理）
// 是跨租户管理口径（super 路径，D1 只留占位），不适配。两条件均空在
// 契约侧同样拒绝，这里先给出框架语境的错误。
func (a *vectorStoreAdapter) DeleteByFilter(ctx context.Context, opts ...vectorstore.DeleteOption) error {
	store, err := a.requireStore()
	if err != nil {
		return err
	}
	cfg := vectorstore.ApplyDeleteOptions(opts...)
	if cfg.DeleteAll {
		return fmt.Errorf("%w: delete-all is a tenant-wide purge reserved for the super management path", errVectorUnsupported)
	}
	if len(cfg.DocumentIDs) == 0 && len(cfg.Filter) == 0 {
		return errors.New("agent: delete by filter requires document ids or a filter")
	}
	_, err = store.DeleteByFilter(ctx, &biz.VectorFilter{IDs: cfg.DocumentIDs, Metadata: cfg.Filter})
	return err
}

// UpdateByFilter 不适配：框架的批量更新语义依赖 searchfilter 条件 DSL
// 与 metadata.* 局部更新，当前无消费方；为不囤积未经使用的复杂度，
// 待 D2 检索工具给出真实需求时按需映射。
func (a *vectorStoreAdapter) UpdateByFilter(ctx context.Context, opts ...vectorstore.UpdateByFilterOption) (int64, error) {
	if _, err := a.requireStore(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("%w: bulk update by filter has no consumer yet; map it with the D2 retrieval tool when needed", errVectorUnsupported)
}

// Count 映射 metadata 过滤计数。
func (a *vectorStoreAdapter) Count(ctx context.Context, opts ...vectorstore.CountOption) (int, error) {
	store, err := a.requireStore()
	if err != nil {
		return 0, err
	}
	cfg := vectorstore.ApplyCountOptions(opts...)
	total, err := store.Count(ctx, &biz.VectorFilter{Metadata: cfg.Filter})
	if err != nil {
		return 0, err
	}
	return int(total), nil
}

// GetMetadata 映射 IDs/metadata 过滤与分页；框架侧的 Limit/Offset 校验
// （ApplyGetMetadataOptions）先行，分页语义与契约一致。
func (a *vectorStoreAdapter) GetMetadata(ctx context.Context, opts ...vectorstore.GetMetadataOption) (map[string]vectorstore.DocumentMetadata, error) {
	store, err := a.requireStore()
	if err != nil {
		return nil, err
	}
	cfg, err := vectorstore.ApplyGetMetadataOptions(opts...)
	if err != nil {
		return nil, err
	}
	metadata, err := store.GetMetadata(ctx, &biz.VectorFilter{
		IDs:      cfg.IDs,
		Metadata: cfg.Filter,
		Limit:    cfg.Limit,
		Offset:   cfg.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]vectorstore.DocumentMetadata, len(metadata))
	for id, m := range metadata {
		out[id] = vectorstore.DocumentMetadata{Metadata: m}
	}
	return out, nil
}

// Close 无自有资源：连接池与向量仓储的生命周期归 data 层装配
// （pg_sqlc.NewVectorStore 的 cleanup），按框架接口要求返回 nil。
func (a *vectorStoreAdapter) Close() error { return nil }

// toFrameworkDocument 领域文档 → 框架文档（EmbeddingText 不参与存储，
// 摄取时已物化为向量）。
func toFrameworkDocument(doc *biz.VectorDocument) *document.Document {
	return &document.Document{
		ID:        doc.ID,
		Name:      doc.Name,
		Content:   doc.Content,
		Metadata:  doc.Metadata,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
	}
}

// toFloat32/toFloat64 是向量精度转换：列类型是 float4，float64 入参
// 降精度是存储语义的一部分，非适配缺陷。
func toFloat32(values []float64) []float32 {
	out := make([]float32, len(values))
	for i, v := range values {
		out[i] = float32(v)
	}
	return out
}

func toFloat64(values []float32) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = float64(v)
	}
	return out
}
