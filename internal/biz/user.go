package biz

import (
	"context"
	"strings"
	"time"

	v1 "grape/api/user/v1"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrUserNotFound 表示目标用户不存在。
	ErrUserNotFound = errors.NotFound(v1.ErrorReason_USER_NOT_FOUND.String(), "user not found")
	// ErrUserInvalidArgument 表示用户请求参数非法。
	ErrUserInvalidArgument = errors.BadRequest(v1.ErrorReason_USER_INVALID_ARGUMENT.String(), "invalid user argument")
	// ErrUserAlreadyExists 表示创建用户时唯一约束冲突（如邮箱已被占用）。
	ErrUserAlreadyExists = errors.Conflict(v1.ErrorReason_USER_ALREADY_EXISTS.String(), "user already exists")
)

// UserStatus 是用户账号的生命周期状态，取值与存储层枚举一致。
type UserStatus string

const (
	// UserStatusActive 表示正常账号。
	UserStatusActive UserStatus = "active"
	// UserStatusInactive 表示已停用但保留的账号。
	UserStatusInactive UserStatus = "inactive"
	// UserStatusBanned 表示已封禁的账号。
	UserStatusBanned UserStatus = "banned"
)

// User 是用户领域模型。Password 仅在创建与改密路径携带，
// 读取路径绝不允许跨越传输层。
type User struct {
	ID        int64
	Name      string
	Email     string
	Password  string
	Status    UserStatus
	Role      *string
	Age       *int32
	Score     *float64
	Balance   *string // 十进制字符串，如 "100.25"
	BirthDate *string // 日期 YYYY-MM-DD
	OrgID     *string // 组织 UUID
	Tags      []int32
	Meta      map[string]any
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

// UserRepo 是用户仓储接口。List 消费 dynq 动态查询协议。
type UserRepo interface {
	// GetByID 按主键查询单个用户。
	GetByID(context.Context, int64) (*User, error)
	// GetByEmail 按唯一邮箱查询单个用户。
	GetByEmail(context.Context, string) (*User, error)
	// List 按动态查询协议返回分页集合。
	List(context.Context, *dynq.Query) (*UserList, error)
	// Create 创建用户。
	Create(context.Context, *User) (*User, error)
	// Update 覆盖可变字段（status、password、时间戳除外）。
	Update(context.Context, *User) (*User, error)
	// SetPassword 替换用户密码。
	SetPassword(context.Context, int64, string) error
	// Delete 物理删除用户。
	Delete(context.Context, int64) error
}

// UserUsecase 是用户用例。
type UserUsecase struct {
	repo UserRepo
}

// NewUserUsecase 构造用户用例。
func NewUserUsecase(repo UserRepo) *UserUsecase {
	return &UserUsecase{repo: repo}
}

// GetUser 按 ID 查询用户。
func (uc *UserUsecase) GetUser(ctx context.Context, id int64) (*User, error) {
	if id <= 0 {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.GetByID(ctx, id)
}

// GetUserByEmail 按邮箱查询用户。
func (uc *UserUsecase) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if strings.TrimSpace(email) == "" {
		return nil, ErrUserInvalidArgument
	}
	return uc.repo.GetByEmail(ctx, email)
}

// ListUsers 经动态查询协议列出用户。
func (uc *UserUsecase) ListUsers(ctx context.Context, q *dynq.Query) (*UserList, error) {
	return uc.repo.List(ctx, q)
}

// CreateUser 创建用户；status 未指定时默认 active，密码哈希后落库。
func (uc *UserUsecase) CreateUser(ctx context.Context, u *User) (*User, error) {
	if err := validateUserCreate(u); err != nil {
		return nil, err
	}
	hashed, err := hashPassword(u.Password)
	if err != nil {
		return nil, err
	}
	u.Password = hashed
	if u.Status == "" {
		u.Status = UserStatusActive
	}
	return uc.repo.Create(ctx, u)
}

// UpdateUser 覆盖用户可变字段。status、password 与时间戳由服务端管理，
// 输入中被忽略。
func (uc *UserUsecase) UpdateUser(ctx context.Context, u *User) (*User, error) {
	if u == nil || u.ID <= 0 {
		return nil, ErrUserInvalidArgument
	}
	if err := validateUserMutable(u); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, u)
}

// SetPassword 替换用户密码；密码哈希后落库。
func (uc *UserUsecase) SetPassword(ctx context.Context, id int64, password string) error {
	if id <= 0 || password == "" {
		return ErrUserInvalidArgument
	}
	hashed, err := hashPassword(password)
	if err != nil {
		return err
	}
	return uc.repo.SetPassword(ctx, id, hashed)
}

// hashPassword 以 bcrypt 生成带盐哈希。密码超出 bcrypt 长度上限时
// 视为请求参数非法。
func hashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", ErrUserInvalidArgument
	}
	return string(hashed), nil
}

// DeleteUser 物理删除用户。
func (uc *UserUsecase) DeleteUser(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrUserInvalidArgument
	}
	return uc.repo.Delete(ctx, id)
}

// validateUserCreate 校验创建入参：name、email、password 必填。
func validateUserCreate(u *User) error {
	if u == nil {
		return ErrUserInvalidArgument
	}
	if strings.TrimSpace(u.Name) == "" ||
		strings.TrimSpace(u.Email) == "" ||
		u.Password == "" {
		return ErrUserInvalidArgument
	}
	return validateUserMutable(u)
}

// validateUserMutable 校验可变字段：name 必填。
func validateUserMutable(u *User) error {
	if strings.TrimSpace(u.Name) == "" {
		return ErrUserInvalidArgument
	}
	return nil
}
