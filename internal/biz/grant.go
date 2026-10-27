package biz

import (
	"context"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// Agent 工具授权的错误语义：
//   - ErrGrantNotFound：授权不存在、不可见（跨租户）或已吊销——三者统一
//     NotFound，不泄露存在性；
//   - ErrGrantInvalidArgument：授权写入/查询的入参非法；
//   - ErrGrantAccessDenied：功能权限码不通过（全租户列表、吊销他人授权）
//     ——存在性不敏感的明确拒绝（403）。
var (
	ErrGrantNotFound        = kratoserrors.NotFound("AGENT_TOOL_GRANT_NOT_FOUND", "agent tool grant not found")
	ErrGrantInvalidArgument = kratoserrors.BadRequest("AGENT_TOOL_GRANT_INVALID_ARGUMENT", "invalid agent tool grant argument")
	ErrGrantAccessDenied    = kratoserrors.Forbidden("AGENT_TOOL_GRANT_ACCESS_DENIED", "agent tool grant access denied")
)

// Agent 工具授权管理的功能权限码（种子菜单 252/253，角色经菜单关联获
// 得）。本人授权列表与本人吊销不设码——认证身份即凭据；跨人视角才验码。
// reason 字符串暂不进 proto ErrorReason 枚举：管理端点为手写 HTTP 形态，
// 走 proto 化（ErrorReason + openapi）时随该批变更补枚举。
const (
	// PermAgentGrantQuery 放行全租户授权列表（scope=tenant）。
	PermAgentGrantQuery = "agent:grant:query"
	// PermAgentGrantRevoke 放行吊销他人授权。
	PermAgentGrantRevoke = "agent:grant:revoke"
)

// ToolGrant 是工具授权的领域模型：用户对某工具的预先授权，满足 ack
// confirm 层级的确认要求（强制确认的策略判定不可被许可满足）。归属
// （TenantID/UserID）由仓储从认证 ctx 提取，不经调用方携带。
type ToolGrant struct {
	ID int64
	// TenantID / UserID 是许可归属（查询键前两元）。
	TenantID string
	UserID   int64
	// ToolName 是工具名（与 ack 分级表、工具集登记名一致）。
	ToolName string
	// SessionID 是绑定的会话（AG-UI threadId）；nil 即全局级——对该
	// 用户全部会话生效。
	SessionID *string
	// CreatedBy 是授权来源操作者（确认决定的 DecidedBy，正常流即属主
	// 本人）；RevokedAt 非空即已吊销，行保留作审计现场。
	CreatedBy int64
	CreatedAt time.Time
	RevokedBy *int64
	RevokedAt *time.Time
}

// GrantRepo 是工具授权仓储接口。全部实现必须经存储层会话门面在租户
// 事务内执行（RLS），且每条查询显式携带认证租户过滤（RLS 关闭场景的
// 应用层兜底，与向量层隔离口径一致）。
//
// 许可评估原语（FindActive*）的未命中是正常控制流而非错误：miss 以
// (nil, nil) 表达——ack 的许可评估本就以布尔命中为语义，评估路径上
// 「没有许可」与「有许可」同等寻常。按 id 定位的管理原语（GetByID/
// Revoke*）的未命中才是错误，返回 ErrGrantNotFound。
type GrantRepo interface {
	// FindActiveForSession 按会话级查活跃许可；miss 返回 (nil, nil)。
	FindActiveForSession(ctx context.Context, auth Identity, userID int64, sessionID, toolName string) (*ToolGrant, error)
	// FindActiveGlobal 按全局级查活跃许可；miss 返回 (nil, nil)。
	FindActiveGlobal(ctx context.Context, auth Identity, userID int64, toolName string) (*ToolGrant, error)
	// Create 写入一条授权；同键已存在活跃许可时幂等跳过并返回 false
	// （许可语义是布尔存在而非计数，先到决定生效，不覆盖不报错）。
	Create(ctx context.Context, auth Identity, g *ToolGrant) (bool, error)
	// GetByID 按租户内 id 取授权行（含已吊销，供吊销前归属判定）；
	// 不存在或跨租户返回 ErrGrantNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*ToolGrant, error)
	// ListActiveByUser 列出指定用户的活跃授权（本人视图）。
	ListActiveByUser(ctx context.Context, auth Identity, userID int64) ([]*ToolGrant, error)
	// ListActiveByTenant 列出租户内全部用户的活跃授权（管理视图）。
	ListActiveByTenant(ctx context.Context, auth Identity) ([]*ToolGrant, error)
	// RevokeOwn 属主吊销：id + 归属人双定位，原子置 revoked_at；未命中
	// （非本人/已吊销/不存在）返回 ErrGrantNotFound。
	RevokeOwn(ctx context.Context, auth Identity, id int64) error
	// RevokeManaged 管理吊销：调用方先验 agent:grant:revoke 权限码
	// （见 GrantUsecase.Revoke）；未命中（不存在/已吊销）返回
	// ErrGrantNotFound。
	RevokeManaged(ctx context.Context, auth Identity, id int64) error
	// HasPermission 判断用户是否持有功能权限码（fn_has_permission，
	// 角色→菜单→权限码）。
	HasPermission(ctx context.Context, auth Identity, code string) (bool, error)
}

// GrantUsecase 是工具授权用例：ack 许可源的领域入口与授权管理 REST 的
// 业务面。许可评估路径（Resolve/Remember）由 agent 适配层消费；管理路
// 径（ListOwn/ListTenant/Revoke）由手写端点消费。
type GrantUsecase struct {
	repo GrantRepo
}

// NewGrantUsecase 构造工具授权用例。
func NewGrantUsecase(repo GrantRepo) *GrantUsecase {
	return &GrantUsecase{repo: repo}
}

// Resolve 查询用户的活跃许可：会话级先于全局级（ack GrantSource 的
// 既有排序语义，此处显式编排）——会话级命中即返回，未命中才落全局级；
// 空会话标识直接查全局级。两级都未命中返回 (nil, nil)，调用方按未命中
// 处理（确认照常发起，miss 是正常控制流，见 GrantRepo 注释）。
func (uc *GrantUsecase) Resolve(ctx context.Context, auth Identity, userID int64, sessionID, toolName string) (*ToolGrant, error) {
	if sessionID != "" {
		g, err := uc.repo.FindActiveForSession(ctx, auth, userID, sessionID, toolName)
		if err != nil {
			return nil, err
		}
		if g != nil {
			return g, nil
		}
	}
	return uc.repo.FindActiveGlobal(ctx, auth, userID, toolName)
}

// Remember 落库一条授权（ack 同意决定 + remember 的持久化形态）：
// sessionID 非空即会话级、空即全局级。同键活跃许可已存在时幂等跳过
// （首个决定生效，见 GrantRepo.Create）。CreatedBy 取操作者本人——
// 归属守卫保证确认决定者即会话属主，许可归属与授权来源一致。
func (uc *GrantUsecase) Remember(ctx context.Context, auth Identity, sessionID, toolName string) error {
	if toolName == "" {
		return ErrGrantInvalidArgument
	}
	g := &ToolGrant{
		TenantID:  auth.TenantID,
		UserID:    auth.UserID,
		ToolName:  toolName,
		CreatedBy: auth.UserID,
	}
	if sessionID != "" {
		g.SessionID = &sessionID
	}
	_, err := uc.repo.Create(ctx, auth, g)
	return err
}

// ListOwn 列出当前认证用户的活跃授权（本人视图，不设权限码）。
func (uc *GrantUsecase) ListOwn(ctx context.Context, auth Identity) ([]*ToolGrant, error) {
	return uc.repo.ListActiveByUser(ctx, auth, auth.UserID)
}

// ListTenant 列出租户内全部用户的活跃授权（管理视图）：功能权限码
// agent:grant:query 不通过即 ErrGrantAccessDenied（403）。判定在用例层
// 完成（入口的直接受托方），仓储方法不携带权限参数——功能权限码不进
// 仓储（permission.md 的落点约束）。
func (uc *GrantUsecase) ListTenant(ctx context.Context, auth Identity) ([]*ToolGrant, error) {
	ok, err := uc.repo.HasPermission(ctx, auth, PermAgentGrantQuery)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrGrantAccessDenied
	}
	return uc.repo.ListActiveByTenant(ctx, auth)
}

// Revoke 吊销授权：属主本人吊销自己的授权不设码；吊销他人的授权要求
// 功能权限码 agent:grant:revoke，不通过即 ErrGrantAccessDenied（403）。
// 目标不可见（跨租户）等同不存在返回 ErrGrantNotFound——先取行判归属
// 再走对应仓储原语；并发下授权恰已被吊销时仓储以 0 行落
// ErrGrantNotFound（幂等，不报错）。
// 存在性语义（有意取舍）：无码调用方对「存在但他人」的 id 得 403、对
// 「不存在」的 id 得 404——同租户内授权 id 的存在性可被枚举（id 含
// 时间戳混合随机，不可预测、无内容泄露）；如需存在性不敏感，将无码
// 调用方收敛到 RevokeOwn 的 0 行统一 404 口径即可。
func (uc *GrantUsecase) Revoke(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrGrantInvalidArgument
	}
	g, err := uc.repo.GetByID(ctx, auth, id)
	if err != nil {
		return err
	}
	if g.UserID == auth.UserID {
		return uc.repo.RevokeOwn(ctx, auth, id)
	}
	ok, err := uc.repo.HasPermission(ctx, auth, PermAgentGrantRevoke)
	if err != nil {
		return err
	}
	if !ok {
		return ErrGrantAccessDenied
	}
	return uc.repo.RevokeManaged(ctx, auth, id)
}

// HasPermission 判断用户是否持有指定功能权限码，供入口层做需要 403
// 语义的前置判定。
func (uc *GrantUsecase) HasPermission(ctx context.Context, auth Identity, code string) (bool, error) {
	return uc.repo.HasPermission(ctx, auth, code)
}
