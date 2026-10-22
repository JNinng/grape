package biz

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeSessionRepo 记录用例交给仓储的入参，用于断言明文令牌从不跨越
// 仓储边界。
type fakeSessionRepo struct {
	session *Session
	err     error

	issueCalls    int
	issueOwner    Identity
	issueInput    *IssueSessionInput
	validateCalls int
	accessHash    string
	refreshCalls  int
	refreshInput  *RefreshSessionInput
	revokeCalls   int
	revokeAuth    Identity
	revokeID      int64
}

func (f *fakeSessionRepo) Issue(_ context.Context, owner Identity, in *IssueSessionInput) (*Session, error) {
	f.issueCalls++
	f.issueOwner = owner
	f.issueInput = in
	return f.session, f.err
}

func (f *fakeSessionRepo) Validate(_ context.Context, accessTokenHash string) (*Session, error) {
	f.validateCalls++
	f.accessHash = accessTokenHash
	return f.session, f.err
}

func (f *fakeSessionRepo) Revoke(_ context.Context, auth Identity, id int64) error {
	f.revokeCalls++
	f.revokeAuth = auth
	f.revokeID = id
	return f.err
}

func (f *fakeSessionRepo) Refresh(_ context.Context, in *RefreshSessionInput) (*Session, error) {
	f.refreshCalls++
	f.refreshInput = in
	return f.session, f.err
}

func TestSessionUsecaseIssueHashesTokens(t *testing.T) {
	repo := &fakeSessionRepo{session: &Session{ID: 42, TenantID: "t-1", UserID: 7}}
	uc := NewSessionUsecase(repo)
	owner := Identity{TenantID: "t-1", UserID: 7}
	device, ip := "test-device", "127.0.0.1"

	out, err := uc.Issue(context.Background(), owner, SessionOptions{}, &device, &ip)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if repo.issueCalls != 1 {
		t.Fatalf("repo issue calls = %d, want 1", repo.issueCalls)
	}
	if repo.issueOwner != owner {
		t.Fatalf("repo owner = %+v, want %+v", repo.issueOwner, owner)
	}
	if out.Session != repo.session {
		t.Fatal("issue result must carry the stored session")
	}
	if out.AccessToken == "" || out.RefreshToken == "" || out.AccessToken == out.RefreshToken {
		t.Fatalf("tokens = %q/%q, want two independent non-empty values", out.AccessToken, out.RefreshToken)
	}
	if len(out.AccessToken) != 43 {
		t.Fatalf("access token length = %d, want 43", len(out.AccessToken))
	}

	in := repo.issueInput
	if in == nil {
		t.Fatal("repo received no issue input")
	}
	if in.AccessTokenHash != hashSessionToken(out.AccessToken) {
		t.Fatal("repo must receive the access token hash, not plaintext")
	}
	if in.RefreshTokenHash != hashSessionToken(out.RefreshToken) {
		t.Fatal("repo must receive the refresh token hash, not plaintext")
	}
	if in.AccessTokenHash == out.AccessToken || in.RefreshTokenHash == out.RefreshToken {
		t.Fatal("plaintext token crossed the repo boundary")
	}
	if in.AccessTTL != DefaultSessionAccessTTL || in.RefreshTTL != DefaultSessionRefreshTTL {
		t.Fatalf("ttl = %v/%v, want defaults %v/%v", in.AccessTTL, in.RefreshTTL, DefaultSessionAccessTTL, DefaultSessionRefreshTTL)
	}
	if in.DeviceInfo == nil || *in.DeviceInfo != device || in.IPAddress == nil || *in.IPAddress != ip {
		t.Fatalf("device/ip not forwarded: %+v", in)
	}
}

func TestSessionUsecaseIssueCustomTTL(t *testing.T) {
	repo := &fakeSessionRepo{session: &Session{ID: 1}}
	uc := NewSessionUsecase(repo)

	if _, err := uc.Issue(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		SessionOptions{AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour}, nil, nil); err != nil {
		t.Fatalf("issue: %v", err)
	}
	in := repo.issueInput
	if in.AccessTTL != 5*time.Minute || in.RefreshTTL != time.Hour {
		t.Fatalf("ttl = %v/%v, want 5m/1h", in.AccessTTL, in.RefreshTTL)
	}
}

func TestSessionUsecaseClampsMetadata(t *testing.T) {
	repo := &fakeSessionRepo{session: &Session{ID: 1}}
	uc := NewSessionUsecase(repo)

	device := strings.Repeat("設", 600)
	ip := strings.Repeat("1", 100)
	if _, err := uc.Issue(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		SessionOptions{}, &device, &ip); err != nil {
		t.Fatalf("issue: %v", err)
	}
	in := repo.issueInput
	if in.DeviceInfo == nil || len([]rune(*in.DeviceInfo)) != MaxSessionDeviceInfoChars {
		t.Fatalf("device len = %d, want %d", len([]rune(*in.DeviceInfo)), MaxSessionDeviceInfoChars)
	}
	if in.IPAddress == nil || len([]rune(*in.IPAddress)) != MaxSessionIPAddressChars {
		t.Fatalf("ip len = %d, want %d", len([]rune(*in.IPAddress)), MaxSessionIPAddressChars)
	}
	if *in.DeviceInfo != string([]rune(device)[:MaxSessionDeviceInfoChars]) {
		t.Fatal("device must be the rune prefix")
	}
	// 未超长时原样透传（同指针）
	shortDevice := "ua/short"
	shortIP := "127.0.0.1"
	if _, err := uc.Issue(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		SessionOptions{}, &shortDevice, &shortIP); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if repo.issueInput.DeviceInfo != &shortDevice || repo.issueInput.IPAddress != &shortIP {
		t.Fatal("within-limit metadata must pass through unchanged")
	}
	// nil 透传为 nil
	if _, err := uc.Issue(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		SessionOptions{}, nil, nil); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if repo.issueInput.DeviceInfo != nil || repo.issueInput.IPAddress != nil {
		t.Fatalf("nil metadata must stay nil: %+v", repo.issueInput)
	}
}

func TestSessionUsecaseIssueRejectsInvalidOwner(t *testing.T) {
	repo := &fakeSessionRepo{}
	uc := NewSessionUsecase(repo)

	if _, err := uc.Issue(context.Background(), Identity{TenantID: "", UserID: 1}, SessionOptions{}, nil, nil); !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("empty tenant: want ErrSessionInvalidArgument, got %v", err)
	}
	if _, err := uc.Issue(context.Background(), Identity{TenantID: "t-1", UserID: 0}, SessionOptions{}, nil, nil); !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("zero user: want ErrSessionInvalidArgument, got %v", err)
	}
	if repo.issueCalls != 0 {
		t.Fatalf("repo issue calls = %d, want 0", repo.issueCalls)
	}
}

func TestSessionUsecaseValidateHashesToken(t *testing.T) {
	repo := &fakeSessionRepo{session: &Session{ID: 1}}
	uc := NewSessionUsecase(repo)

	if _, err := uc.Validate(context.Background(), "plain-access-token"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if repo.validateCalls != 1 || repo.accessHash != hashSessionToken("plain-access-token") {
		t.Fatalf("repo got %q (calls=%d), want token hash", repo.accessHash, repo.validateCalls)
	}

	repo.validateCalls = 0
	if _, err := uc.Validate(context.Background(), ""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("empty token: want ErrSessionInvalid, got %v", err)
	}
	if repo.validateCalls != 0 {
		t.Fatalf("empty token reached repo (%d calls)", repo.validateCalls)
	}
}

func TestSessionUsecaseRefreshRotatesTokens(t *testing.T) {
	repo := &fakeSessionRepo{session: &Session{ID: 9}}
	uc := NewSessionUsecase(repo)

	out, err := uc.Refresh(context.Background(), "plain-refresh-token", SessionOptions{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	in := repo.refreshInput
	if in == nil {
		t.Fatal("repo received no refresh input")
	}
	if in.CurrentRefreshTokenHash != hashSessionToken("plain-refresh-token") {
		t.Fatal("repo must receive the current refresh token hash")
	}
	if in.NewAccessTokenHash != hashSessionToken(out.AccessToken) {
		t.Fatal("repo must receive the new access token hash")
	}
	if in.NewRefreshTokenHash != hashSessionToken(out.RefreshToken) {
		t.Fatal("repo must receive the new refresh token hash")
	}
	if out.AccessToken == "plain-refresh-token" || out.RefreshToken == "plain-refresh-token" || out.AccessToken == out.RefreshToken {
		t.Fatal("refresh must issue two independent new tokens")
	}
	if in.NewRefreshTokenHash == in.CurrentRefreshTokenHash {
		t.Fatal("refresh token must be rotated")
	}
	if in.AccessTTL != DefaultSessionAccessTTL || in.RefreshTTL != DefaultSessionRefreshTTL {
		t.Fatalf("refresh ttl = %v/%v, want defaults %v/%v", in.AccessTTL, in.RefreshTTL, DefaultSessionAccessTTL, DefaultSessionRefreshTTL)
	}

	repo.refreshCalls = 0
	if _, err := uc.Refresh(context.Background(), "", SessionOptions{}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("empty refresh token: want ErrSessionInvalid, got %v", err)
	}
	if repo.refreshCalls != 0 {
		t.Fatalf("empty refresh token reached repo (%d calls)", repo.refreshCalls)
	}
}

func TestSessionUsecaseRevoke(t *testing.T) {
	repo := &fakeSessionRepo{}
	uc := NewSessionUsecase(repo)
	auth := Identity{TenantID: "t-1", UserID: 7}

	if err := uc.Revoke(context.Background(), auth, 42); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if repo.revokeCalls != 1 || repo.revokeAuth != auth || repo.revokeID != 42 {
		t.Fatalf("repo revoke = (%+v, %d, calls=%d), want (%+v, 42)", repo.revokeAuth, repo.revokeID, repo.revokeCalls, auth)
	}

	if err := uc.Revoke(context.Background(), auth, 0); !errors.Is(err, ErrSessionInvalidArgument) {
		t.Fatalf("zero session id: want ErrSessionInvalidArgument, got %v", err)
	}
}

func TestSessionTokenShape(t *testing.T) {
	first, err := newSessionToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	second, err := newSessionToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if first == second {
		t.Fatal("two generated tokens must differ")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("token is not base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("token entropy = %d bytes, want 32", len(raw))
	}
	if hash := hashSessionToken(first); len(hash) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(hash))
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	auth := Identity{TenantID: "t-1", UserID: 7}
	ctx := WithIdentity(context.Background(), auth)

	got, ok := IdentityFromContext(ctx)
	if !ok || got != auth {
		t.Fatalf("auth from ctx = (%+v, %v), want %+v", got, ok, auth)
	}

	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Fatal("identity must be absent without injection")
	}
}
