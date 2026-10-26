package middleware

import (
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/conf"

	"github.com/go-kratos/kratos/v3/transport"
	"google.golang.org/protobuf/types/known/durationpb"
)

// corsConf 构造启用态 CORS 配置；origins 逐条传入。
func corsConf(origins []string, maxAge time.Duration) *conf.Server {
	c := &conf.Server{Http: &conf.Server_HTTP{
		Cors: &conf.Server_CORS{Enabled: true, AllowedOrigins: origins},
	}}
	if maxAge > 0 {
		c.Http.Cors.MaxAge = durationpb.New(maxAge)
	}
	return c
}

// runCORS 跑通链内 CORS 中间件：返回 handler 是否被调用。
func runCORS(t *testing.T, cr *CORS, tr transport.Transporter) bool {
	t.Helper()
	var called bool
	_, err := cr.Middleware()(func(ctx context.Context, req any) (any, error) {
		called = true
		return nil, nil
	})(transport.NewServerContext(context.Background(), tr), struct{}{})
	if err != nil {
		t.Fatalf("cors middleware: %v", err)
	}
	return called
}

func Test_new_cors_disabled_returns_nil_without_error(t *testing.T) {
	cr, err := NewCORS(&conf.Server{Http: &conf.Server_HTTP{}})
	if err != nil {
		t.Fatalf("disabled cors construction: %v", err)
	}
	if cr != nil {
		t.Fatal("disabled cors must be nil")
	}
}

func Test_new_cors_enabled_without_origins_is_rejected(t *testing.T) {
	if _, err := NewCORS(corsConf(nil, 0)); err == nil {
		t.Fatal("enabled cors without origins must be rejected")
	}
}

func Test_new_cors_rejects_malformed_origin_entries(t *testing.T) {
	for name, origin := range map[string]string{
		"schemeless host": "app.example.com",
		"with path":       "https://app.example.com/base",
		"with query":      "https://app.example.com/?a=1",
		"with userinfo":   "https://user@app.example.com",
		"with fragment":   "https://app.example.com/#frag",
		"bad port":        "https://app.example.com:notaport",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewCORS(corsConf([]string{origin}, 0)); err == nil {
				t.Fatalf("origin %q must be rejected", origin)
			}
		})
	}
}

func Test_new_cors_accepts_wildcard_and_valid_origins(t *testing.T) {
	origins := []string{"*", "https://app.example.com", "http://localhost:5173", "https://host:443"}
	if _, err := NewCORS(corsConf(origins, time.Hour)); err != nil {
		t.Fatalf("valid origins rejected: %v", err)
	}
}

func Test_cors_middleware_exposes_trace_id_header_on_plain_request(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"https://app.example.com"}, 0))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	tr := newHTTPTraceTransporter("10.0.0.1:5000", fakeHeader{"Origin": "https://app.example.com"})
	if !runCORS(t, cr, tr) {
		t.Fatal("plain request must reach handler")
	}
	if got := tr.ReplyHeader().Get("Access-Control-Expose-Headers"); got != corsExposeHeaders {
		t.Fatalf("expose headers = %q, want %q", got, corsExposeHeaders)
	}
	if !containsToken(tr.ReplyHeader().Get("Access-Control-Expose-Headers"), "x-trace-id") {
		t.Fatal("expose headers must contain x-trace-id")
	}
}

func Test_cors_middleware_echoes_allowed_origin_and_vary(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"https://app.example.com"}, 0))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	tr := newHTTPTraceTransporter("10.0.0.1:5000", fakeHeader{"Origin": "https://app.example.com"})
	runCORS(t, cr, tr)
	if got := tr.ReplyHeader().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("allow-origin = %q, want echoed origin", got)
	}
	if tr.ReplyHeader().Get("Vary") != "Origin" {
		t.Fatal("Vary: Origin missing")
	}
}

func Test_cors_middleware_omits_allow_origin_for_unknown_origin(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"https://app.example.com"}, 0))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	tr := newHTTPTraceTransporter("10.0.0.1:5000", fakeHeader{"Origin": "https://evil.example.net"})
	runCORS(t, cr, tr)
	if got := tr.ReplyHeader().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unknown origin got allow-origin %q, want empty", got)
	}
}

func Test_cors_middleware_short_circuits_preflight_without_reaching_handler(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"*"}, time.Hour))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	// 请求头键按查找键原样存（fakeHeader 与真实 carrier 的对齐口径，
	// 见 auth_test 既有用法）；方法经 Request() 供 isPreflight 判定。
	req := httptest.NewRequest(nethttp.MethodOptions, "/agui/", nil)
	tr := newHTTPTraceTransporter("10.0.0.1:5000", fakeHeader{
		"Origin":                         "https://app.example.com",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization, content-type",
	})
	tr.request = req
	if runCORS(t, cr, tr) {
		t.Fatal("preflight must not reach handler")
	}
	if got := tr.ReplyHeader().Get("Access-Control-Allow-Methods"); got != "POST" {
		t.Fatalf("allow-methods = %q, want echoed POST", got)
	}
	if got := tr.ReplyHeader().Get("Access-Control-Allow-Headers"); got != "authorization, content-type" {
		t.Fatalf("allow-headers = %q, want echoed request headers", got)
	}
	if got := tr.ReplyHeader().Get("Access-Control-Max-Age"); got != "3600" {
		t.Fatalf("max-age = %q, want 3600", got)
	}
}

func Test_cors_middleware_passes_non_preflight_options_through(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"*"}, 0))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	req := httptest.NewRequest(nethttp.MethodOptions, "/agui/", nil) // 无 ACRM 头
	tr := newHTTPTraceTransporter("10.0.0.1:5000", fakeHeader{"Origin": "https://app.example.com"})
	tr.request = req
	if !runCORS(t, cr, tr) {
		t.Fatal("non-preflight OPTIONS must reach handler")
	}
}

// Test_cors_method_not_allowed_answers_preflight_falls_back_otherwise 守卫
// 生成代码路由的预检兜底：预检 204 应答且不进 fallback，其余请求原样回落。
func Test_cors_method_not_allowed_answers_preflight_falls_back_otherwise(t *testing.T) {
	cr, err := NewCORS(corsConf([]string{"https://app.example.com"}, 0))
	if err != nil {
		t.Fatalf("new cors: %v", err)
	}
	fallbackCalled := false
	fallback := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		fallbackCalled = true
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(nethttp.MethodOptions, "/v1/users/list", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	cr.MethodNotAllowed(fallback).ServeHTTP(w, req)
	if fallbackCalled {
		t.Fatal("preflight must not reach fallback")
	}
	if w.Code != nethttp.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatal("preflight allow-origin missing")
	}
	if !containsToken(w.Header().Get("Access-Control-Expose-Headers"), "x-trace-id") {
		t.Fatal("preflight expose headers must contain x-trace-id")
	}

	w2 := httptest.NewRecorder()
	cr.MethodNotAllowed(fallback).ServeHTTP(w2, httptest.NewRequest(nethttp.MethodDelete, "/v1/users/list", nil))
	if !fallbackCalled {
		t.Fatal("non-preflight 405 must fall back")
	}
}

// containsToken 判定逗号分隔头清单是否含指定 token（大小写不敏感）。
func containsToken(header, token string) bool {
	for _, part := range splitAndTrim(header) {
		if strings.EqualFold(part, token) {
			return true
		}
	}
	return false
}

func splitAndTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
