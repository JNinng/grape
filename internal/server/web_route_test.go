package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/web"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/jninng/trpc_agent_go_ack/acktest"
)

// webTestFS 提供最小 SPA 产物形态：入口页 + 内容哈希 assets 文件。
func webTestFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>spa-index</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('app')")},
	}
}

// newWebTestServer 装配带静态托管的 HTTP 服务器：业务服务为 nil，只验证
// 路由形态与挂载语义。reserved 由用例给定，便于单独验证边界匹配。
func newWebTestServer(t *testing.T, dist fs.FS, reserved []string) *kratoshttp.Server {
	t.Helper()
	traceMiddleware, err := entrymw.NewTraceMiddleware(&conf.Server{})
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	noop := func(h middleware.Handler) middleware.Handler { return h }
	srv, err := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, nil, traceMiddleware,
		noop, authMiddleware, ops.NewMetricsHandler(nil, nil), nil, nil, nil, nil, nil, nil, nil,
		WebHandler(newWebFileHandler(dist, reserved)))
	if err != nil {
		panic("build http server: " + err.Error())
	}
	return srv
}

// webDo 执行一次请求并返回响应。
func webDo(t *testing.T, srv *kratoshttp.Server, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestWebRootServesIndexWithoutToken 验证根路径出页面壳且免认证：静态
// 托管不经业务中间件链，无令牌也可访问（登录页本身即页面壳）。
func TestWebRootServesIndexWithoutToken(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("root status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("root body missing index content:\n%s", rec.Body.String())
	}
}

// TestWebSPARouteRefreshServesIndex 验证 SPA 客户端路由刷新不 404：
// 非文件路径一律回退 index.html。
func TestWebSPARouteRefreshServesIndex(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/some/route", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("spa route status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("spa route body missing index content:\n%s", rec.Body.String())
	}
}

// TestWebAssetServesFileWithImmutableCache 验证 assets 命中文件直出并带
// 长缓存头（Vite 产物内容哈希，文件名不可变）。
func TestWebAssetServesFileWithImmutableCache(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/assets/app.js", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "console.log('app')") {
		t.Fatalf("asset body missing file content:\n%s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q, want immutable long cache", got)
	}
}

// TestWebUnknownAPIPathReturns404NotIndex 验证未知 API 路径不返回页面壳：
// 保留前缀内未命中生成路由的路径按 API 语义 404。
func TestWebUnknownAPIPathReturns404NotIndex(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/v1/nonexistent", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown api path status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("unknown api path must not serve index:\n%s", rec.Body.String())
	}
}

// TestWebReservedPrefixWithMethodMismatchReturns404 验证保留前缀判定与
// 方法无关：POST 打在未注册的 /v1 路径上同样 404，不落页面壳。
func TestWebReservedPrefixWithMethodMismatchReturns404(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodPost, "/v1/nonexistent", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown api path (post) status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("unknown api path must not serve index:\n%s", rec.Body.String())
	}
}

// TestWebAdjacentPrefixFallsBackToIndex 验证保留前缀按边界匹配：/v1x
// 不在 /v1 名单内，正常回退页面壳。
func TestWebAdjacentPrefixFallsBackToIndex(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/v1x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("adjacent prefix status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("adjacent prefix body missing index content:\n%s", rec.Body.String())
	}
}

// TestWebNonGetOnStaticPathReturns405 静态路径只有读语义：非 GET/HEAD
// 返回 405 并带 Allow 头。
func TestWebNonGetOnStaticPathReturns405(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodPost, "/", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post root status = %d, want 405\n%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("allow header = %q, want GET, HEAD", got)
	}
}

// TestWebMetricsRouteNotSwallowedByStatic 验证运维出口先于静态兜底：
// /metrics 命中直挂路由返回指标文本，不回退页面壳。
func TestWebMetricsRouteNotSwallowedByStatic(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("metrics route must not serve index:\n%s", rec.Body.String())
	}
}

// TestWebProtectedAPIWithoutTokenReturns401 验证无令牌 API 仍 401：生成
// 路由先于静态兜底命中，认证中间件在链上拒绝，静态不介入。
func TestWebProtectedAPIWithoutTokenReturns401(t *testing.T) {
	srv := newWebTestServer(t, webTestFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/v1/users/1", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected api without token status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "spa-index") {
		t.Fatalf("protected api must not serve index:\n%s", rec.Body.String())
	}
}

// TestWebAgentPrefixWithoutTokenNotSwallowedByStatic 在真实 Agent 挂载
// （含静态托管）下验证 /agui/* 先于 SPA fallback：无令牌 401 而非页面壳。
func TestWebAgentPrefixWithoutTokenNotSwallowedByStatic(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodGet, "/agui/ack/confirmations?threadId=thread-1", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("agui without token status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "前端未构建") {
		t.Fatalf("agui route must not serve placeholder index:\n%s", rec.Body.String())
	}
}

// TestWebEmbeddedPlaceholderDistServesIndex 验证仓库占位产物可服务：
// 未执行 npm 构建时根路径返回占位页而非 404。
func TestWebEmbeddedPlaceholderDistServesIndex(t *testing.T) {
	srv := newWebTestServer(t, web.DistFS(), ReservedAPIPrefixes(nil))
	rec := webDo(t, srv, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("placeholder root status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "前端未构建") {
		t.Fatalf("placeholder root body missing notice:\n%s", rec.Body.String())
	}
}
