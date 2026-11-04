package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// stubPermissionRepo 是权限判定中间件路由测试的替身：恒判定持有、固定
// 权限码集合，并记录 HasPermission 调用次数供链路断言。
type stubPermissionRepo struct {
	codes        []string
	granted      bool
	hasPermCalls int
}

func (r *stubPermissionRepo) HasPermission(_ context.Context, _ biz.Identity, _ string) (bool, error) {
	r.hasPermCalls++
	return r.granted, nil
}

func (r *stubPermissionRepo) ListCodes(context.Context, biz.Identity) ([]string, error) {
	return r.codes, nil
}

// permStubMiddleware 返回恒放行的权限判定中间件：既有路由测试只验证
// 路由形态与挂载语义，受控端点不触达权限语义。
func permStubMiddleware() entrymw.PermissionMiddleware {
	return entrymw.NewPermissionMiddleware(biz.NewPermissionUsecase(&stubPermissionRepo{granted: true}))
}

// newPermissionTestServer 装配最小 HTTP 服务器：仅注册权限码查询端点
// （真实 service 形态）与用户路由（nil 实现——判定不通过时请求不得到
// 达 handler，触达即 panic 经 recovery 变 500，测试以状态码暴露）。
func newPermissionTestServer(t *testing.T, repo biz.PermissionRepo) *kratoshttp.Server {
	t.Helper()
	traceMiddleware, err := entrymw.NewTraceMiddleware(&conf.Server{})
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	permMiddleware := entrymw.NewPermissionMiddleware(biz.NewPermissionUsecase(repo))
	noop := func(h middleware.Handler) middleware.Handler { return h }
	srv, err := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, nil, traceMiddleware,
		noop, authMiddleware, permMiddleware, ops.NewMetricsHandler(nil, nil),
		nil, nil, nil, nil, nil,
		service.NewPermissionService(biz.NewPermissionUsecase(repo)),
		nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return srv
}

// permRouteDo 执行一次带可选令牌的请求并返回响应。
func permRouteDo(t *testing.T, srv *kratoshttp.Server, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestPermissionRoute_missing_token_returns_401_before_permission 验证
// 链序：认证先于权限判定，无令牌在认证层即 401，权限仓储零调用。
func TestPermissionRoute_missing_token_returns_401_before_permission(t *testing.T) {
	repo := &stubPermissionRepo{}
	srv := newPermissionTestServer(t, repo)

	rec := permRouteDo(t, srv, "/v1/users/1", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestPermissionRoute_no_permission_returns_403_with_perm_reason 验证
// 受控端点无权限的兜底语义：认证通过但权限码不持有 → 403 +
// PERM_ACCESS_DENIED，且不触达业务 handler（nil 实现，触达即 500）。
func TestPermissionRoute_no_permission_returns_403_with_perm_reason(t *testing.T) {
	repo := &stubPermissionRepo{granted: false}
	srv := newPermissionTestServer(t, repo)

	rec := permRouteDo(t, srv, "/v1/users/1", "any-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PERM_ACCESS_DENIED") {
		t.Fatalf("body missing PERM_ACCESS_DENIED reason:\n%s", rec.Body.String())
	}
}

// TestPermissionRoute_granted_request_reaches_permission_service 验证
// 放行链路：码表外端点（权限码查询本身）不查判定仓储、直达到 service
// 返回权限码集合——导航过滤端点的路由级契约。
func TestPermissionRoute_granted_request_reaches_permission_service(t *testing.T) {
	repo := &stubPermissionRepo{granted: true, codes: []string{"sys:user:query", "sys:user:create"}}
	srv := newPermissionTestServer(t, repo)

	rec := permRouteDo(t, srv, "/v1/permissions/mine", "any-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, `"codes":["sys:user:query","sys:user:create"]`) {
		t.Fatalf("body = %s, want sorted codes array", got)
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0 (ungated operation)", repo.hasPermCalls)
	}
}
