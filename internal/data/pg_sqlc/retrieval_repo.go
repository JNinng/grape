package pg_sqlc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// retrievalLogRepo 是 biz.RetrievalLogRepo 的 PostgreSQL 实现。写入与查询经
// IdentityTx 执行：写入侧租户由门面注入，读取侧由 RLS 兜底且 WHERE 显式带
// 同源租户谓词（RLS 关闭时仍不越租户）。表为追加写，仓储只有写入与分页读
// 取面；jsonb 落库再按字节上限兜底，超限降级为截断声明形态。
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

// retrievalJSONMaxBytes 是 filter_summary / hits / stages 三列的序列化字
// 节上限（应用层兜底）：命中与阶段候选上限 50 条、过滤摘要在写入方侧已
// 做脱敏截断，正常载荷远小于该值；超限按防御性降级处理。
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
	case biz.RetrievalSourceTool, biz.RetrievalSourceConsole, biz.RetrievalSourcePipeline:
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
	var filterSummary, hits, stages []byte
	if len(rec.FilterSummary) > 0 {
		filterSummary = marshalRetrievalJSON(rec.FilterSummary, "filter_summary")
	}
	if len(rec.Hits) > 0 {
		hits = marshalRetrievalJSON(rec.Hits, "hits")
	}
	if len(rec.Stages) > 0 {
		stages = marshalRetrievalJSON(json.RawMessage(rec.Stages), "stages")
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Q().InsertAgentRetrievalLog(ctx, gen.InsertAgentRetrievalLogParams{
			UserID:        auth.UserID,
			TraceID:       textPtr(rec.TraceID),
			SessionID:     textPtr(rec.SessionID),
			Source:        rec.Source,
			Embedder:      textPtr(rec.Embedder),
			QueryText:     textPtr(rec.QueryText),
			RequestedTopK: int32Ptr(rec.RequestedTopK),
			EffectiveTopK: int32Ptr(rec.EffectiveTopK),
			FilterSummary: filterSummary,
			Hits:          hits,
			Stages:        stages,
			DurationMs:    int32Ptr(int(rec.DurationMS)),
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
// 语义为「未指定 / 不适用」，jsonb 为 nil 载荷）。FilterSummary / Hits /
// Stages 以落库字节原样透传（证据面渲染，不做读侧重塑）。
func retrievalLogToBiz(po *gen.AgentRetrievalLog) *biz.RetrievalLog {
	return &biz.RetrievalLog{
		ID:            po.ID,
		TenantID:      po.TenantID,
		UserID:        po.UserID,
		TraceID:       textValue(po.TraceID),
		SessionID:     textValue(po.SessionID),
		Source:        po.Source,
		Embedder:      textValue(po.Embedder),
		QueryText:     textValue(po.QueryText),
		RequestedTopK: int32Value(po.RequestedTopK),
		EffectiveTopK: int32Value(po.EffectiveTopK),
		FilterSummary: json.RawMessage(po.FilterSummary),
		Hits:          json.RawMessage(po.Hits),
		Stages:        json.RawMessage(po.Stages),
		DurationMS:    int64(int32Value(po.DurationMs)),
		Status:        po.Status,
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}
