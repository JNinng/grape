package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeAuthUsers 只实现登录路径触碰的 UserRepo 方法，其余经内嵌接口兜底。
type fakeAuthUsers struct {
	UserRepo

	cred    *UserCredential
	credErr error

	setPasswordCalls int
	setPasswordHash  string
	setPasswordErr   error
}

func (f *fakeAuthUsers) GetCredentialByName(_ context.Context, tenantID, name string) (*UserCredential, error) {
	if tenantID != "t-1" || name != "admin" {
		return nil, ErrUserNotFound
	}
	return f.cred, f.credErr
}

func (f *fakeAuthUsers) SetPassword(_ context.Context, auth AuthContext, id int64, hashed string) error {
	f.setPasswordCalls++
	f.setPasswordHash = hashed
	return f.setPasswordErr
}

// newAuthUsecase 组装被测用例：登录签发复用既有 fakeSessionRepo，
// 过渡期默认租户为 t-1。
func newAuthUsecase(t *testing.T, users *fakeAuthUsers) (*AuthUsecase, *fakeSessionRepo) {
	t.Helper()
	sessionsRepo := &fakeSessionRepo{session: &Session{ID: 77, TenantID: "t-1", UserID: 1}}
	return NewAuthUsecase(users, NewSessionUsecase(sessionsRepo), "t-1"), sessionsRepo
}

func TestAuthLoginSuccessAndRehash(t *testing.T) {
	argonHash, err := HashPassword("pw-plain")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeAuthUsers{cred: &UserCredential{
		UserID: 1, Status: UserStatusEnabled, Name: "admin", Nickname: "管理员", PasswordHash: argonHash,
	}}
	uc, sessionsRepo := newAuthUsecase(t, users)
	ctx := context.Background()

	out, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "pw-plain"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if out.Issued.AccessToken == "" || out.Issued.RefreshToken == "" {
		t.Fatal("login must return token pair")
	}
	if out.Issued.Session.UserID != 1 || out.Issued.Session.TenantID != "t-1" {
		t.Fatalf("session owner = %+v, want user 1 in t-1", out.Issued.Session)
	}
	if out.User.Name != "admin" || out.User.Nickname != "管理员" || out.User.ID != 1 {
		t.Fatalf("summary = %+v, want admin", out.User)
	}
	if users.setPasswordCalls != 0 {
		t.Fatal("argon2id (preferred) must not rehash")
	}
	if sessionsRepo.issueCalls != 1 || sessionsRepo.issueOwner.UserID != 1 {
		t.Fatalf("issue = (%d calls, owner %+v), want 1 call for user 1", sessionsRepo.issueCalls, sessionsRepo.issueOwner)
	}

	// 过渡期默认租户：请求未指明租户时回落用例构造值
	out, err = uc.Login(ctx, &LoginInput{UserName: "admin", Password: "pw-plain"})
	if err != nil {
		t.Fatalf("login with default tenant: %v", err)
	}
	if out.Issued.Session.TenantID != "t-1" || out.User.TenantID != "t-1" {
		t.Fatalf("tenant = %s/%s, want default t-1", out.Issued.Session.TenantID, out.User.TenantID)
	}

	// 种子 scrypt 哈希：登录成功并透明重哈希为 argon2id
	users.cred.PasswordHash = legacyScryptHash(t, "pw-plain", 14, 8, 1)
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "pw-plain"}); err != nil {
		t.Fatalf("legacy login: %v", err)
	}
	if users.setPasswordCalls != 1 {
		t.Fatalf("rehash calls = %d, want 1", users.setPasswordCalls)
	}
	if !strings.HasPrefix(users.setPasswordHash, "$argon2id$") {
		t.Fatalf("rehashed = %q, want argon2id", users.setPasswordHash)
	}

	// 重哈希失败不阻断登录
	users.setPasswordErr = errors.New("scope denied")
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "pw-plain"}); err != nil {
		t.Fatalf("login must survive rehash failure: %v", err)
	}
}

func TestAuthLoginRejects(t *testing.T) {
	argonHash, _ := HashPassword("pw-plain")
	users := &fakeAuthUsers{cred: &UserCredential{
		UserID: 1, Status: UserStatusEnabled, Name: "admin", PasswordHash: argonHash,
	}}
	uc, _ := newAuthUsecase(t, users)
	ctx := context.Background()

	// 口令不匹配与用户不存在同因，防枚举
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "wrong"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "nobody", Password: "pw-plain"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: want ErrInvalidCredentials, got %v", err)
	}

	// 凭据点查的介质错误原样透传（不是凭据错误）
	users.credErr = errors.New("db down")
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "pw-plain"}); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("repo error must pass through, got %v", err)
	}
	users.credErr = nil

	// 禁用账号：口令通过后才拒绝
	users.cred.Status = UserStatusDisabled
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "pw-plain"}); !errors.Is(err, ErrAuthUserDisabled) {
		t.Fatalf("disabled: want ErrAuthUserDisabled, got %v", err)
	}
	// 禁用账号口令错误时仍报凭据错误（不泄露账号状态）
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-1", UserName: "admin", Password: "wrong"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled+wrong password: want ErrInvalidCredentials, got %v", err)
	}

	// 非默认租户是合法登录目标：与未知用户同因，防枚举
	if _, err := uc.Login(ctx, &LoginInput{TenantID: "t-other", UserName: "admin", Password: "pw-plain"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("other tenant: want ErrInvalidCredentials, got %v", err)
	}

	// 无默认租户且请求未指明租户：参数非法
	noDefault := NewAuthUsecase(users, NewSessionUsecase(&fakeSessionRepo{}), "")
	if _, err := noDefault.Login(ctx, &LoginInput{UserName: "admin", Password: "pw-plain"}); !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("no tenant and no default: want ErrSessionInvalidArgument, got %v", err)
	}

	// 入参缺失：无用户名、无口令、空入参
	for i, in := range []*LoginInput{
		{TenantID: "t-1", Password: "pw-plain"},
		{TenantID: "t-1", UserName: "admin"},
		nil,
	} {
		if _, err := uc.Login(ctx, in); !errors.Is(err, ErrSessionInvalidArgument) {
			t.Fatalf("case %d: want ErrSessionInvalidArgument, got %v", i, err)
		}
	}
}
