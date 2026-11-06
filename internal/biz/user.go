package biz

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/user/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrUserNotFound 表示目标用户不存在（含数据范围不可见：不可见等同不存在）。
	ErrUserNotFound = kratoserrors.NotFound(v1.ErrorReason_USER_NOT_FOUND.String(), "user not found")
	// ErrUserInvalidArgument 表示用户请求参数非法。
	ErrUserInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_USER_INVALID_ARGUMENT.String(), "invalid user argument")
	// ErrUserAlreadyExists 表示租户内 name/email/phone 唯一约束冲突。
	ErrUserAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_USER_ALREADY_EXISTS.String(), "user already exists")
	// ErrUserAccessDenied 表示写侧权限判定不通过（分配角色、归属转移的
	// 新归属不可达）；数据范围不可见不走这里——那等同不存在。
	ErrUserAccessDenied = kratoserrors.Forbidden(v1.ErrorReason_USER_ACCESS_DENIED.String(), "user access denied")
	// ErrUserPasswordMismatch 表示个人中心改密时当前口令不匹配；返回
	// 400 而非 401，避免已认证会话被前端按失效处理。
	ErrUserPasswordMismatch = kratoserrors.BadRequest(v1.ErrorReason_USER_PASSWORD_MISMATCH.String(), "password mismatch")
)

// User 是用户领域模型。Password 仅在创建与改密路径携带：创建时为明文
// 入参、经用例哈希后传仓储；读取路径不携带，绝不跨越传输层。
// 归属三元组仅在创建（经 fn_can_set_owner 校验）与归属转移用例可写。
type User struct {
	ID       int64
	Status   int32 // 0 禁用、1 启用
	Name     string
	Nickname string
	Password string
	Email    *string
	Phone    *string
	Remark   *string
	Locale   string

	OwnerUserID      *int64
	OwnerDeptID      *int64
	OwnerUserGroupID *int64

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// UserList 是用户仓储 List 返回的分页信封。
type UserList struct {
	Items      []*User
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// OwnerTriple 表示一条数据的归属：归属人、归属部门、归属用户组。
// nil 表示没有该归属（存储 NULL），不与 ID=0 混淆。
type OwnerTriple struct {
	UserID  *int64
	DeptID  *int64
	GroupID *int64
}

// UserRepo 是用户仓储接口。全部实现必须经存储层会话门面在租户事务内
// 执行（RLS），@scope 守卫由仓储注入。List 消费 dynq 动态查询协议。
type UserRepo interface {
	// GetByID 按 id 查询单个用户；数据范围不可见返回 ErrUserNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*User, error)
	// List 按动态查询协议返回分页集合，结果已按数据范围过滤。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*UserList, error)
	// Create 创建用户（Password 为哈希后值）；唯一冲突映射 ErrUserAlreadyExists。
	Create(ctx context.Context, auth Identity, u *User) (*User, error)
	// Update 覆盖可变字段（不含 password 与归属三元组）。
	Update(ctx context.Context, auth Identity, u *User) (*User, error)
	// UpdateNickname 仅更新昵称（与 updated_by），其余可变字段保持原值。
	UpdateNickname(ctx context.Context, auth Identity, id int64, nickname string) (*User, error)
	// SetPassword 替换密码哈希。
	SetPassword(ctx context.Context, auth Identity, id int64, hashedPassword string) error
	// SoftDelete 软删除用户（deleted_at 置位）。
	SoftDelete(ctx context.Context, auth Identity, id int64) error
	// AssignRoles 批量分配角色：按 ID 锁目标行（不判可见性；不存在/软删
	// 返回 ErrUserNotFound），库内 fn_can_assign_role 逐角色判定（可见性
	// + 目标范围 + 分配权限码），全部通过才写入；任一不通过整体拒绝返回
	// ErrUserAccessDenied。返回实际新增条数（已拥有的跳过）。
	AssignRoles(ctx context.Context, auth Identity, userID int64, roleIDs []int64) (int64, error)
	// TransferOwner 修改归属三元组：两头校验（该用户有权操作 + 新归属
	// 可达），新归属不可达返回 ErrUserAccessDenied。返回转移后的用户。
	TransferOwner(ctx context.Context, auth Identity, id int64, to OwnerTriple) (*User, error)
	// GetCredentialForLogin 登录路径专用：按租户内用户名读取口令哈希与
	// 状态。调用发生在操作者身份成立之前，故入参是租户 ID 而非
	// Identity；属文档化例外，不使用 super 命名。软删除行等同不存在
	// 返回 ErrUserNotFound。返回的哈希仅供口令校验单点使用，不跨越 biz。
	GetCredentialForLogin(ctx context.Context, tenantID, name string) (*UserCredential, error)
	// GetMyProfile 读取认证用户本人的资料。目标恒取 auth.UserID，不判
	// 行级归属；属身份成立后的内部回读例外。
	GetMyProfile(ctx context.Context, auth Identity) (*User, error)
	// UpdateMyProfile 覆盖本人可编辑资料（昵称/邮箱/手机号/备注/语言），
	// 目标恒取 auth.UserID。
	UpdateMyProfile(ctx context.Context, auth Identity, u *User) (*User, error)
	// GetMyPasswordHash 返回本人口令哈希，仅供当前口令校验单点使用。
	GetMyPasswordHash(ctx context.Context, auth Identity) (string, error)
	// SetMyPassword 替换本人口令哈希。
	SetMyPassword(ctx context.Context, auth Identity, hashedPassword string) error
}

// UserUsecase 是用户用例。
type UserUsecase struct {
	repo UserRepo
}

// NewUserUsecase 构造用户用例。
func NewUserUsecase(repo UserRepo) *UserUsecase {
	return &UserUsecase{repo: repo}
}

// GetUser 按 id 查询用户。
func (uc *UserUsecase) GetUser(ctx context.Context, auth Identity, id int64) (*User, error) {
	if id <= 0 {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.GetByID(ctx, auth, id)
}

// ListUsers 经动态查询协议列出用户。
func (uc *UserUsecase) ListUsers(ctx context.Context, auth Identity, q *dynq.Query) (*UserList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateUser 创建用户：status 缺省启用、locale 缺省 zh-CN，密码哈希后落库；
// 请求未指定归属人时缺省为创建人，避免落一条所有非全租户角色都不可见的行。
func (uc *UserUsecase) CreateUser(ctx context.Context, auth Identity, u *User) (*User, error) {
	if err := validateUserMutable(u); err != nil {
		return nil, err
	}
	if u.OwnerUserID == nil {
		creator := auth.UserID
		u.OwnerUserID = &creator
	}
	hashed, err := HashPassword(u.Password)
	if err != nil {
		return nil, err
	}
	u.Password = hashed
	if u.Status == 0 {
		u.Status = UserStatusEnabled
	}
	if u.Locale == "" {
		u.Locale = "zh-CN"
	}
	return uc.repo.Create(ctx, auth, u)
}

// UpdateUser 覆盖可变字段。password、归属三元组与时间戳由服务端管理。
func (uc *UserUsecase) UpdateUser(ctx context.Context, auth Identity, u *User) (*User, error) {
	if u == nil || u.ID <= 0 {
		return nil, ErrUserInvalidArgument
	}
	if err := validateUserMutable(u); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, auth, u)
}

// UpdateNickname 只更新昵称：入参去空白、非空且不超过列宽，其余可变
// 字段保持原值（存储层定向更新，不做整行覆盖）。
func (uc *UserUsecase) UpdateNickname(ctx context.Context, auth Identity, id int64, nickname string) (*User, error) {
	if id <= 0 {
		return nil, ErrUserInvalidArgument
	}
	trimmed := strings.TrimSpace(nickname)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > userNicknameMaxRunes {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.UpdateNickname(ctx, auth, id, trimmed)
}

// SetUserPassword 设置新密码；密码哈希后落库。
func (uc *UserUsecase) SetUserPassword(ctx context.Context, auth Identity, id int64, password string) error {
	if id <= 0 || password == "" {
		return ErrUserInvalidArgument
	}
	hashed, err := HashPassword(password)
	if err != nil {
		return err
	}
	return uc.repo.SetPassword(ctx, auth, id, hashed)
}

// GetMyProfile 返回认证用户本人的资料。
func (uc *UserUsecase) GetMyProfile(ctx context.Context, auth Identity) (*User, error) {
	return uc.repo.GetMyProfile(ctx, auth)
}

// UpdateMyProfile 覆盖本人可编辑资料：昵称必填，locale 缺省 zh-CN。
func (uc *UserUsecase) UpdateMyProfile(ctx context.Context, auth Identity, u *User) (*User, error) {
	if u == nil || strings.TrimSpace(u.Nickname) == "" ||
		utf8.RuneCountInString(u.Nickname) > userNicknameMaxRunes {
		return nil, ErrUserInvalidArgument
	}
	if u.Locale == "" {
		u.Locale = "zh-CN"
	}
	return uc.repo.UpdateMyProfile(ctx, auth, u)
}

// ChangeMyPassword 校验当前口令后替换为新口令。当前口令不匹配返回
// ErrUserPasswordMismatch（400，不触发会话失效）。
func (uc *UserUsecase) ChangeMyPassword(ctx context.Context, auth Identity, oldPassword, newPassword string) error {
	if oldPassword == "" || newPassword == "" {
		return ErrUserInvalidArgument
	}
	hash, err := uc.repo.GetMyPasswordHash(ctx, auth)
	if err != nil {
		return err
	}
	ok, _, err := VerifyPassword(hash, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrUserPasswordMismatch
	}
	hashed, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return uc.repo.SetMyPassword(ctx, auth, hashed)
}

// DeleteUser 软删除用户。
func (uc *UserUsecase) DeleteUser(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrUserInvalidArgument
	}
	return uc.repo.SoftDelete(ctx, auth, id)
}

// AssignRoles 批量分配角色：入参去重排序，保证判定与落库稳定。
func (uc *UserUsecase) AssignRoles(ctx context.Context, auth Identity, userID int64, roleIDs []int64) (int64, error) {
	if userID <= 0 || len(roleIDs) == 0 {
		return 0, ErrUserInvalidArgument
	}
	roles := slices.Clone(roleIDs)
	slices.Sort(roles)
	roles = slices.Compact(roles)
	return uc.repo.AssignRoles(ctx, auth, userID, roles)
}

// TransferUserOwner 修改归属三元组。
func (uc *UserUsecase) TransferUserOwner(ctx context.Context, auth Identity, id int64, to OwnerTriple) (*User, error) {
	if id <= 0 {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.TransferOwner(ctx, auth, id, to)
}

// UserStatusEnabled / UserStatusDisabled 与存储层 sys_user.status 取值一致。
const (
	UserStatusDisabled int32 = 0
	UserStatusEnabled  int32 = 1
)

// userNicknameMaxRunes 与 sys_user.nickname 列宽（varchar(256)）对齐。
const userNicknameMaxRunes = 256

// validateUserMutable 校验可变字段：name/nickname 必填、status 合法。
func validateUserMutable(u *User) error {
	if u == nil ||
		strings.TrimSpace(u.Name) == "" ||
		strings.TrimSpace(u.Nickname) == "" ||
		(u.Status != UserStatusDisabled && u.Status != UserStatusEnabled) {
		return ErrUserInvalidArgument
	}
	return nil
}
