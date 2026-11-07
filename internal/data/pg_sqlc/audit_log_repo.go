package pg_sqlc

import (
	"context"
	"errors"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// auditLogRepo 是 biz.AuditLogRepo 的 PostgreSQL 实现：登录日志与操作日志
// 两表的追加写与查询面（append-only，无更新/删除口径）。两表是租户内公共
// 资源：表无 @scope，行级无范围，租户边界由 RLS 会话变量承担。登录日志
// 在身份成立前经 TenantTx 追加（审计列显式给出），其余读写经 IdentityTx；
// 读侧租户谓词由 RLS 承担（两表均为基线 RLS 表），不做应用层重复过滤。
type auditLogRepo struct {
	data *Data
}

// NewAuditLogRepo 构造审计日志仓储。
func NewAuditLogRepo(data *Data) biz.AuditLogRepo {
	return &auditLogRepo{data: data}
}

// toBizLoginLog 将持久化登录日志转换为领域模型。
func toBizLoginLog(po *gen.SysLoginLog) *biz.LoginLog {
	if po == nil {
		return nil
	}
	return &biz.LoginLog{
		ID:            po.ID,
		UserID:        po.UserID,
		Username:      po.Username,
		IPAddress:     po.IpAddress,
		UserAgent:     po.UserAgent,
		LoginType:     po.LoginType,
		IsSuccess:     po.IsSuccess,
		FailureReason: po.FailureReason,
		LoginAt:       tsToTime(po.LoginAt),
		CreatedBy:     po.CreatedBy,
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}

// toBizOperationLog 将持久化操作日志转换为领域模型。JSON 快照原样透传，
// 不在仓储层解码/重编码。
func toBizOperationLog(po *gen.SysOperationLog) *biz.OperationLog {
	if po == nil {
		return nil
	}
	return &biz.OperationLog{
		ID:            po.ID,
		UserID:        po.UserID,
		Username:      po.Username,
		OperationType: po.OperationType,
		TargetTable:   po.TargetTable,
		TargetID:      po.TargetID,
		OldValue:      po.OldValue,
		NewValue:      po.NewValue,
		IPAddress:     po.IpAddress,
		UserAgent:     po.UserAgent,
		APIPath:       po.ApiPath,
		HTTPMethod:    po.HttpMethod,
		DurationMs:    po.DurationMs,
		ResultCode:    po.ResultCode,
		ResultMsg:     po.ResultMsg,
		RequestID:     po.RequestID,
		OperatedAt:    tsToTime(po.OperatedAt),
		CreatedBy:     po.CreatedBy,
		CreatedAt:     tsToTime(po.CreatedAt),
	}
}

// GetLoginLogByID 按主键读取登录日志。租户边界由 RLS 承担，跨租户等同不存在。
func (r *auditLogRepo) GetLoginLogByID(ctx context.Context, auth biz.Identity, id int64) (*biz.LoginLog, error) {
	var out *biz.LoginLog
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysLoginLogByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizLoginLog(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListLoginLogs 解析动态查询协议并返回分页信封；表无 @scope，无需注入
// 守卫，字段、操作符、投影与游标合法性由生成代码校验，协议错误映射为
// ErrAuditLogInvalidArgument。
func (r *auditLogRepo) ListLoginLogs(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.LoginLogList, error) {
	var out *biz.LoginLogList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysLoginLogQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysLoginLog(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.LoginLogList{
			Items:      make([]*biz.LoginLog, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizLoginLog(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysLoginLog(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// InsertLoginLog 追加一条登录日志。登录路径的操作者身份尚未成立，经
// TenantTx 只注入租户（RLS 允许本租户追加）；user_id/created_by 由入参
// 显式给出（目标用户不可定位时为 0）。这是 TenantTx 文档化的 append-only
// 审计写例外，不承载其他业务写路径。
func (r *auditLogRepo) InsertLoginLog(ctx context.Context, in *biz.LoginLogInput) error {
	if in == nil || in.TenantID == "" {
		return biz.ErrAuditLogInvalidArgument
	}
	err := r.data.TenantTx(ctx, in.TenantID, func(s *TenantTx) error {
		return s.Q().InsertSysLoginLog(ctx, gen.InsertSysLoginLogParams{
			UserID:        in.UserID,
			Username:      in.Username,
			TenantID:      in.TenantID,
			IpAddress:     in.IPAddress,
			UserAgent:     in.UserAgent,
			LoginType:     in.LoginType,
			IsSuccess:     in.IsSuccess,
			FailureReason: in.FailureReason,
			CreatedBy:     in.UserID,
		})
	})
	if err != nil {
		return r.mapErr(err)
	}
	return nil
}

// GetOperationLogByID 按主键读取操作日志。租户边界由 RLS 承担，跨租户等同不存在。
func (r *auditLogRepo) GetOperationLogByID(ctx context.Context, auth biz.Identity, id int64) (*biz.OperationLog, error) {
	var out *biz.OperationLog
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysOperationLogByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizOperationLog(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListOperationLogs 解析动态查询协议并返回分页信封，口径同 ListLoginLogs。
func (r *auditLogRepo) ListOperationLogs(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.OperationLogList, error) {
	var out *biz.OperationLogList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysOperationLogQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysOperationLog(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.OperationLogList{
			Items:      make([]*biz.OperationLog, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizOperationLog(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysOperationLog(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// InsertOperationLog 追加一条操作日志。身份已成立，经 IdentityTx 注入
// 租户并取认证用户作为操作者；target/snapshot 等未采集字段按 nil 落
// NULL。
func (r *auditLogRepo) InsertOperationLog(ctx context.Context, auth biz.Identity, in *biz.OperationLogInput) error {
	if in == nil {
		return biz.ErrAuditLogInvalidArgument
	}
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		return s.Q().InsertSysOperationLog(ctx, gen.InsertSysOperationLogParams{
			UserID:        auth.UserID,
			Username:      in.Username,
			TenantID:      auth.TenantID,
			OperationType: in.OperationType,
			TargetTable:   in.TargetTable,
			TargetID:      in.TargetID,
			OldValue:      in.OldValue,
			NewValue:      in.NewValue,
			IpAddress:     in.IPAddress,
			UserAgent:     in.UserAgent,
			ApiPath:       in.APIPath,
			HttpMethod:    in.HTTPMethod,
			DurationMs:    in.DurationMs,
			ResultCode:    in.ResultCode,
			ResultMsg:     in.ResultMsg,
			RequestID:     in.RequestID,
			CreatedBy:     auth.UserID,
		})
	})
	if err != nil {
		return r.mapErr(err)
	}
	return nil
}

// mapErr 将驱动与协议错误映射为审计日志的领域错误：无行（含跨租户等同
// 不存在）→ NotFound；dynq 协议错误 → InvalidArgument（保留协议原因）；
// 其余原样透传。不走 mapStorageError：两表 append-only 只读，没有唯一
// 冲突路径，不需要 alreadyExists 目标。
func (r *auditLogRepo) mapErr(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return biz.ErrAuditLogNotFound
	case isDynqError(err):
		be := biz.ErrAuditLogInvalidArgument
		return kratoserrors.New(int(be.Code), be.Reason, dynq.AsError(err).Reason)
	default:
		return err
	}
}
