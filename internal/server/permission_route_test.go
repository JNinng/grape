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
	"github.com/jninng/sqlc_gen_dynamic/dynq"
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
// featureFlag / tenant 非 nil 时挂载对应路由（放行路径用例用真实
// service + 替身仓储）；其余业务服务传 nil，本组用例不触达。
func newPermissionTestServer(t *testing.T, repo biz.PermissionRepo, featureFlag *service.FeatureFlagService, tenant *service.TenantService) *kratoshttp.Server {
	t.Helper()
	traceMiddleware, err := entrymw.NewTraceMiddleware(&conf.Server{})
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	permMiddleware := entrymw.NewPermissionMiddleware(biz.NewPermissionUsecase(repo))
	noop := func(h middleware.Handler) middleware.Handler { return h }
	srv, err := NewHTTPServer(
		&conf.Server{Http: &conf.Server_HTTP{}},
		nil,
		traceMiddleware,
		noop,
		authMiddleware,
		permMiddleware,
		ops.NewMetricsHandler(nil, nil),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		featureFlag,
		nil,
		// 租户管理服务由用例按需注入（nil 表示本组用例不触达租户路由）。
		tenant,
		service.NewPermissionService(biz.NewPermissionUsecase(repo)),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
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
	srv := newPermissionTestServer(t, repo, nil, nil)

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
	srv := newPermissionTestServer(t, repo, nil, nil)

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
	srv := newPermissionTestServer(t, repo, nil, nil)

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

// stubFeatureFlagListRepo 是功能开关放行路径用例的替身：List 返回空页，
// 其余方法不被触达（嵌入接口零值）。
type stubFeatureFlagListRepo struct{ biz.FeatureFlagRepo }

func (stubFeatureFlagListRepo) List(context.Context, biz.Identity, *dynq.Query) (*biz.FeatureFlagList, error) {
	return &biz.FeatureFlagList{}, nil
}

// permRoutePostDo 执行一次带可选令牌的 POST 空查询请求并返回响应。
func permRoutePostDo(t *testing.T, srv *kratoshttp.Server, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestConfigFamilyListRoutes_missing_token_returns_401 验证配置族四个 List
// 端点无令牌在认证层即 401（链序：认证先于权限判定，权限仓储零调用）。
func TestConfigFamilyListRoutes_missing_token_returns_401(t *testing.T) {
	repo := &stubPermissionRepo{}
	srv := newPermissionTestServer(t, repo, nil, nil)

	targets := []string{
		"/v1/configs/list",
		"/v1/tenant-configs/list",
		"/v1/feature-flags/list",
		"/v1/translations/list",
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			rec := permRoutePostDo(t, srv, target, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401\n%s", rec.Code, rec.Body.String())
			}
		})
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestFeatureFlagListRoute_granted_request_reaches_service 验证放行路径：
// 判定通过后请求到达真实 service（替身仓储返回空页），权限码按端点声明
// 透传。
func TestFeatureFlagListRoute_granted_request_reaches_service(t *testing.T) {
	repo := &stubPermissionRepo{granted: true}
	featureFlag := service.NewFeatureFlagService(biz.NewFeatureFlagUsecase(stubFeatureFlagListRepo{}))
	srv := newPermissionTestServer(t, repo, featureFlag, nil)

	rec := permRoutePostDo(t, srv, "/v1/feature-flags/list", "any-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 1 {
		t.Fatalf("has-permission calls = %d, want 1 (gated operation)", repo.hasPermCalls)
	}
}

// stubTenantListRepo 是租户放行路径用例的替身：List 返回空页，其余方法
// 不被触达（嵌入接口零值）。
type stubTenantListRepo struct{ biz.TenantRepo }

func (stubTenantListRepo) List(context.Context, biz.Identity, *dynq.Query) (*biz.TenantList, error) {
	return &biz.TenantList{}, nil
}

// TestTenantListRoute_missing_token_returns_401 验证租户 List 端点无令牌
// 在认证层即 401（链序：认证先于权限判定，权限仓储零调用）。
func TestTenantListRoute_missing_token_returns_401(t *testing.T) {
	repo := &stubPermissionRepo{}
	srv := newPermissionTestServer(t, repo, nil, nil)

	rec := permRoutePostDo(t, srv, "/v1/tenants/list", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestTenantRoutes_missing_token_returns_401 验证租户管理全部 6 个其余
// 端点（create/get/update/delete/disable/restore）无令牌在认证层即 401：
// 路由在册、经 ctx.Middleware 入链，权限仓储零调用。
func TestTenantRoutes_missing_token_returns_401(t *testing.T) {
	repo := &stubPermissionRepo{}
	srv := newPermissionTestServer(t, repo, nil, nil)

	cases := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodPost, "/v1/tenants/create", "{}"},
		{http.MethodGet, "/v1/tenants/t-test-02", ""},
		{http.MethodPut, "/v1/tenants/update", "{}"},
		{http.MethodDelete, "/v1/tenants/t-test-02", ""},
		{http.MethodPost, "/v1/tenants/t-test-02/disable", ""},
		{http.MethodPost, "/v1/tenants/t-test-02/restore", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401\n%s", rec.Code, rec.Body.String())
			}
		})
	}
	if repo.hasPermCalls != 0 {
		t.Fatalf("has-permission calls = %d, want 0", repo.hasPermCalls)
	}
}

// TestTenantListRoute_granted_request_reaches_service 验证放行路径：判定
// 通过后请求到达真实 service（替身仓储返回空页），端点声明的
// sys:tenant:query 被执行一次。
func TestTenantListRoute_granted_request_reaches_service(t *testing.T) {
	repo := &stubPermissionRepo{granted: true}
	tenant := service.NewTenantService(biz.NewTenantUsecase(stubTenantListRepo{}))
	srv := newPermissionTestServer(t, repo, nil, tenant)

	rec := permRoutePostDo(t, srv, "/v1/tenants/list", "any-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if repo.hasPermCalls != 1 {
		t.Fatalf("has-permission calls = %d, want 1 (gated operation)", repo.hasPermCalls)
	}
}
