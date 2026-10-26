package server

import (
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/agent"
	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/transport/http"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"google.golang.org/protobuf/types/known/durationpb"
)

// corsHTTPConf 返回 CORS 启用的服务器配置：白名单单源 + 1h 预检缓存。
func corsHTTPConf() *conf.Server {
	c := agentHTTPConf()
	c.Http.Cors = &conf.Server_CORS{
		Enabled:        true,
		AllowedOrigins: []string{"https://app.example.com"},
		MaxAge:         durationpb.New(time.Hour),
	}
	return c
}

// corsDo 执行带自定义头的请求并返回响应（agentDo 不支持 Origin 头）。
func corsDo(t *testing.T, srv *agentTestServer, req *nethttp.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.server.ServeHTTP(rec, req)
	return rec
}

// agentTestServer 是 cors 用例的最小服务器持有物。
type agentTestServer struct {
	server *http.Server
}

// Test_cors_preflight_on_agent_route_short_circuits_in_chain 验证 agent
// 前缀路由（method 通配）的预检在链内短路：204 + Allow-Origin + 回显
// 方法/请求头 + Max-Age，不触达认证（无令牌也不 401）。
func Test_cors_preflight_on_agent_route_short_circuits_in_chain(t *testing.T) {
	srv := newCorsTestServer(t, corsHTTPConf())
	req := httptest.NewRequest(nethttp.MethodOptions, "/agui/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	rec := corsDo(t, srv, req)

	if rec.Code != nethttp.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204\n%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow-origin = %q, want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "POST" {
		t.Fatalf("allow-methods = %q, want echoed POST", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "authorization, content-type" {
		t.Fatalf("allow-headers = %q, want echoed request headers", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "3600" {
		t.Fatalf("max-age = %q, want 3600", got)
	}
	if !headerContainsToken(rec.Header().Get("Access-Control-Expose-Headers"), "x-trace-id") {
		t.Fatal("expose headers must contain x-trace-id")
	}
}

// Test_cors_preflight_on_generated_route_via_405_fallback 验证生成代码
// 路由的预检经 gorilla/mux 405 出口由兜底 handler 应答（OPTIONS 与注册
// 方法不匹配进不了中间件链）。
func Test_cors_preflight_on_generated_route_via_405_fallback(t *testing.T) {
	srv := newCorsTestServer(t, corsHTTPConf())
	req := httptest.NewRequest(nethttp.MethodOptions, "/v1/users/list", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := corsDo(t, srv, req)

	if rec.Code != nethttp.StatusNoContent {
		t.Fatalf("generated route preflight status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow-origin = %q, want echoed origin", got)
	}
	if !headerContainsToken(rec.Header().Get("Access-Control-Expose-Headers"), "x-trace-id") {
		t.Fatal("expose headers must contain x-trace-id")
	}
}

// Test_cors_rejected_origin_gets_no_allow_origin 验证白名单外来源不带
// Allow-Origin（浏览器侧自拒），请求本身照常处理。
func Test_cors_rejected_origin_gets_no_allow_origin(t *testing.T) {
	srv := newCorsTestServer(t, corsHTTPConf())
	req := httptest.NewRequest(nethttp.MethodPost, "/agui/", strings.NewReader(agentChatRequest("thread-1")))
	req.Header.Set("Origin", "https://evil.example.net")
	req.Header.Set("Content-Type", "application/json")
	rec := corsDo(t, srv, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("rejected origin got allow-origin %q", got)
	}
	if !headerContainsToken(rec.Header().Get("Access-Control-Expose-Headers"), "x-trace-id") {
		t.Fatal("expose headers must contain x-trace-id on plain requests too")
	}
}

// Test_cors_disabled_outputs_no_cors_headers 验证默认关闭：不输出任何
// CORS 头（含暴露头）。
func Test_cors_disabled_outputs_no_cors_headers(t *testing.T) {
	srv := newCorsTestServer(t, agentHTTPConf())
	req := httptest.NewRequest(nethttp.MethodPost, "/agui/", strings.NewReader(agentChatRequest("thread-1")))
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Content-Type", "application/json")
	rec := corsDo(t, srv, req)
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Expose-Headers"} {
		if got := rec.Header().Get(h); got != "" {
			t.Fatalf("cors disabled but %s = %q", h, got)
		}
	}
}

// Test_new_http_server_rejects_enabled_cors_without_origins 验证配置
// 错误在装配期暴露：enabled 但白名单为空，拒绝启动。
func Test_new_http_server_rejects_enabled_cors_without_origins(t *testing.T) {
	confServer := agentHTTPConf()
	confServer.Http.Cors = &conf.Server_CORS{Enabled: true}
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	_, err = NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, nil), nil, ops.NewMetricsHandler(nil), nil, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "allowed_origins") {
		t.Fatalf("enabled cors without origins must fail with origins error, got %v", err)
	}
}

// headerContainsToken 判定逗号分隔头清单是否含指定 token（大小写不敏感）。
func headerContainsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// newCorsTestServer 以给定配置装配完整 HTTP 服务器（CORS 行为走真链）。
func newCorsTestServer(t *testing.T, confServer *conf.Server) *agentTestServer {
	t.Helper()
	svc, closeSessions, err := agent.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc, agent.NewVectorStore(nil),
		biz.NewUserUsecase(&agentStubUserRepo{}), nil, agent.WithModelOverride(acktest.NewScriptedModel("stub")))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	t.Cleanup(stack.Close)
	agentService := service.NewAgentService(stack)
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	srv, err := NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, agentService),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, agentService, nil, nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	return &agentTestServer{server: srv}
}
