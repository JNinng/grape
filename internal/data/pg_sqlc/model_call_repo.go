package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// modelCallLogRepo 是 biz.ModelCallLogRepo 的 PostgreSQL 实现。写入与查询
// 经 IdentityTx 执行：写入侧租户由门面注入，读取侧由 RLS 兜底且 WHERE 显式
// 带同源租户谓词（RLS 关闭时仍不越租户）。表为追加写，仓储只有写入与分页
// 读取面；身份缺失或非法一律拒绝——留痕旁路面唯一的身份防线。
type modelCallLogRepo struct {
	data *Data
}

// NewModelCallLogRepo 构造模型调用留痕仓储。
func NewModelCallLogRepo(data *Data) biz.ModelCallLogRepo {
	return &modelCallLogRepo{data: data}
}

// modelCallIdentity 从认证 ctx 提取身份：缺失或非法一律拒绝。身份只来自
// 服务端认证中间件，永不受请求侧输入影响（与检索留痕仓储同一纪律）。
func modelCallIdentity(ctx context.Context) (Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return Identity{}, errors.New("pg_sqlc: model call log requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return Identity{}, err
	}
	return auth, nil
}

// Append 写入一条模型调用留痕。租户取门面注入的会话变量，创建者与记录
// 用户同值（留痕行由该用户的操作产生）；场景与状态词表、可空列语义按
// biz DO 注释在此复检（表无 CHECK，仓储是落库前的最后一道把门）。
func (r *modelCallLogRepo) Append(ctx context.Context, entry *biz.ModelCallEntry) error {
	if entry == nil {
		return errors.New("pg_sqlc: model call log entry is required")
	}
	if !biz.ValidModelCallScenario(entry.Scenario) {
		return fmt.Errorf("pg_sqlc: model call log scenario %q is unknown", entry.Scenario)
	}
	switch entry.Status {
	case biz.ModelCallStatusSuccess, biz.ModelCallStatusError:
	default:
		return fmt.Errorf("pg_sqlc: model call log status %q is unknown", entry.Status)
	}
	auth, err := modelCallIdentity(ctx)
	if err != nil {
		return err
	}
	return r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		_, err := s.Q().InsertAgentModelCallLog(ctx, gen.InsertAgentModelCallLogParams{
			UserID:        auth.UserID,
			TraceID:       textPtr(entry.TraceID),
			SessionID:     textPtr(entry.SessionID),
			Scenario:      entry.Scenario,
			ModelKey:      textPtr(entry.ModelKey),
			Endpoint:      textPtr(entry.Endpoint),
			DocumentID:    int64Ptr(entry.DocumentID),
			InputSummary:  textPtr(entry.InputSummary),
			OutputSummary: textPtr(entry.OutputSummary),
			InputTokens:   int32Ptr(int(entry.InputTokens)),
			OutputTokens:  int32Ptr(int(entry.OutputTokens)),
			DurationMs:    int32Ptr(int(entry.DurationMS)),
			Status:        entry.Status,
			Error:         textPtr(entry.Error),
		})
		return err
	})
}

// modelCallFilterParams 把 biz 过滤归一为 List/Count 共用的生成参数。
func modelCallFilterParams(f *biz.ModelCallFilter) (gen.ListAgentModelCallLogsParams, gen.CountAgentModelCallLogsParams) {
	list := gen.ListAgentModelCallLogsParams{
		Scenario:    textPtr(f.Scenario),
		ModelKey:    textPtr(f.ModelKey),
		Status:      textPtr(f.Status),
		DocumentID:  int64Ptr(f.DocumentID),
		CreatedFrom: timeToTS(timePtr(f.CreatedFrom)),
		CreatedTo:   timeToTS(timePtr(f.CreatedTo)),
		Limit:       int64(f.Limit),
		Offset:      int64(f.Offset),
	}
	count := gen.CountAgentModelCallLogsParams{
		Scenario:    list.Scenario,
		ModelKey:    list.ModelKey,
		Status:      list.Status,
		DocumentID:  list.DocumentID,
		CreatedFrom: list.CreatedFrom,
		CreatedTo:   list.CreatedTo,
	}
	return list, count
}

// List 按场景/模型键/状态/文档/时间范围过滤分页查询模型调用留痕（时间
// 倒序，同秒以 id tie-break）。
func (r *modelCallLogRepo) List(ctx context.Context, filter *biz.ModelCallFilter) (*biz.ModelCallList, error) {
	auth, err := modelCallIdentity(ctx)
	if err != nil {
		return nil, err
	}
	f := &biz.ModelCallFilter{}
	if filter != nil {
		*f = *filter
	}
	params, countParams := modelCallFilterParams(f)
	var out *biz.ModelCallList
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		rows, err := s.Q().ListAgentModelCallLogs(ctx, params)
		if err != nil {
			return err
		}
		total, err := s.Q().CountAgentModelCallLogs(ctx, countParams)
		if err != nil {
			return err
		}
		items := make([]*biz.ModelCallLog, 0, len(rows))
		for i := range rows {
			items = append(items, modelCallLogToBiz(&rows[i]))
		}
		out = &biz.ModelCallList{Items: items, Total: total}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Count 按同条件统计条数（与 List 同口径）。
func (r *modelCallLogRepo) Count(ctx context.Context, filter *biz.ModelCallFilter) (int64, error) {
	auth, err := modelCallIdentity(ctx)
	if err != nil {
		return 0, err
	}
	f := &biz.ModelCallFilter{}
	if filter != nil {
		*f = *filter
	}
	_, countParams := modelCallFilterParams(f)
	var total int64
	err = r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		var err error
		total, err = s.Q().CountAgentModelCallLogs(ctx, countParams)
		return err
	})
	return total, err
}

// int64Ptr / int64Value / timePtr 是本域的可空渲染辅助（与 audit_repo.go
// 的 int32 家族、tenant_admin.go 的 timeToTS 同形态）：0 / 零值语义统一为
// 「未上报 / 无文档事实 / 不过滤」，落 NULL。
func int64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// modelCallLogToBiz 转换为领域模型；可空列 NULL 渲染为零值（token 与
// 文档 ID 的 0 语义为「未上报 / 无文档事实」）。
func modelCallLogToBiz(po *gen.AgentModelCallLog) *biz.ModelCallLog {
	return &biz.ModelCallLog{
		ID:            po.ID,
		TenantID:      po.TenantID,
		UserID:        po.UserID,
		TraceID:       textValue(po.TraceID),
		SessionID:     textValue(po.SessionID),
		Scenario:      po.Scenario,
		ModelKey:      textValue(po.ModelKey),
		Endpoint:      textValue(po.Endpoint),
		DocumentID:    int64Value(po.DocumentID),
		InputSummary:  textValue(po.InputSummary),
		OutputSummary: textValue(po.OutputSummary),
		InputTokens:   int32Value(po.InputTokens),
		OutputTokens:  int32Value(po.OutputTokens),
		DurationMS:    int64(int32Value(po.DurationMs)),
		Status:        po.Status,
		Error:         textValue(po.Error),
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}
