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
	// TenantID 是登录目标租户；为空时回落用例构造时的默认租户。
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
	// audit 是登录日志的追加写入点；nil 表示不记录（单元测试或精简装配）。
	audit *AuditLogUsecase
	// defaultTenantID 是登录默认租户：请求未指明租户时回落到这里；
	// 来源是装配层的 auth 配置。
	defaultTenantID string
}

// NewAuthUsecase 构造登录用例；defaultTenantID 为缺省租户，
// 可为空（此时请求必须显式携带租户）。audit 可为 nil（不记录登录日志）。
func NewAuthUsecase(users UserRepo, sessions *SessionUsecase, audit *AuditLogUsecase, defaultTenantID string) *AuthUsecase {
	return &AuthUsecase{users: users, sessions: sessions, audit: audit, defaultTenantID: defaultTenantID}
}

// recordLoginAttempt 追加一条登录日志。审计写入是 best-effort：失败只
// 记日志，不改变登录结果；明文口令不进入日志事实。
func (uc *AuthUsecase) recordLoginAttempt(
	ctx context.Context,
	tenantID, userName string,
	userID int64,
	ipAddress, deviceInfo *string,
	success bool,
	reason string,
) {
	if uc.audit == nil {
		return
	}
	var failureReason *string
	if reason != "" {
		failureReason = &reason
	}
	in := &LoginLogInput{
		TenantID:      tenantID,
		UserID:        userID,
		Username:      userName,
		IPAddress:     ipAddress,
		UserAgent:     deviceInfo,
		LoginType:     "password",
		IsSuccess:     success,
		FailureReason: failureReason,
	}
	if err := uc.audit.RecordLogin(ctx, in); err != nil {
		slog.WarnContext(ctx, "login audit write failed",
			"tenant_id", tenantID, "user_name", userName, "user_id", userID, "success", success, "err", err)
	}
}

// Login 校验用户名与口令，通过后签发会话并返回明文令牌对。
// 用户不存在与口令不匹配同因返回 ErrInvalidCredentials（防枚举），禁用
// 账号在口令通过后才拒绝；校验成功且算法非首选时透明重哈希，失败不阻断
// 登录。明文口令只在本次调用内存中存在，日志与错误不携带。
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
	cred, err := uc.users.GetCredentialForLogin(ctx, tenantID, in.UserName)
	if err != nil {
		// 目标用户不存在（含软删除）与口令不匹配同因，不泄露存在性。
		if errors.Is(err, ErrUserNotFound) {
			uc.recordLoginAttempt(ctx, tenantID, in.UserName, 0, in.IPAddress, in.DeviceInfo, false, "invalid credentials")
			return nil, ErrInvalidCredentials
		}
		uc.recordLoginAttempt(ctx, tenantID, in.UserName, 0, in.IPAddress, in.DeviceInfo, false, "credential lookup failed")
		return nil, err
	}
	ok, rehash, err := VerifyPassword(cred.PasswordHash, in.Password)
	if err != nil {
		uc.recordLoginAttempt(ctx, tenantID, in.UserName, cred.UserID, in.IPAddress, in.DeviceInfo, false, "password verify failed")
		return nil, err
	}
	if !ok {
		uc.recordLoginAttempt(ctx, tenantID, in.UserName, cred.UserID, in.IPAddress, in.DeviceInfo, false, "invalid credentials")
		return nil, ErrInvalidCredentials
	}
	if cred.Status != UserStatusEnabled {
		uc.recordLoginAttempt(ctx, tenantID, in.UserName, cred.UserID, in.IPAddress, in.DeviceInfo, false, "user disabled")
		return nil, ErrAuthUserDisabled
	}
	if rehash {
		if hashed, herr := HashPassword(in.Password); herr != nil {
			slog.WarnContext(ctx, "login rehash skipped: hash password failed",
				"tenant_id", tenantID, "user_id", cred.UserID, "err", herr)
		} else if err := uc.users.SetPassword(ctx, Identity{TenantID: tenantID, UserID: cred.UserID}, cred.UserID, hashed); err != nil {
			// 重哈希以本人身份写入；失败（如数据范围不可操作）不阻断
			// 登录，记日志后下次成功登录重试。
			slog.WarnContext(ctx, "login rehash skipped: set password denied",
				"tenant_id", tenantID, "user_id", cred.UserID, "err", err)
		}
	}
	issued, err := uc.sessions.Issue(ctx, Identity{TenantID: tenantID, UserID: cred.UserID}, in.Opts, in.DeviceInfo, in.IPAddress)
	if err != nil {
		uc.recordLoginAttempt(ctx, tenantID, in.UserName, cred.UserID, in.IPAddress, in.DeviceInfo, false, "session issue failed")
		return nil, err
	}
	uc.recordLoginAttempt(ctx, tenantID, in.UserName, cred.UserID, in.IPAddress, in.DeviceInfo, true, "")
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
