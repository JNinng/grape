package biz

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/org/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrUserGroupNotFound 表示目标用户组不存在（含跨租户不可见：RLS 之外等同不存在）。
	ErrUserGroupNotFound = kratoserrors.NotFound(v1.ErrorReason_USER_GROUP_NOT_FOUND.String(), "user group not found")
	// ErrUserGroupInvalidArgument 表示用户组请求参数非法（成员引用未知用户等）。
	ErrUserGroupInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_USER_GROUP_INVALID_ARGUMENT.String(), "invalid user group argument")
	// ErrUserGroupAlreadyExists 表示租户内组编码/组名唯一约束冲突（未删除用户组间）。
	ErrUserGroupAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_USER_GROUP_ALREADY_EXISTS.String(), "user group already exists")
)

// UserGroup 是用户组领域模型。用户组是租户内资源：租户边界由存储层 RLS
// 承担，表无 @scope（租户内全可见），故仓储方法不携带 Scope。code 是
// 身份列（创建后不变）、owner_user_id 是归属列，不经 Update 用例修改。
// 成员映射 sys_user_group_mapping 不单独立资源，随本资源的成员用例维护。
type UserGroup struct {
	ID          int64
	Status      int32 // 0 禁用、1 启用
	Name        string
	Code        string
	Remark      *string // 可空
	OwnerUserID *int64  // 组长/归属用户，可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// UserGroupList 是用户组仓储 List 返回的分页信封。
type UserGroupList struct {
	Items      []*UserGroup
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// UserGroupMember 是组成员条目：映射行与成员用户展示字段的联结视图。
// 映射行无可变业务字段，移除成员走物理删除。
type UserGroupMember struct {
	UserID   int64
	Name     string // 成员用户名（sys_user.name）
	Nickname string
	Status   int32 // 成员用户数据状态：0 禁用、1 启用

	AddedBy int64
	AddedAt time.Time
}

// UserGroupMemberList 是成员列表的分页信封。
type UserGroupMemberList struct {
	Items []*UserGroupMember
	Total int64
}

// UserGroupRepo 是用户组仓储接口。全部实现必须经存储层会话门面在租户
// 事务内执行（RLS）；成员写路径先锁组行再操作映射表。List 消费 dynq
// 动态查询协议。
type UserGroupRepo interface {
	// GetByID 按 id 查询单个用户组；不存在或跨租户返回 ErrUserGroupNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*UserGroup, error)
	// List 按动态查询协议返回分页集合（租户内平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*UserGroupList, error)
	// Create 创建用户组；组编码/组名冲突映射 ErrUserGroupAlreadyExists。
	Create(ctx context.Context, auth Identity, g *UserGroup) (*UserGroup, error)
	// Update 覆盖可变字段（name、status、remark；不含 code 与 owner_user_id）。
	Update(ctx context.Context, auth Identity, g *UserGroup) (*UserGroup, error)
	// SoftDelete 软删除用户组（deleted_at 置位）。成员映射行物理残留：
	// 映射表无外键级联，权限推导与成员列表都以组表/用户表活跃过滤联结，
	// 残留行不产生可见副作用。
	SoftDelete(ctx context.Context, auth Identity, id int64) error
	// ListMembers 分页返回组成员（联结用户表展示字段）；组不存在或跨租户
	// 返回 ErrUserGroupNotFound。已软删用户不出现在结果中。
	ListMembers(ctx context.Context, auth Identity, groupID int64, page, pageSize int) (*UserGroupMemberList, error)
	// AddMembers 批量添加成员：目标用户必须存在且未删除，任一未知返回
	// ErrUserGroupInvalidArgument 且整批不写入；已是成员的用户跳过，
	// 返回实际新增条数。
	AddMembers(ctx context.Context, auth Identity, groupID int64, userIDs []int64) (int64, error)
	// RemoveMembers 批量移除成员（物理删除映射行）；组不存在或跨租户
	// 返回 ErrUserGroupNotFound，移除非成员不算错误，返回实际移除条数。
	RemoveMembers(ctx context.Context, auth Identity, groupID int64, userIDs []int64) (int64, error)
}

// UserGroupUsecase 是用户组用例。
type UserGroupUsecase struct {
	repo UserGroupRepo
}

// NewUserGroupUsecase 构造用户组用例。
func NewUserGroupUsecase(repo UserGroupRepo) *UserGroupUsecase {
	return &UserGroupUsecase{repo: repo}
}

// GetUserGroup 按 id 查询用户组。
func (uc *UserGroupUsecase) GetUserGroup(ctx context.Context, auth Identity, id int64) (*UserGroup, error) {
	if id <= 0 {
		return nil, ErrUserGroupInvalidArgument
	}
	return uc.repo.GetByID(ctx, auth, id)
}

// ListUserGroups 经动态查询协议列出用户组（平铺分页视图）。
func (uc *UserGroupUsecase) ListUserGroups(ctx context.Context, auth Identity, q *dynq.Query) (*UserGroupList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateUserGroup 创建用户组：status 未指定（哨兵）缺省启用、显式 0 原样
// 落库为禁用，组长可省略。
func (uc *UserGroupUsecase) CreateUserGroup(ctx context.Context, auth Identity, g *UserGroup) (*UserGroup, error) {
	if err := validateUserGroup(g, false); err != nil {
		return nil, err
	}
	if g.Status == UserGroupStatusUnspecified {
		g.Status = UserGroupStatusEnabled
	}
	return uc.repo.Create(ctx, auth, g)
}

// UpdateUserGroup 覆盖可变字段（name、status、remark）。code 与
// owner_user_id 由服务端管理：组编码创建后不变，组长变更走专项口径。
func (uc *UserGroupUsecase) UpdateUserGroup(ctx context.Context, auth Identity, g *UserGroup) (*UserGroup, error) {
	if g == nil || g.ID <= 0 {
		return nil, ErrUserGroupInvalidArgument
	}
	if err := validateUserGroup(g, true); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, auth, g)
}

// DeleteUserGroup 软删除用户组；成员映射行物理残留并被活跃过滤排除。
func (uc *UserGroupUsecase) DeleteUserGroup(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrUserGroupInvalidArgument
	}
	return uc.repo.SoftDelete(ctx, auth, id)
}

// ListUserGroupMembers 分页返回组成员；入参页码与页大小归一化（页码
// 小于 1 取 1，页大小小于 1 取 20、大于上限取上限）。
func (uc *UserGroupUsecase) ListUserGroupMembers(ctx context.Context, auth Identity, groupID int64, page, pageSize int) (*UserGroupMemberList, error) {
	if groupID <= 0 {
		return nil, ErrUserGroupInvalidArgument
	}
	return uc.repo.ListMembers(ctx, auth, groupID, normalizePage(page), normalizePageSize(pageSize))
}

// AddUserGroupMembers 批量添加成员：入参去重排序，保证存在性判定与落库
// 稳定。
func (uc *UserGroupUsecase) AddUserGroupMembers(ctx context.Context, auth Identity, groupID int64, userIDs []int64) (int64, error) {
	if groupID <= 0 || len(userIDs) == 0 {
		return 0, ErrUserGroupInvalidArgument
	}
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return uc.repo.AddMembers(ctx, auth, groupID, ids)
}

// RemoveUserGroupMembers 批量移除成员：入参去重排序；移除非成员不算错误，
// 返回实际移除条数。
func (uc *UserGroupUsecase) RemoveUserGroupMembers(ctx context.Context, auth Identity, groupID int64, userIDs []int64) (int64, error) {
	if groupID <= 0 || len(userIDs) == 0 {
		return 0, ErrUserGroupInvalidArgument
	}
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return uc.repo.RemoveMembers(ctx, auth, groupID, ids)
}

// UserGroupStatusEnabled / UserGroupStatusDisabled / UserGroupStatusUnspecified
// 与存储层 sys_user_group.status 取值对齐：-1 只是创建入参的「未指定」
// 哨兵（optional int32 的缺失必须与显式 0=禁用可区分），不落库；用例把
// 未指定落为缺省启用，显式 0 原样落库为禁用。
const (
	UserGroupStatusDisabled    int32 = 0
	UserGroupStatusEnabled     int32 = 1
	UserGroupStatusUnspecified int32 = -1
)

// 用户组列宽约束，与 sys_user_group 的 name/code/remark 列宽对齐。
const (
	userGroupNameMaxRunes   = 256 // varchar(256)
	userGroupCodeMaxRunes   = 64  // varchar(64)
	userGroupRemarkMaxRunes = 512 // varchar(512)
)

// 成员分页缺省与上限：映射表无动态查询协议，固定 offset 分页取全仓动态
// 协议的插件级缺省页大小与上限（sqlc.yaml 的 page_default/page_max）。
const (
	UserGroupMemberPageDefault = 20
	UserGroupMemberPageMax     = 200
)

// normalizePage 归一化页码：小于 1 取 1。
func normalizePage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

// normalizePageSize 归一化页大小：小于 1 取缺省，大于上限取上限。
func normalizePageSize(pageSize int) int {
	if pageSize < 1 {
		return UserGroupMemberPageDefault
	}
	if pageSize > UserGroupMemberPageMax {
		return UserGroupMemberPageMax
	}
	return pageSize
}

// validateUserGroup 校验用户组可变字段：name 必填且不超列宽、remark 不超
// 列宽、status 合法（创建路径额外接受未指定哨兵，缺省由用例落启用；更新
// 路径必填，只认 0/1）、组长创建时若携带必须为正 ID。code 是身份列：仅
// 创建路径必填且不超列宽（更新输入不携带，仓储不写该列）。
func validateUserGroup(g *UserGroup, update bool) error {
	if g == nil ||
		strings.TrimSpace(g.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(g.Name)) > userGroupNameMaxRunes ||
		(g.Remark != nil && utf8.RuneCountInString(*g.Remark) > userGroupRemarkMaxRunes) {
		return ErrUserGroupInvalidArgument
	}
	if !update && (strings.TrimSpace(g.Code) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(g.Code)) > userGroupCodeMaxRunes) {
		return ErrUserGroupInvalidArgument
	}
	switch {
	case update:
		if g.Status != UserGroupStatusDisabled && g.Status != UserGroupStatusEnabled {
			return ErrUserGroupInvalidArgument
		}
	case g.Status != UserGroupStatusUnspecified &&
		g.Status != UserGroupStatusDisabled && g.Status != UserGroupStatusEnabled:
		return ErrUserGroupInvalidArgument
	}
	if !update && g.OwnerUserID != nil && *g.OwnerUserID <= 0 {
		return ErrUserGroupInvalidArgument
	}
	return nil
}
