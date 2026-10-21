package biz

import (
	"context"
	"errors"
	"log/slog"
)

// UserCredential 是登录校验所需的最小用户事实。PasswordHash 仅供 biz
// 的口令校验单点（VerifyPassword）使用，不得跨越 biz 边界。
type UserCredential struct {
	// UserID 是用户主键，校验通过后构造会话的身份事实。
	UserID int64
	// Status 是数据状态：0 禁用、1 启用（与 UserStatus* 取值一致）。
	Status int32
	// Name 是用户名。
	Name string
	// Nickname 是昵称。
	Nickname string
	// PasswordHash 是库内自描述口令哈希（算法标识内嵌）。
	PasswordHash string
}

// UserSummary 是登录响应携带的账号摘要：来自凭据点查的行内事实，
// 不含口令哈希。
type UserSummary struct {
	ID       int64
	TenantID string
	Name     string
	Nickname string
}

// LoginResult 是登录结果：令牌对与会话事实之外，附带账号摘要供响应
// 渲染，避免传输层为拼响应再查一次用户。
type LoginResult struct {
	Issued *IssuedSession
	User   UserSummary
}

// LoginInput 是登录用例的入参。
type LoginInput struct {
	// TenantID 是登录目标租户；为空时回落用例构造时的过渡期默认租户。
	TenantID string
	// UserName 是租户内唯一的用户名。
	UserName string
	// Password 是明文口令，仅本次校验使用。
	Password string
	// Opts 控制签发会话的有效期；零值取缺省值。
	Opts SessionOptions
	// DeviceInfo 是登录设备信息，nil 表示未提供。
	DeviceInfo *string
	// IPAddress 是登录来源 IP，nil 表示未提供。
	IPAddress *string
}

// AuthUsecase 是登录用例：口令校验（多算法分发）与会话签发的编排点。
// 令牌生成/哈希仍单点收敛在 SessionUsecase；本用例只决定“能否登录”
// 与“签发给谁”。
type AuthUsecase struct {
	users    UserRepo
	sessions *SessionUsecase
	// defaultTenantID 是过渡期登录默认租户（多租户登录形态确定前，
	// 请求未指明租户时回落到这里；来源是装配层的 auth 配置）。
	defaultTenantID string
}

// NewAuthUsecase 构造登录用例；defaultTenantID 为过渡期缺省租户，
// 可为空（此时请求必须显式携带租户）。
func NewAuthUsecase(users UserRepo, sessions *SessionUsecase, defaultTenantID string) *AuthUsecase {
	return &AuthUsecase{users: users, sessions: sessions, defaultTenantID: defaultTenantID}
}

// Login 校验用户名与口令，通过后签发会话并返回明文令牌对。
//
// 纪律：
//   - 用户不存在与口令不匹配同因返回 ErrInvalidCredentials，防枚举；
//     禁用账号在口令校验通过后才拒绝（ErrAuthUserDisabled），未通过
//     口令校验的调用方无从得知账号状态；
//   - 口令按库内哈希的算法标识分发校验（VerifyPassword）；校验成功且
//     非当前首选算法时透明重哈希（种子 scrypt → argon2id 的迁移路径），
//     重哈希失败不阻断登录，仅记日志，下一次成功登录自动重试；
//   - 明文口令只在本次调用内存中存在，日志与错误信息不携带。
func (uc *AuthUsecase) Login(ctx context.Context, in *LoginInput) (*LoginResult, error) {
	if in == nil {
		return nil, ErrSessionInvalidArgument
	}
	tenantID := in.TenantID
	if tenantID == "" {
		tenantID = uc.defaultTenantID
	}
	if tenantID == "" || in.UserName == "" || in.Password == "" {
		return nil, ErrSessionInvalidArgument
	}
	cred, err := uc.users.GetCredentialByName(ctx, tenantID, in.UserName)
	if err != nil {
		// 目标用户不存在（含软删除）与口令不匹配同因，不泄露存在性。
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	ok, rehash, err := VerifyPassword(cred.PasswordHash, in.Password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	if cred.Status != UserStatusEnabled {
		return nil, ErrAuthUserDisabled
	}
	if rehash {
		if hashed, herr := HashPassword(in.Password); herr != nil {
			slog.WarnContext(ctx, "login rehash skipped: hash password failed",
				"tenant_id", tenantID, "user_id", cred.UserID, "err", herr)
		} else if err := uc.users.SetPassword(ctx, AuthContext{TenantID: tenantID, UserID: cred.UserID}, cred.UserID, hashed); err != nil {
			// 重哈希以本人身份写入；失败（如数据范围不可操作）不阻断
			// 登录，记日志后下次成功登录重试。
			slog.WarnContext(ctx, "login rehash skipped: set password denied",
				"tenant_id", tenantID, "user_id", cred.UserID, "err", err)
		}
	}
	issued, err := uc.sessions.Issue(ctx, AuthContext{TenantID: tenantID, UserID: cred.UserID}, in.Opts, in.DeviceInfo, in.IPAddress)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Issued: issued,
		User: UserSummary{
			ID:       cred.UserID,
			TenantID: tenantID,
			Name:     cred.Name,
			Nickname: cred.Nickname,
		},
	}, nil
}
