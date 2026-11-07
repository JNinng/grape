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

// LoginLogInput 是追加登录日志的入参。登录用例在身份成立前调用：
// TenantID 是登录目标租户；UserID/CreatedBy 在目标用户不可定位时为 0
// （登录失败且用户不存在/不可见），成功与口令错误等已知用户场景为真实
// 用户 ID。UserAgent 取客户端声明的设备信息。
type LoginLogInput struct {
	TenantID      string
	UserID        int64
	Username      string
	IPAddress     *string
	UserAgent     *string
	LoginType     string
	IsSuccess     bool
	FailureReason *string
}

// OperationLogInput 是追加操作日志的入参。身份（租户/操作者）由调用方
// 的 Identity 提供，本结构只承载操作事实；未采集的快照/目标 ID 保持
// nil/空，落库为 NULL。
type OperationLogInput struct {
	Username      string
	OperationType string
	TargetTable   *string
	TargetID      *string
	OldValue      json.RawMessage
	NewValue      json.RawMessage
	IPAddress     *string
	UserAgent     *string
	APIPath       *string
	HTTPMethod    *string
	DurationMs    *int32
	ResultCode    *string
	ResultMsg     *string
	RequestID     *string
}

// AuditLogRepo 是审计日志仓储接口：登录日志与操作日志两表共用。
// 读路径经存储层会话门面在租户事务内执行（RLS）；写路径是 append-only
// 追加：登录日志在身份成立前经 TenantTx（审计列由入参给出），操作日志
// 在身份成立后经 IdentityTx（审计列取认证身份）。List 消费 dynq 动态
// 查询协议。
type AuditLogRepo interface {
	// GetLoginLogByID 按 id 查询单条登录日志；不存在或跨租户返回
	// ErrAuditLogNotFound。
	GetLoginLogByID(ctx context.Context, auth Identity, id int64) (*LoginLog, error)
	// ListLoginLogs 按动态查询协议返回分页集合（租户内平铺视图）。
	ListLoginLogs(ctx context.Context, auth Identity, q *dynq.Query) (*LoginLogList, error)
	// InsertLoginLog 追加一条登录日志（身份成立前，TenantTx + 显式
	// actor 列）。
	InsertLoginLog(ctx context.Context, in *LoginLogInput) error
	// GetOperationLogByID 按 id 查询单条操作日志；不存在或跨租户返回
	// ErrAuditLogNotFound。
	GetOperationLogByID(ctx context.Context, auth Identity, id int64) (*OperationLog, error)
	// ListOperationLogs 按动态查询协议返回分页集合（租户内平铺视图）。
	ListOperationLogs(ctx context.Context, auth Identity, q *dynq.Query) (*OperationLogList, error)
	// InsertOperationLog 追加一条操作日志（身份成立后，IdentityTx）。
	InsertOperationLog(ctx context.Context, auth Identity, in *OperationLogInput) error
}

// AuditLogUsecase 是审计日志用例：读路径无业务规则可施加，入参校验
// （ID 非正 fail-fast）之后转发仓储，协议合法性由生成代码校验；写路径
// 只做 append-only 追加，写入失败由调用方按 best-effort 处理（不得反向
// 阻断登录或业务操作）。
type AuditLogUsecase struct {
	repo AuditLogRepo
}

// NewAuditLogUsecase 构造审计日志用例。
func NewAuditLogUsecase(repo AuditLogRepo) *AuditLogUsecase {
	return &AuditLogUsecase{repo: repo}
}

// RecordLogin 追加一条登录日志。租户与用户名是登录尝试的最小定位事实；
// login_type 缺省 password。
func (uc *AuditLogUsecase) RecordLogin(ctx context.Context, in *LoginLogInput) error {
	if in == nil || in.TenantID == "" || in.Username == "" {
		return ErrAuditLogInvalidArgument
	}
	if in.LoginType == "" {
		in.LoginType = "password"
	}
	return uc.repo.InsertLoginLog(ctx, in)
}

// RecordOperation 追加一条操作日志。身份必须成立（租户非空、用户 ID
// 为正）；用户名允许为空（认证身份未携带冗余用户名时由调用方尽力补齐）。
func (uc *AuditLogUsecase) RecordOperation(ctx context.Context, auth Identity, in *OperationLogInput) error {
	if in == nil || auth.TenantID == "" || auth.UserID <= 0 || in.OperationType == "" {
		return ErrAuditLogInvalidArgument
	}
	return uc.repo.InsertOperationLog(ctx, auth, in)
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
