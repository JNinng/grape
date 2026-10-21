package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	sessionv1 "grape/api/session/v1"

	"github.com/go-kratos/kratos/v3/errors"
)

var (
	// ErrSessionInvalid 表示令牌无效：会话不存在、已撤销、已过期或已
	// 禁用。不区分具体原因，避免向调用方泄露会话状态。
	ErrSessionInvalid = errors.Unauthorized(sessionv1.ErrorReason_SESSION_INVALID.String(), "session invalid or expired")
	// ErrSessionInvalidArgument 表示会话签发/撤销的入参非法。
	ErrSessionInvalidArgument = errors.BadRequest(sessionv1.ErrorReason_SESSION_INVALID_ARGUMENT.String(), "invalid session argument")
)

// SessionStatusEnabled / SessionStatusDisabled 与存储层 sys_session.status
// 取值一致。
const (
	SessionStatusDisabled int32 = 0
	SessionStatusEnabled  int32 = 1
)

// 会话有效期缺省值。接入配置后由调用方经 SessionOptions 覆盖。
const (
	DefaultSessionAccessTTL  = 30 * time.Minute
	DefaultSessionRefreshTTL = 7 * 24 * time.Hour
)

// 会话元数据列宽上限（sys_session.device_info / ip_address 为
// varchar(512) / varchar(64)）。超长按字符截断而非拒绝：登录不应因
// 异常长的 User-Agent / X-Forwarded-For 失败，元数据仅作审计展示。
const (
	MaxSessionDeviceInfoChars = 512
	MaxSessionIPAddressChars  = 64
)

// Session 是会话领域模型。令牌不以任何形态出现在模型里：哈希只在
// 仓储接口入参上出现，明文只在用例内短暂存在并作为签发结果返回。
type Session struct {
	// ID 是会话主键。
	ID int64
	// Status 是会话状态：0 禁用、1 启用。
	Status int32
	// UserID 是会话所属用户。
	UserID int64
	// TenantID 是会话所属租户；校验命中后与 UserID 一起构造 AuthContext。
	TenantID string

	// ExpiresAt 是访问令牌过期时间。
	ExpiresAt time.Time
	// RefreshExpiresAt 是刷新令牌过期时间；nil 表示没有刷新令牌。
	RefreshExpiresAt *time.Time
	// LastActivityAt 是最近活跃时间：签发与刷新时更新，校验不写入。
	LastActivityAt *time.Time
	// DeviceInfo 是登录设备信息（OS/浏览器/型号等），仅作审计展示。
	DeviceInfo *string
	// IPAddress 是登录来源 IP，仅作审计展示。
	IPAddress *string
	// IsRevoked 为 true 时即使未过期也不可用于认证。
	IsRevoked bool

	// CreatedBy 是签发操作者（会话用户）。
	CreatedBy int64
	// UpdatedBy 是最近一次变更的操作者（刷新为会话用户，撤销为调用者）。
	UpdatedBy int64
	// CreatedAt 是签发时间。
	CreatedAt time.Time
	// UpdatedAt 是最近一次变更时间；由存储实现维护。
	UpdatedAt *time.Time
}

// AuthContext 返回会话的身份事实，供认证中间件注入上下文。
func (s *Session) AuthContext() AuthContext {
	return AuthContext{TenantID: s.TenantID, UserID: s.UserID}
}

// IssueSessionInput 是签发会话的仓储入参：令牌哈希已由 biz 用例计算，
// 仓储只见哈希、不见明文。有效期以 TTL 表达，由存储介质在写入时刻
// 起算，避免应用与存储的时钟/时区差异影响过期判定。
type IssueSessionInput struct {
	// AccessTokenHash 是 sha256(访问令牌) 的小写十六进制；明文不进入仓储。
	AccessTokenHash string
	// RefreshTokenHash 是 sha256(刷新令牌) 的小写十六进制。
	RefreshTokenHash string
	// AccessTTL 是访问令牌有效期，由存储实现在写入时刻起算。
	AccessTTL time.Duration
	// RefreshTTL 是刷新令牌有效期，由存储实现在写入时刻起算。
	RefreshTTL time.Duration
	// DeviceInfo 是登录设备信息，nil 表示未提供。
	DeviceInfo *string
	// IPAddress 是登录来源 IP，nil 表示未提供。
	IPAddress *string
}

// RefreshSessionInput 是刷新令牌轮换的仓储入参：当前刷新令牌哈希只用于
// 定位会话，新访问令牌/刷新令牌哈希与有效期由 biz 用例计算。
type RefreshSessionInput struct {
	// CurrentRefreshTokenHash 是当前刷新令牌哈希，只用于定位会话；轮换后即失效。
	CurrentRefreshTokenHash string
	// NewAccessTokenHash 是新访问令牌哈希。
	NewAccessTokenHash string
	// NewRefreshTokenHash 是新刷新令牌哈希，供存储实现校验写回值。
	NewRefreshTokenHash string
	// AccessTTL 是新访问令牌有效期，从轮换时刻由存储实现起算。
	AccessTTL time.Duration
	// RefreshTTL 是新刷新令牌有效期，从轮换时刻由存储实现起算。
	RefreshTTL time.Duration
}

// SessionRepo 是会话仓储接口。签发/校验/撤销/刷新四个动作按领域建模，
// 介质（数据库/缓存）差异收在实现之后；令牌哈希由 biz 用例计算后
// 传入，仓储出入参只见哈希。过期会话清理不属于存取接口：它由介质侧
// 的维护任务承担，调用方无需感知。
type SessionRepo interface {
	// Issue 写入一条会话并返回落库后的会话事实。owner 是会话所属身份，
	// 同时作为仓储的租户边界与审计操作者。
	Issue(ctx context.Context, owner AuthContext, in *IssueSessionInput) (*Session, error)
	// Validate 按访问令牌哈希返回有效会话（启用、未撤销、未过期）；
	// 任一条件不满足都返回 ErrSessionInvalid，不区分原因。
	Validate(ctx context.Context, accessTokenHash string) (*Session, error)
	// Revoke 撤销租户内的指定会话；已撤销或不可见时幂等返回 nil。
	Revoke(ctx context.Context, auth AuthContext, id int64) error
	// Refresh 按当前刷新令牌哈希轮换令牌：命中的行必须启用、未撤销、
	// 刷新令牌未过期；旧刷新令牌随即失效。返回轮换后的会话。
	Refresh(ctx context.Context, in *RefreshSessionInput) (*Session, error)
}

// IssuedSession 是签发/刷新结果：AccessToken/RefreshToken 是明文令牌，
// 只返回给调用方（登录/刷新响应），不入库、不跨越仓储边界。
type IssuedSession struct {
	// Session 是落库后的会话事实，过期时间以存储返回值为准。
	Session *Session
	// AccessToken 是明文访问令牌，只返回调用方，不落库。
	AccessToken string
	// RefreshToken 是明文刷新令牌，只返回调用方；下一次轮换后失效。
	RefreshToken string
}

// SessionOptions 控制签发/刷新的有效期；零值取缺省值。
type SessionOptions struct {
	// AccessTTL 是访问令牌有效期；<=0 取 DefaultSessionAccessTTL。
	AccessTTL time.Duration
	// RefreshTTL 是刷新令牌有效期；<=0 取 DefaultSessionRefreshTTL。
	RefreshTTL time.Duration
}

func (o SessionOptions) withDefaults() SessionOptions {
	if o.AccessTTL <= 0 {
		o.AccessTTL = DefaultSessionAccessTTL
	}
	if o.RefreshTTL <= 0 {
		o.RefreshTTL = DefaultSessionRefreshTTL
	}
	return o
}

// SessionUsecase 是会话用例：令牌生成与哈希的唯一入口，仓储只接收
// 哈希。登录（密码校验）与认证中间件（令牌校验）落地时复用本用例。
type SessionUsecase struct {
	repo SessionRepo
}

// NewSessionUsecase 构造会话用例。
func NewSessionUsecase(repo SessionRepo) *SessionUsecase {
	return &SessionUsecase{repo: repo}
}

// Issue 签发会话：生成独立的访问令牌与刷新令牌，只把哈希交给仓储，
// 明文令牌随结果返回给调用方。
func (uc *SessionUsecase) Issue(ctx context.Context, owner AuthContext, opts SessionOptions, deviceInfo, ipAddress *string) (*IssuedSession, error) {
	if owner.TenantID == "" || owner.UserID <= 0 {
		return nil, ErrSessionInvalidArgument
	}
	opts = opts.withDefaults()
	accessToken, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("issue session: generate access token: %w", err)
	}
	refreshToken, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("issue session: generate refresh token: %w", err)
	}
	session, err := uc.repo.Issue(ctx, owner, &IssueSessionInput{
		AccessTokenHash:  hashSessionToken(accessToken),
		RefreshTokenHash: hashSessionToken(refreshToken),
		AccessTTL:        opts.AccessTTL,
		RefreshTTL:       opts.RefreshTTL,
		DeviceInfo:       clampSessionChars(deviceInfo, MaxSessionDeviceInfoChars),
		IPAddress:        clampSessionChars(ipAddress, MaxSessionIPAddressChars),
	})
	if err != nil {
		return nil, err
	}
	return &IssuedSession{Session: session, AccessToken: accessToken, RefreshToken: refreshToken}, nil
}

// Validate 校验访问令牌：哈希后交仓储点查，返回会话身份事实。
func (uc *SessionUsecase) Validate(ctx context.Context, accessToken string) (*Session, error) {
	if accessToken == "" {
		return nil, ErrSessionInvalid
	}
	return uc.repo.Validate(ctx, hashSessionToken(accessToken))
}

// Revoke 撤销会话（登出）。重复撤销幂等。
func (uc *SessionUsecase) Revoke(ctx context.Context, auth AuthContext, sessionID int64) error {
	if auth.TenantID == "" || auth.UserID <= 0 || sessionID <= 0 {
		return ErrSessionInvalidArgument
	}
	return uc.repo.Revoke(ctx, auth, sessionID)
}

// Refresh 用刷新令牌轮换一对新令牌：访问令牌与刷新令牌都是独立随机
// 值，旧刷新令牌随即失效。有效期从本次刷新时刻重新起算。
func (uc *SessionUsecase) Refresh(ctx context.Context, refreshToken string, opts SessionOptions) (*IssuedSession, error) {
	if refreshToken == "" {
		return nil, ErrSessionInvalid
	}
	opts = opts.withDefaults()
	accessToken, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("refresh session: generate access token: %w", err)
	}
	newRefreshToken, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("refresh session: generate refresh token: %w", err)
	}
	session, err := uc.repo.Refresh(ctx, &RefreshSessionInput{
		CurrentRefreshTokenHash: hashSessionToken(refreshToken),
		NewAccessTokenHash:      hashSessionToken(accessToken),
		NewRefreshTokenHash:     hashSessionToken(newRefreshToken),
		AccessTTL:               opts.AccessTTL,
		RefreshTTL:              opts.RefreshTTL,
	})
	if err != nil {
		return nil, err
	}
	return &IssuedSession{Session: session, AccessToken: accessToken, RefreshToken: newRefreshToken}, nil
}

// newSessionToken 生成 32 字节随机不透明令牌（base64url 无填充）。
// 令牌不含租户/用户信息，身份事实一律以会话行内数据为准。
func newSessionToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// hashSessionToken 是令牌哈希的唯一入口：sha256 → 小写十六进制
// （64 字符，落在 token_hash varchar(128) 内）。
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// clampSessionChars 按字符数截断会话元数据（PostgreSQL varchar(n) 按字
// 符计长度），nil 原样返回；未超长时返回原指针，不产生拷贝。
func clampSessionChars(s *string, maxChars int) *string {
	if s == nil {
		return nil
	}
	runes := []rune(*s)
	if len(runes) <= maxChars {
		return s
	}
	clipped := string(runes[:maxChars])
	return &clipped
}
