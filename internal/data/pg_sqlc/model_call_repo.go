package pg_sqlc

import (
	"context"
	"errors"
	"fmt"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// modelCallLogRepo 是 biz.ModelCallLogRepo 的 PostgreSQL 实现。写入与查询
// 都经 IdentityTx 执行，身份缺失或非法一律拒绝；写入侧租户由门面注入，
// 读取侧租户边界由会话变量与表 RLS 承担（动态 List 只投影声明列，读侧行
// 不回填 @internal 列）。表为追加写，仓储只有写入与动态查询面；写入路径
// 由观察面直连，场景/状态词表复检是落库前的最后一道把门。
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

// List 解析动态查询协议并返回分页信封：字段、操作符、投影与游标合法性
// 由生成代码校验；total 与翻页位置无关，计数前剥离游标（游标会把行集
// 收窄到某一页位置之后，导致各页 total 递减）。
func (r *modelCallLogRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.ModelCallLogList, error) {
	var out *biz.ModelCallLogList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseAgentModelCallLogQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListAgentModelCallLog(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.ModelCallLogList{
			Items:      make([]*biz.ModelCallLog, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, modelCallLogToBiz(&res.Items[i]))
		}
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountAgentModelCallLog(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, mapModelCallErr(err)
	}
	return out, nil
}

// mapModelCallErr 将驱动与协议错误映射为模型调用留痕的领域错误：dynq
// 协议错误 → InvalidArgument（保留协议原因）；其余原样透传。不走
// mapStorageError：表 append-only 只有读取与追加，没有唯一冲突与无行
// 语义路径。
func mapModelCallErr(err error) error {
	if isDynqError(err) {
		be := biz.ErrModelCallInvalidArgument
		return kratoserrors.New(int(be.Code), be.Reason, dynq.AsError(err).Reason)
	}
	return err
}

// int64Ptr / int64Value 是本域的可空渲染辅助（与 audit_repo.go 的 int32
// 家族、tenant_admin.go 的 timeToTS 同形态）：0 语义统一为「未上报 /
// 无文档事实」，落 NULL。
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

// modelCallLogToBiz 转换为领域模型；可空列 NULL 渲染为零值（token 与
// 文档 ID 的 0 语义为「未上报 / 无文档事实」）。tenant_id / session_id 是
// @internal 列，不进入动态查询投影，故读侧行不带这两项事实。会话列写侧由
// 观察面从 ctx 补齐（管线侧恒空），读侧不需要回填。
func modelCallLogToBiz(po *gen.AgentModelCallLog) *biz.ModelCallLog {
	return &biz.ModelCallLog{
		ID:            po.ID,
		UserID:        po.UserID,
		TraceID:       textValue(po.TraceID),
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
		CreatedBy:     po.CreatedBy,
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}
