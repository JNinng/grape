package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	sessionv1 "grape/api/session/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// fakeSessionRepo 只为 Validate 提供会话事实并记录哈希入参。
type fakeSessionRepo struct {
	session *biz.Session
	err     error

	validateCalls int
	accessHash    string
}

func (f *fakeSessionRepo) Issue(context.Context, biz.Identity, *biz.IssueSessionInput) (*biz.Session, error) {
	return nil, biz.ErrSessionInvalidArgument
}

func (f *fakeSessionRepo) Validate(_ context.Context, accessTokenHash string) (*biz.Session, error) {
	f.validateCalls++
	f.accessHash = accessTokenHash
	return f.session, f.err
}

func (f *fakeSessionRepo) Revoke(context.Context, biz.Identity, int64) error {
	return biz.ErrSessionInvalidArgument
}

func (f *fakeSessionRepo) RevokeByAccessTokenHash(context.Context, string) error {
	return biz.ErrSessionInvalidArgument
}

func (f *fakeSessionRepo) Refresh(context.Context, *biz.RefreshSessionInput) (*biz.Session, error) {
	return nil, biz.ErrSessionInvalid
}

// fakeTransporter / fakeHeader 提供中间件读取的传输事实：operation 与
// 请求头，模拟 Kratos HTTP/gRPC 服务端上下文。
type fakeTransporter struct {
	operation string
	header    transport.Header
}

func (f *fakeTransporter) Kind() transport.Kind            { return transport.KindHTTP }
func (f *fakeTransporter) Endpoint() string                { return "test://" }
func (f *fakeTransporter) Operation() string               { return f.operation }
func (f *fakeTransporter) RequestHeader() transport.Header { return f.header }
func (f *fakeTransporter) ReplyHeader() transport.Header   { return f.header }

type fakeHeader map[string]string

func (h fakeHeader) Get(key string) string      { return h[key] }
func (h fakeHeader) Set(key, value string)      { h[key] = value }
func (h fakeHeader) Add(key, value string)      { h[key] = value }
func (h fakeHeader) Keys() []string             { return nil }
func (h fakeHeader) Values(key string) []string { return nil }

// serverContext 把传输事实放进 ctx，等价于 Kratos 服务端入口的构造。
func serverContext(ctx context.Context, operation string, header transport.Header) context.Context {
	return transport.NewServerContext(ctx, &fakeTransporter{operation: operation, header: header})
}

// invoke 以给定 ctx 跑通中间件，返回标记 handler 是否执行。
func invoke(ctx context.Context, m middleware.Middleware) (called bool, err error) {
	_, err = m(func(ctx context.Context, req any) (any, error) {
		called = true
		return nil, nil
	})(ctx, struct{}{})
	return called, err
}

func TestAuthMiddlewarePublicOperationSkipsValidation(t *testing.T) {
	repo := &fakeSessionRepo{}
	m := NewAuthMiddleware(biz.NewSessionUsecase(repo))

	for _, op := range []string{
		sessionv1.OperationSessionServiceLogin,
		sessionv1.OperationSessionServiceRefresh,
		sessionv1.OperationSessionServiceLogout,
	} {
		called, err := invoke(serverContext(context.Background(), op, fakeHeader{}), m)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if !called {
			t.Fatalf("%s: handler must run without a token", op)
		}
	}
	if repo.validateCalls != 0 {
		t.Fatalf("validate calls = %d, want 0", repo.validateCalls)
	}
}

func TestAuthMiddlewareDeniesWithoutTransport(t *testing.T) {
	repo := &fakeSessionRepo{}
	m := NewAuthMiddleware(biz.NewSessionUsecase(repo))

	// 无传输上下文时中间件读不到 operation，端点是否公开无从判定，
	// 一律按未认证拒绝。
	called, err := invoke(context.Background(), m)
	if called {
		t.Fatal("handler must not run without a transport")
	}
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.validateCalls != 0 {
		t.Fatalf("validate calls = %d, want 0", repo.validateCalls)
	}
}

func TestAuthMiddlewareRejectsMissingOrMalformedToken(t *testing.T) {
	repo := &fakeSessionRepo{}
	m := NewAuthMiddleware(biz.NewSessionUsecase(repo))
	op := userv1.OperationUserServiceGetUser

	for name, header := range map[string]fakeHeader{
		"missing":      {},
		"other scheme": {"Authorization": "Basic dXNlcjpwYXNz"},
		"empty token":  {"Authorization": "Bearer "},
		"bearer only":  {"Authorization": "Bearer"},
	} {
		called, err := invoke(serverContext(context.Background(), op, header), m)
		if called {
			t.Fatalf("%s: handler must not run on rejection", name)
		}
		if err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
	if repo.validateCalls != 0 {
		t.Fatalf("validate calls = %d, want 0", repo.validateCalls)
	}
}

func TestAuthMiddlewareInjectsSessionIdentity(t *testing.T) {
	token := "test-access-token"
	repo := &fakeSessionRepo{session: &biz.Session{ID: 42, TenantID: "t-1", UserID: 7}}
	m := NewAuthMiddleware(biz.NewSessionUsecase(repo))

	var got biz.Identity
	_, err := m(func(ctx context.Context, req any) (any, error) {
		auth, ok := biz.IdentityFromContext(ctx)
		if !ok {
			t.Fatal("identity missing in handler context")
		}
		got = auth
		return nil, nil
	})(serverContext(context.Background(), userv1.OperationUserServiceGetUser, fakeHeader{
		"Authorization": "Bearer " + token,
	}), struct{}{})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if want := (biz.Identity{TenantID: "t-1", UserID: 7}); got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
	sum := sha256.Sum256([]byte(token))
	if repo.accessHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("repo got hash %q, want sha256 hex of token", repo.accessHash)
	}
}

func TestAuthMiddlewarePropagatesInvalidToken(t *testing.T) {
	repo := &fakeSessionRepo{err: biz.ErrSessionInvalid}
	m := NewAuthMiddleware(biz.NewSessionUsecase(repo))

	called, err := invoke(serverContext(context.Background(), userv1.OperationUserServiceGetUser, fakeHeader{
		"Authorization": "Bearer stale-token",
	}), m)
	if called {
		t.Fatal("handler must not run when validation fails")
	}
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
}
