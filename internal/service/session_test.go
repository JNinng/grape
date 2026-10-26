package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	sessionv1 "grape/api/session/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/transport"
)

// fakeSessionRepo 只为登出捕获用例交给仓储的令牌哈希。仓储契约对未知
// 令牌/已撤销会话幂等返回 nil，替身以此形态模拟。
type fakeSessionRepo struct {
	biz.SessionRepo

	logoutCalls int
	logoutHash  string
}

func (f *fakeSessionRepo) RevokeByAccessTokenHash(_ context.Context, accessTokenHash string) error {
	f.logoutCalls++
	f.logoutHash = accessTokenHash
	return nil
}

// fakeTransporter / fakeHeader 提供 Logout 读取的传输事实：请求头，模拟
// Kratos HTTP/gRPC 服务端上下文（形态同认证中间件测试）。
type fakeTransporter struct {
	header transport.Header
}

func (f *fakeTransporter) Kind() transport.Kind            { return transport.KindHTTP }
func (f *fakeTransporter) Endpoint() string                { return "test://" }
func (f *fakeTransporter) Operation() string               { return sessionv1.OperationSessionServiceLogout }
func (f *fakeTransporter) RequestHeader() transport.Header { return f.header }
func (f *fakeTransporter) ReplyHeader() transport.Header   { return f.header }

type fakeHeader map[string]string

func (h fakeHeader) Get(key string) string      { return h[key] }
func (h fakeHeader) Set(key, value string)      { h[key] = value }
func (h fakeHeader) Add(key, value string)      { h[key] = value }
func (h fakeHeader) Keys() []string             { return nil }
func (h fakeHeader) Values(key string) []string { return nil }

func logoutContext(header transport.Header) context.Context {
	return transport.NewServerContext(context.Background(), &fakeTransporter{header: header})
}

func TestSessionServiceLogoutForwardsTokenHash(t *testing.T) {
	repo := &fakeSessionRepo{}
	svc := NewSessionService(nil, biz.NewSessionUsecase(repo))

	out, err := svc.Logout(logoutContext(fakeHeader{"Authorization": "Bearer test-access-token"}), &sessionv1.LogoutRequest{})
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if out == nil {
		t.Fatal("logout must return an empty reply")
	}
	sum := sha256.Sum256([]byte("test-access-token"))
	if repo.logoutCalls != 1 || repo.logoutHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("repo got %q (calls=%d), want sha256 hex of token", repo.logoutHash, repo.logoutCalls)
	}
}

func TestSessionServiceLogoutRejectsMissingOrMalformedHeader(t *testing.T) {
	repo := &fakeSessionRepo{}
	svc := NewSessionService(nil, biz.NewSessionUsecase(repo))

	for name, header := range map[string]fakeHeader{
		"missing":      {},
		"other scheme": {"Authorization": "Basic dXNlcjpwYXNz"},
		"empty token":  {"Authorization": "Bearer "},
		"bearer only":  {"Authorization": "Bearer"},
	} {
		_, err := svc.Logout(logoutContext(header), &sessionv1.LogoutRequest{})
		if !errors.Is(err, biz.ErrSessionInvalid) {
			t.Fatalf("%s: want ErrSessionInvalid, got %v", name, err)
		}
	}
	if repo.logoutCalls != 0 {
		t.Fatalf("usecase reached repo on rejection (%d calls)", repo.logoutCalls)
	}
}

func TestSessionServiceLogoutIdempotentWhenSessionGone(t *testing.T) {
	// 替身按仓储契约对已撤销/不存在的会话幂等返回 nil：重放登出必须
	// 同样成功，不把无操作转成错误。
	repo := &fakeSessionRepo{}
	svc := NewSessionService(nil, biz.NewSessionUsecase(repo))
	req := &sessionv1.LogoutRequest{}
	header := fakeHeader{"Authorization": "Bearer replayed-token"}

	for i := 0; i < 2; i++ {
		if _, err := svc.Logout(logoutContext(header), req); err != nil {
			t.Fatalf("replay %d: logout must stay idempotent, got %v", i+1, err)
		}
	}
	if repo.logoutCalls != 2 {
		t.Fatalf("repo calls = %d, want 2", repo.logoutCalls)
	}
}
