package vector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"grape/internal/biz"
	"grape/internal/conf"
	pgsqlc "grape/internal/data/pg_sqlc"
	"grape/pkg/redact"
)

// 检索留痕装饰器：位于向量仓储之上、框架适配器之下，检索与摄取两条链路
// 都经它留痕（一次操作一行，失败行同样留痕）。留痕点、独立 IdentityTx、
// 脱敏与长度兜底、top_k 口径见 internal/data/README.md 的
// 「检索留痕装饰器」。
const (
	// retrievalQueryMaxRunes 是查询文本的兜底截断上限（rune 计）。
	retrievalQueryMaxRunes = 4096
	// retrievalMaxLoggedHits 是命中数组的条数上限（应用层截断，超限
	// 不另行标记——见 schema/97_agent_retrieval_log.sql 列注释）。
	retrievalMaxLoggedHits = 50
)

// VectorStoreSupply 是向量存储的 wire 供给包装（与 SystemData 对 *pgsqlc.Data
// 同一手法）：留痕装饰器以本类型为入参包装出最终对外的 biz.VectorStore，
// 避免原仓储与装饰器提供同一接口类型造成装配环。字段嵌入保持零成本
// 透传，测试与非 wire 消费方仍可直接用 NewVectorStore。
type VectorStoreSupply struct {
	biz.VectorStore
}

// NewVectorStoreSupply 提供向量存储的 wire 供给：构造既有 pgvector 仓储
// （含维度探针等装配期校验）并包装为可装饰类型。
func NewVectorStoreSupply(c *conf.Vector, d *pgsqlc.Data) (VectorStoreSupply, func(), error) {
	store, cleanup, err := NewVectorStore(c, d)
	if err != nil {
		return VectorStoreSupply{}, nil, err
	}
	return VectorStoreSupply{VectorStore: store}, cleanup, nil
}

// NewLoggedVectorStore 用检索留痕装饰器包装向量仓储，产出对外的
// biz.VectorStore：框架适配器（agent.NewVectorStore）与摄取管线拿到的
// 都是装饰后的实例，留痕对两条链路透明。registry 是与日志/审计表/复现
// 视图共用的脱敏注册表（单一代码点，经 wire 提供同一实例）；registry
// 为 nil 时按「放行原值 + 截断兜底」降级（与审计用例同口径）。
func NewLoggedVectorStore(supply VectorStoreSupply, logs biz.RetrievalLogRepo, registry *redact.Registry) (biz.VectorStore, func(), error) {
	if supply.VectorStore == nil {
		return nil, nil, errors.New("pg_sqlc: logged vector store requires an inner store")
	}
	if logs == nil {
		return nil, nil, errors.New("pg_sqlc: logged vector store requires a retrieval log repo")
	}
	return &loggedVectorStore{inner: supply.VectorStore, logs: logs, registry: registry}, func() {}, nil
}

// loggedVectorStore 是留痕装饰器实现。
type loggedVectorStore struct {
	inner    biz.VectorStore
	logs     biz.RetrievalLogRepo
	registry *redact.Registry
}

// 装饰器与被装饰仓储同守一份 biz.VectorStore 契约：接口增删方法在此
// 编译期暴露，避免装饰器静默漏转发。
var _ biz.VectorStore = (*loggedVectorStore)(nil)

// Search 转发向量检索并留痕：无论成败都记一行（status 区分），留痕写入
// 失败只记日志，检索结果不受影响。
func (s *loggedVectorStore) Search(ctx context.Context, embedding []float32, filter *biz.VectorFilter) ([]*biz.ScoredVectorDocument, error) {
	start := time.Now()
	hits, err := s.inner.Search(ctx, embedding, filter)
	rec := s.buildRecord(ctx, biz.RetrievalSourceTool, filter, retrievalHitList(hits), err, time.Since(start))
	rec.QueryText = s.redactQueryText(biz.RetrievalQueryFromContext(ctx))
	rec.RequestedTopK = requestedTopK(filter)
	rec.EffectiveTopK = effectiveTopK(filter)
	s.record(ctx, rec)
	return hits, err
}

// Add 转发写入并按摄取侧留痕（来源 pipeline）：作用文档进 hits 列
// （单元素），锚点取切片元数据（管线摄取可反查原文位置）。
func (s *loggedVectorStore) Add(ctx context.Context, doc *biz.VectorDocument) error {
	start := time.Now()
	err := s.inner.Add(ctx, doc)
	s.record(ctx, s.buildIngestRecord(ctx, doc, err, time.Since(start)))
	return err
}

// Update 转发覆盖并按摄取侧留痕，语义同 Add。
func (s *loggedVectorStore) Update(ctx context.Context, doc *biz.VectorDocument) error {
	start := time.Now()
	err := s.inner.Update(ctx, doc)
	s.record(ctx, s.buildIngestRecord(ctx, doc, err, time.Since(start)))
	return err
}

// Delete 按 ID 删除：点删不是召回与摄取证据，转发不留痕（留痕点声明见
// 文件头注释）。
func (s *loggedVectorStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

// Get 按 ID 取文档：点读是引用反查的读路径而非召回/摄取证据，转发不留
// 痕（留痕点声明见文件头注释）。
func (s *loggedVectorStore) Get(ctx context.Context, id string) (*biz.VectorDocument, error) {
	return s.inner.Get(ctx, id)
}

// DeleteByFilter 转发条件删除并按摄取侧留痕：条件摘要进 filter_summary
// （管线的同来源覆盖清除即此路径），行数结果由调用方持有、不入留痕。
func (s *loggedVectorStore) DeleteByFilter(ctx context.Context, filter *biz.VectorFilter) (int64, error) {
	start := time.Now()
	deleted, err := s.inner.DeleteByFilter(ctx, filter)
	rec := s.buildRecord(ctx, biz.RetrievalSourcePipeline, filter, nil, err, time.Since(start))
	s.record(ctx, rec)
	return deleted, err
}

// Count 转发统计，不留痕（枚举统计非证据面）。
func (s *loggedVectorStore) Count(ctx context.Context, filter *biz.VectorFilter) (int64, error) {
	return s.inner.Count(ctx, filter)
}

// UpdateMetadata 转发 metadata 合并更新，不留痕（目录状态同步非检索证据）。
func (s *loggedVectorStore) UpdateMetadata(ctx context.Context, filter *biz.VectorFilter, patch map[string]any) (int64, error) {
	return s.inner.UpdateMetadata(ctx, filter, patch)
}

// GetMetadata 转发枚举，不留痕（枚举统计非证据面）。
func (s *loggedVectorStore) GetMetadata(ctx context.Context, filter *biz.VectorFilter) (map[string]map[string]any, error) {
	return s.inner.GetMetadata(ctx, filter)
}

// buildRecord 组装留痕行公共部分：身份/租户不进行（仓储从认证 ctx 提取
// 经 IdentityTx 注入），trace_id / session_id 取 ctx 事实，嵌入空间取
// 过滤声明，查询文本与 top_k 由检索路径的调用方补充。status 按操作成败
// 取终态。
func (s *loggedVectorStore) buildRecord(ctx context.Context, source string, filter *biz.VectorFilter, hits []biz.RetrievalHit, opErr error, elapsed time.Duration) *biz.RetrievalLogRecord {
	rec := &biz.RetrievalLogRecord{
		TraceID:    biz.TraceIDFromContext(ctx),
		SessionID:  biz.SessionIDFromContext(ctx),
		Source:     source,
		Hits:       hits,
		DurationMS: elapsed.Milliseconds(),
		Status:     biz.RetrievalStatusSuccess,
	}
	if opErr != nil {
		rec.Status = biz.RetrievalStatusError
	}
	if filter != nil {
		rec.Embedder = filter.Embedder
	}
	if summary := s.redactFilterSummary(retrievalFilterSummary(filter)); summary != nil {
		rec.FilterSummary = summary
	}
	return rec
}

// buildIngestRecord 组装摄取侧行：作用文档进 hits 列（单元素，锚点取
// 切片元数据），嵌入空间取文档归属，无查询文本与 top_k 语义。
func (s *loggedVectorStore) buildIngestRecord(ctx context.Context, doc *biz.VectorDocument, opErr error, elapsed time.Duration) *biz.RetrievalLogRecord {
	rec := &biz.RetrievalLogRecord{
		TraceID:    biz.TraceIDFromContext(ctx),
		SessionID:  biz.SessionIDFromContext(ctx),
		Source:     biz.RetrievalSourcePipeline,
		DurationMS: elapsed.Milliseconds(),
		Status:     biz.RetrievalStatusSuccess,
	}
	if opErr != nil {
		rec.Status = biz.RetrievalStatusError
	}
	if doc != nil {
		rec.Embedder = doc.Embedder
		rec.Hits = []biz.RetrievalHit{{
			DocumentID: doc.ID,
			Anchor:     metadataString(doc.Metadata, "anchor"),
		}}
	}
	return rec
}

// record 落一行留痕：写入失败只记日志不中断检索（旁路观察面纪律）。
// 身份缺失的失败同样只记日志——留痕写不进去不能改变业务调用的结果。
func (s *loggedVectorStore) record(ctx context.Context, rec *biz.RetrievalLogRecord) {
	if err := s.logs.Append(ctx, rec); err != nil {
		slog.WarnContext(ctx, "retrieval log write failed", "source", rec.Source, "status", rec.Status, "error", err)
	}
}

// redactQueryText 消费 retrieval 类别脱敏查询文本并做长度兜底（对齐
// 审计摘要的「未登记放行 + 截断」口径）。无查询事实返回空串（落库 NULL，
// 不伪造）。
func (s *loggedVectorStore) redactQueryText(query string) string {
	if query == "" {
		return ""
	}
	if s.registry != nil {
		redacted, _ := s.registry.Redact(redact.CategoryRetrieval, query)
		query = redacted
	}
	return truncateRetrievalText(query)
}

// redactFilterSummary 消费 retrieval 类别脱敏过滤摘要：序列化 → 脱敏 →
// 重解析回 jsonb 载体；截断破坏 JSON 结构（或序列化失败）时降级为截断
// 声明形态。无条件摘要返回 nil（落库 NULL）。
func (s *loggedVectorStore) redactFilterSummary(summary map[string]any) map[string]any {
	if len(summary) == 0 {
		return nil
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		return map[string]any{"summary_error": "unserializable filter"}
	}
	if s.registry != nil {
		redacted, _ := s.registry.Redact(redact.CategoryRetrieval, string(raw))
		raw = []byte(redacted)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"summary_truncated": truncateRetrievalText(string(raw))}
	}
	return out
}

// retrievalFilterSummary 把契约过滤条件折叠为摘要载体（ids / metadata /
// min_score 三键，空值省略）。
func retrievalFilterSummary(filter *biz.VectorFilter) map[string]any {
	if filter == nil {
		return nil
	}
	summary := make(map[string]any, 3)
	if len(filter.IDs) > 0 {
		summary["ids"] = filter.IDs
	}
	if len(filter.Metadata) > 0 {
		summary["metadata"] = filter.Metadata
	}
	if filter.MinScore > 0 {
		summary["min_score"] = filter.MinScore
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

// retrievalHitList 把检索命中折叠为证据单元（上限截断）；nil 命中保持
// nil（落库 NULL——无命中与检索失败在 hits 列同形，状态列区分成败）。
func retrievalHitList(hits []*biz.ScoredVectorDocument) []biz.RetrievalHit {
	if len(hits) == 0 {
		return nil
	}
	capped := hits
	if len(capped) > retrievalMaxLoggedHits {
		capped = capped[:retrievalMaxLoggedHits]
	}
	out := make([]biz.RetrievalHit, 0, len(capped))
	for _, hit := range capped {
		if hit == nil || hit.Document == nil {
			continue
		}
		out = append(out, biz.RetrievalHit{
			DocumentID: hit.Document.ID,
			Score:      hit.Score,
			Anchor:     metadataString(hit.Document.Metadata, "anchor"),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// requestedTopK / effectiveTopK 渲染 top_k 两值：请求值取调用方传入的
// Limit（0=未指定），生效值在未指定/非正时取存储层默认页大小
// （vectorDefaultLimit，与仓储的默认补齐同源同包）。
func requestedTopK(filter *biz.VectorFilter) int {
	if filter == nil {
		return 0
	}
	return filter.Limit
}

func effectiveTopK(filter *biz.VectorFilter) int {
	if filter == nil || filter.Limit <= 0 {
		return vectorDefaultLimit
	}
	return filter.Limit
}

// metadataString 安全读取文档元数据中的字符串键：键缺失或类型不符返回
// 空串（元数据来自摄取侧写入，形状异常不致命；与 agent 层的同名辅助
// 函数同语义，包间不共享私有辅助）。
func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	s, _ := metadata[key].(string)
	return s
}

// truncateRetrievalText 按 rune 截断到兜底上限（多字节字符不劈开），
// 尾部以标记说明发生了截断（与审计摘要同一形态）。
func truncateRetrievalText(value string) string {
	if utf8.RuneCountInString(value) <= retrievalQueryMaxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:retrievalQueryMaxRunes]) + "...(truncated)"
}
