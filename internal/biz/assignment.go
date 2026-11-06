package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	v1 "grape/api/user/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// ErrUserRoleSetChanged 表示目标用户的角色集合在加载后已被他人改动：
// expected_version 与当前可管理集合不一致，调用方应重载后重新提交
// （Conflict 409）；不与「不可分配」的 403 混用。
var ErrUserRoleSetChanged = kratoserrors.Conflict(v1.ErrorReason_USER_ROLE_SET_CHANGED.String(), "user role set changed")

// 选项查询的缺省与上限；入参越界由 NormalizeOptionQuery 归一化。
const (
	OptionDefaultPageSize int32 = 20
	OptionMaxPageSize     int32 = 100
)

// Option 是选择器候选项的领域模型。Value 是来源对象的 int64 ID；Enabled
// 与 Reason 是前向兼容字段（v1 恒 Enabled=true、Reason 为空），Assigned
// 只用于角色场景（目标用户已拥有）。
type Option struct {
	Value    int64
	Label    string
	Enabled  bool
	Reason   string
	Assigned bool
}

// OptionQuery 是可搜索选项来源的查询入参。
type OptionQuery struct {
	Keyword  string
	Page     int32
	PageSize int32
}

// OptionSet 是选项来源的一页结果；Total v1 不返回（nil）。
type OptionSet struct {
	Items   []*Option
	HasMore bool
	Total   *int64
	Page    int32
}

// UserRoleSet 是目标用户当前可管理角色集合与版本。UnmanagedCount 是
// locked 角色数量（不在调用方管理范围的既有角色），不暴露明细。
type UserRoleSet struct {
	Items          []*Option
	UnmanagedCount int64
	Version        string
}

// SetUserRolesInput 是「管理范围内整组替换」的入参：RoleIDs 只表达
// 调用方可管理范围内的目标集合，不得包含 locked 角色；ExpectedVersion
// 来自 ListUserRoles，必填。
type SetUserRolesInput struct {
	UserID          int64
	RoleIDs         []int64
	ExpectedVersion string
}

// AssignmentRepo 是用户分配选择器与角色整组替换的仓储接口。写路径的
// 判定与写入在同一 IdentityTx 内完成；所有方法经会话门面在租户事务内
// 执行（RLS）。
type AssignmentRepo interface {
	// ListAssignableRoles 返回目标用户可分配、尚未拥有的活跃角色；
	// 目标不存在/软删返回 ErrUserNotFound（分配不要求目标可见，不可见
	// 仍可分配）。
	ListAssignableRoles(ctx context.Context, auth Identity, targetUserID int64, q OptionQuery) (*OptionSet, error)
	// ListUserRoles 返回目标用户当前可管理角色集合、locked 计数与版本；
	// 目标不存在/软删返回 ErrUserNotFound（分配读面不要求目标可见）。
	ListUserRoles(ctx context.Context, auth Identity, userID int64) (*UserRoleSet, error)
	// SetUserRoles 在单个事务内按「管理范围内整组替换」写入角色集合：
	// locked 原样保留、请求触及 locked/不可分配/不可撤销/版本不一致/
	// 自锁一律整体拒绝（错误语义见 permission.md）。
	SetUserRoles(ctx context.Context, auth Identity, in SetUserRolesInput) (*UserRoleSet, error)
	// ListOwnerUserOptions 返回操作者可见范围内的活跃用户候选。
	ListOwnerUserOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error)
	// ListOwnerDeptOptions 返回操作者可见范围内的活跃部门候选。
	ListOwnerDeptOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error)
	// ListOwnerGroupOptions 返回操作者可见范围内的活跃用户组候选。
	ListOwnerGroupOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error)
}

// AssignmentUsecase 是用户分配选择器与角色整组替换的用例：入参校验与分页
// 归一化在这里落地，权限判定与事务编排在仓储内（库内函数）。
type AssignmentUsecase struct {
	repo AssignmentRepo
}

// NewAssignmentUsecase 构造用户分配用例。
func NewAssignmentUsecase(repo AssignmentRepo) *AssignmentUsecase {
	return &AssignmentUsecase{repo: repo}
}

// ListAssignableRoles 列出目标用户可分配且尚未拥有的活跃角色。
func (uc *AssignmentUsecase) ListAssignableRoles(ctx context.Context, auth Identity, targetUserID int64, q OptionQuery) (*OptionSet, error) {
	if targetUserID <= 0 {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.ListAssignableRoles(ctx, auth, targetUserID, NormalizeOptionQuery(q))
}

// ListUserRoles 列出目标用户当前可管理角色集合与版本。
func (uc *AssignmentUsecase) ListUserRoles(ctx context.Context, auth Identity, userID int64) (*UserRoleSet, error) {
	if userID <= 0 {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.ListUserRoles(ctx, auth, userID)
}

// SetUserRoles 用「管理范围内整组替换」写入角色集合：角色 ID 去重升序、
// 全部为正；expected_version 必填（空串按参数非法处理，版本不一致由仓储
// 返回 ErrUserRoleSetChanged）。
func (uc *AssignmentUsecase) SetUserRoles(ctx context.Context, auth Identity, in SetUserRolesInput) (*UserRoleSet, error) {
	if in.UserID <= 0 || strings.TrimSpace(in.ExpectedVersion) == "" {
		return nil, ErrUserInvalidArgument
	}
	roles := slices.Clone(in.RoleIDs)
	slices.Sort(roles)
	roles = slices.Compact(roles)
	for _, id := range roles {
		if id <= 0 {
			return nil, ErrUserInvalidArgument
		}
	}
	in.RoleIDs = roles
	return uc.repo.SetUserRoles(ctx, auth, in)
}

// ListOwnerUserOptions 列出归属用户候选（操作者可见范围内的活跃用户）。
func (uc *AssignmentUsecase) ListOwnerUserOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error) {
	return uc.repo.ListOwnerUserOptions(ctx, auth, NormalizeOptionQuery(q))
}

// ListOwnerDeptOptions 列出归属部门候选（操作者可见范围内的活跃部门）。
func (uc *AssignmentUsecase) ListOwnerDeptOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error) {
	return uc.repo.ListOwnerDeptOptions(ctx, auth, NormalizeOptionQuery(q))
}

// ListOwnerGroupOptions 列出归属用户组候选（操作者可见范围内的活跃组）。
func (uc *AssignmentUsecase) ListOwnerGroupOptions(ctx context.Context, auth Identity, q OptionQuery) (*OptionSet, error) {
	return uc.repo.ListOwnerGroupOptions(ctx, auth, NormalizeOptionQuery(q))
}

// NormalizeOptionQuery 归一化选项查询：去首尾空白的关键字（空白等同于
// 不过滤）、页码下限 1、页大小缺省 20 与上限 100（对齐成员列表先例）。
func NormalizeOptionQuery(q OptionQuery) OptionQuery {
	q.Keyword = strings.TrimSpace(q.Keyword)
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = OptionDefaultPageSize
	}
	if q.PageSize > OptionMaxPageSize {
		q.PageSize = OptionMaxPageSize
	}
	return q
}

// UserRoleSetVersion 计算角色集合版本：sha256("<user_id>|<升序去重可管理
// ID 逗号列表>") 的十六进制。ListUserRoles 与 SetUserRoles 必须共用本
// helper，避免两侧口径漂移。
func UserRoleSetVersion(userID int64, roleIDs []int64) string {
	sorted := slices.Clone(roleIDs)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	parts := make([]string, 0, len(sorted))
	for _, id := range sorted {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s", userID, strings.Join(parts, ","))))
	return hex.EncodeToString(sum[:])
}
