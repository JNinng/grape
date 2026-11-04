package biz

import (
	"context"
	"encoding/json"
	"time"

	v1 "grape/api/audit/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrAuditLogNotFound 表示目标日志行不存在（含跨租户不可见：RLS 之外等同不存在）。
	ErrAuditLogNotFound = kratoserrors.NotFound(v1.ErrorReason_AUDIT_LOG_NOT_FOUND.String(), "audit log not found")
	// ErrAuditLogInvalidArgument 表示审计日志查询请求参数非法（动态查询协议非法、ID 非正等）。
	ErrAuditLogInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_AUDIT_LOG_INVALID_ARGUMENT.String(), "invalid audit log argument")
)

// LoginLog 是登录日志领域模型。登录日志与操作日志均为追加写入
// （append-only）：只有读取面，无更新/删除口径，模型即写入时的只读快照。
// 两表是租户内公共数据：租户边界由存储层 RLS 承担，表无 @scope（租户内
// 全可见），故仓储方法不携带 Scope。
type LoginLog struct {
	ID            int64
	UserID        int64
	Username      string
	IPAddress     *string // 可空
	UserAgent     *string // 可空
	LoginType     string
	IsSuccess     bool
	FailureReason *string // 可空
	LoginAt       time.Time
	CreatedBy     int64
	CreatedAt     time.Time
}

// LoginLogList 是登录日志仓储 List 返回的分页信封。
type LoginLogList struct {
	Items      []*LoginLog
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// OperationLog 是操作日志领域模型。操作前后数据快照为 JSON 对象（写入时
// 即校验合法性），以 json.RawMessage 贯穿全程、原样透传，不中途换形。
type OperationLog struct {
	ID            int64
	UserID        int64
	Username      string
	OperationType string
	TargetTable   *string // 可空
	TargetID      *string // 可空
	OldValue      json.RawMessage
	NewValue      json.RawMessage
	IPAddress     *string // 可空
	UserAgent     *string // 可空
	APIPath       *string // 可空
	HTTPMethod    *string // 可空
	DurationMs    *int32  // 可空
	ResultCode    *string // 可空
	ResultMsg     *string // 可空
	RequestID     *string // 可空
	OperatedAt    time.Time
	CreatedBy     int64
	CreatedAt     time.Time
}

// OperationLogList 是操作日志仓储 List 返回的分页信封。
type OperationLogList struct {
	Items      []*OperationLog
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// AuditLogRepo 是审计日志只读仓储接口：登录日志与操作日志两表共用。
// 全部实现必须经存储层会话门面在租户事务内执行（RLS）。List 消费 dynq
// 动态查询协议。
type AuditLogRepo interface {
	// GetLoginLogByID 按 id 查询单条登录日志；不存在或跨租户返回
	// ErrAuditLogNotFound。
	GetLoginLogByID(ctx context.Context, auth Identity, id int64) (*LoginLog, error)
	// ListLoginLogs 按动态查询协议返回分页集合（租户内平铺视图）。
	ListLoginLogs(ctx context.Context, auth Identity, q *dynq.Query) (*LoginLogList, error)
	// GetOperationLogByID 按 id 查询单条操作日志；不存在或跨租户返回
	// ErrAuditLogNotFound。
	GetOperationLogByID(ctx context.Context, auth Identity, id int64) (*OperationLog, error)
	// ListOperationLogs 按动态查询协议返回分页集合（租户内平铺视图）。
	ListOperationLogs(ctx context.Context, auth Identity, q *dynq.Query) (*OperationLogList, error)
}

// AuditLogUsecase 是审计日志只读查询用例：无业务规则可施加，入参校验
// （ID 非正 fail-fast）之后转发仓储，协议合法性由生成代码校验。
type AuditLogUsecase struct {
	repo AuditLogRepo
}

// NewAuditLogUsecase 构造审计日志查询用例。
func NewAuditLogUsecase(repo AuditLogRepo) *AuditLogUsecase {
	return &AuditLogUsecase{repo: repo}
}

// GetLoginLog 按 id 查询登录日志。
func (uc *AuditLogUsecase) GetLoginLog(ctx context.Context, auth Identity, id int64) (*LoginLog, error) {
	if id <= 0 {
		return nil, ErrAuditLogInvalidArgument
	}
	return uc.repo.GetLoginLogByID(ctx, auth, id)
}

// ListLoginLogs 经动态查询协议列出登录日志（平铺分页视图）。
func (uc *AuditLogUsecase) ListLoginLogs(ctx context.Context, auth Identity, q *dynq.Query) (*LoginLogList, error) {
	return uc.repo.ListLoginLogs(ctx, auth, q)
}

// GetOperationLog 按 id 查询操作日志。
func (uc *AuditLogUsecase) GetOperationLog(ctx context.Context, auth Identity, id int64) (*OperationLog, error) {
	if id <= 0 {
		return nil, ErrAuditLogInvalidArgument
	}
	return uc.repo.GetOperationLogByID(ctx, auth, id)
}

// ListOperationLogs 经动态查询协议列出操作日志（平铺分页视图）。
func (uc *AuditLogUsecase) ListOperationLogs(ctx context.Context, auth Identity, q *dynq.Query) (*OperationLogList, error) {
	return uc.repo.ListOperationLogs(ctx, auth, q)
}
