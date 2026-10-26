package pg_sqlc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// retrievalLogRepo 是 biz.RetrievalLogRepo 的 PostgreSQL 实现（TODO 流 D
// 的 D3）。写入与查询全部经身份事务门面 IdentityTx 执行（每事务
// set_config('app.tenant_id', ..., true)）：写入侧租户由门面注入、查询
// 侧租户由 RLS 兜底（schema/97_agent_retrieval_log.sql 的策略），读侧
// WHERE 再显式带同源租户谓词（queries/agent_retrieval_log.sql 文件头
// 声明的应用层兜底，RLS 被关闭时仍不越租户）。表为追加写，仓储只提供
// 写入与分页读取，没有更新/删除面。
//
// jsonb 落库的长度兜底：调用方（留痕装饰器）已做脱敏与条数上限，本仓储
// 再按字节上限做最后防线——超限整体降级为截断声明形态，保证行宽有界
// （上限调整不伴随存量行迁移，见域文件头注释）。
type retrievalLogRepo struct {
	data *Data
}

// NewRetrievalLogRepo 构造检索留痕仓储。
func NewRetrievalLogRepo(data *Data) biz.RetrievalLogRepo {
	return &retrievalLogRepo{data: data}
}

// retrievalLogIdentity 从认证 ctx 提取身份：缺失或非法一律拒绝。身份只
// 来自服务端认证中间件，永不受请求侧输入影响。
func retrievalLogIdentity(ctx context.Context) (Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return Identity{}, errors.New("pg_sqlc: retrieval log requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return Identity{}, err
	}
	return auth, nil
}

// retrievalJSONMaxBytes 是 filter_summary / hits 两列的序列化字节上限
// （应用层兜底）：命中上限 50 条、过滤摘要在装饰器侧已做脱敏截断，正常
// 载荷远小于该值；超限按防御性降级处理。
const retrievalJSONMaxBytes = 64 * 1024

// retrievalJSONFallback 是超限降级的 jsonb 形态：保留「发生过截断」的
// 事实声明，不静默丢弃也不落超大行。
func retrievalJSONFallback(kind string) []byte {
	raw, err := json.Marshal(map[string]any{"truncated": true, "kind": kind})
	if err != nil {
		return []byte(`{"truncated":true}`)
	}
	return raw
}

// marshalRetrievalJSON 把领域值序列化为 jsonb 载荷，超限时降级为截断
// 声明形态（长度兜底的最后防线）。
func marshalRetrievalJSON(v any, kind string) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return retrievalJSONFallback(kind)
	}
	if len(raw) > retrievalJSONMaxBytes {
		return retrievalJSONFallback(kind)
	}
	return raw
}

// Append 写入一条检索留痕。租户取门面注入的会话变量，创建者与记录用户
// 同值（留痕行由该用户的操作产生）；空值语义按 biz DO 注释渲染 NULL。
func (r *retrievalLogRepo) Append(ctx context.Context, rec *biz.RetrievalLogRecord) error {
	if rec == nil {
		return errors.New("pg_sqlc: retrieval log record is required")
	}
	switch rec.Source {
	case biz.RetrievalSourceTool, biz.RetrievalSourcePipeline:
	default:
		return fmt.Errorf("pg_sqlc: retrieval log source %q is unknown", rec.Source)
	}
	switch rec.Status {
	case biz.RetrievalStatusSuccess, biz.RetrievalStatusError:
	default:
		return fmt.Errorf("pg_sqlc: retrieval log status %q is unknown", rec.Status)
	}
	auth, err := retrievalLogIdentity(ctx)
	if err != nil {
		return err
	}
	var filterSummary, hits []byte
	if len(rec.FilterSummary) > 0 {
		filterSummary = marshalRetrievalJSON(rec.FilterSummary, "filter_summary")
	}
	if len(rec.Hits) > 0 {
		hits = marshalRetrievalJSON(rec.Hits, "hits")
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Q().InsertAgentRetrievalLog(ctx, gen.InsertAgentRetrievalLogParams{
			UserID:        auth.UserID,
			TraceID:       textPtr(rec.TraceID),
			SessionID:     textPtr(rec.SessionID),
			Source:        rec.Source,
			QueryText:     textPtr(rec.QueryText),
			RequestedTopK: int32PtrNonZero(rec.RequestedTopK),
			EffectiveTopK: int32PtrNonZero(rec.EffectiveTopK),
			FilterSummary: filterSummary,
			Hits:          hits,
			DurationMs:    int32Ptr(int32(rec.DurationMS)),
			Status:        rec.Status,
		})
		return err
	})
}

// List 按会话/来源过滤分页查询检索留痕（时间倒序，同秒以 id tie-break）。
func (r *retrievalLogRepo) List(ctx context.Context, filter *biz.RetrievalLogFilter) (*biz.RetrievalLogList, error) {
	auth, err := retrievalLogIdentity(ctx)
	if err != nil {
		return nil, err
	}
	f := &biz.RetrievalLogFilter{}
	if filter != nil {
		*f = *filter
	}
	params := gen.ListAgentRetrievalLogsParams{
		SessionID: textPtr(f.SessionID),
		Source:    textPtr(f.Source),
		Limit:     int64(f.Limit),
		Offset:    int64(f.Offset),
	}
	countParams := gen.CountAgentRetrievalLogsParams{
		SessionID: params.SessionID,
		Source:    params.Source,
	}
	var out *biz.RetrievalLogList
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		rows, err := s.Q().ListAgentRetrievalLogs(ctx, params)
		if err != nil {
			return err
		}
		total, err := s.Q().CountAgentRetrievalLogs(ctx, countParams)
		if err != nil {
			return err
		}
		items := make([]*biz.RetrievalLog, 0, len(rows))
		for i := range rows {
			items = append(items, retrievalLogToBiz(&rows[i]))
		}
		out = &biz.RetrievalLogList{Items: items, Total: total}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// retrievalLogToBiz 转换为领域模型；可空列 NULL 渲染为零值（top_k 的 0
// 语义为「未指定 / 不适用」，jsonb 为 nil 载荷）。
func retrievalLogToBiz(po *gen.AgentRetrievalLog) *biz.RetrievalLog {
	return &biz.RetrievalLog{
		ID:            po.ID,
		TenantID:      po.TenantID,
		UserID:        po.UserID,
		TraceID:       textValue(po.TraceID),
		SessionID:     textValue(po.SessionID),
		Source:        po.Source,
		QueryText:     textValue(po.QueryText),
		RequestedTopK: int32Value(po.RequestedTopK),
		EffectiveTopK: int32Value(po.EffectiveTopK),
		FilterSummary: json.RawMessage(po.FilterSummary),
		Hits:          json.RawMessage(po.Hits),
		DurationMS:    int64(int32Value(po.DurationMs)),
		Status:        po.Status,
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}

// int32PtrNonZero 是 0 → NULL 的可空整数渲染：top_k 的 0 语义是「未
// 指定 / 不适用」，与耗时共用「未记录落 NULL」口径。
func int32PtrNonZero(v int) *int32 {
	if v == 0 {
		return nil
	}
	out := int32(v)
	return &out
}
