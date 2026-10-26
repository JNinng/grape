package middleware

import (
	"context"
	"fmt"
	"net"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"grape/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// corsExposeHeaders 是跨域暴露头清单：TODO E1 明文要求——配 CORS 时
// x-trace-id 必须对浏览器脚本可读；x-request-id 与 trace-id 同源回传，
// 一并暴露。
const corsExposeHeaders = "x-trace-id, x-request-id"

// CORS 是解析后的跨域共享配置：链内中间件（Middleware）管实际请求的
// CORS 头与 agent 前缀路由的预检短路，MethodNotAllowed 兜底生成代码
// 路由的预检（OPTIONS 与注册方法不匹配在 gorilla/mux 层即被 405 拦下、
// 进不了中间件链）。配置关闭时 NewCORS 返回 nil，server 据此跳过装配，
// 不输出任何 CORS 头。
type CORS struct {
	allowAny bool
	origins  map[string]struct{}
	maxAge   time.Duration
}

// NewCORS 按解析 server.http.cors 配置。enabled=false 返回 (nil, nil)；
// enabled=true 时 allowed_origins 必填，条目须为 "*" 或合法 origin
// （scheme://host[:port]，不带 path/query/凭据），装配失败即拒绝启动，
// 与链路中间件的配置错误口径一致。
func NewCORS(c *conf.Server) (*CORS, error) {
	cc := c.GetHttp().GetCors()
	if !cc.GetEnabled() {
		return nil, nil
	}
	cors := &CORS{origins: make(map[string]struct{})}
	listed := 0
	for _, o := range cc.GetAllowedOrigins() {
		if o == "*" {
			cors.allowAny = true
			continue
		}
		if err := validateOrigin(o); err != nil {
			return nil, err
		}
		cors.origins[o] = struct{}{}
		listed++
	}
	if listed == 0 && !cors.allowAny {
		return nil, fmt.Errorf("cors: allowed_origins is required when enabled")
	}
	cors.maxAge = cc.GetMaxAge().AsDuration()
	return cors, nil
}

// originAllowed 判定请求 Origin 是否放行。
func (cr *CORS) originAllowed(origin string) bool {
	if cr.allowAny {
		return true
	}
	_, ok := cr.origins[origin]
	return ok
}

// Middleware 返回链内 CORS 中间件：
//   - 启用即恒回 Access-Control-Expose-Headers（E1：x-trace-id 必须可读），
//     Origin 命中白名单时回 Allow-Origin（回显具体 origin，兼容携带
//     Authorization 的请求）与 Vary: Origin（防缓存串答）；未命中不带
//     Allow-Origin，由浏览器侧自拒；
//   - 预检（OPTIONS 且带 Access-Control-Request-Method）短路 204，不进
//     认证与其后的任何链环；方法与请求头按预检请求回显。
//
// 仅手写通配路由（agent 前缀）的 OPTIONS 能进链，生成代码路由的预检由
// MethodNotAllowed 兜底；非预检请求一律放行。预检不经过链路中间件，
// 故不携带 x-trace-id——预检不是业务请求，浏览器亦不从预检读业务头。
func (cr *CORS) Middleware() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}
			origin := tr.RequestHeader().Get("Origin")
			if cr.originAllowed(origin) {
				tr.ReplyHeader().Set("Access-Control-Allow-Origin", origin)
				tr.ReplyHeader().Set("Vary", "Origin")
			}
			tr.ReplyHeader().Set("Access-Control-Expose-Headers", corsExposeHeaders)
			if isPreflight(tr) {
				cr.replyPreflight(tr)
				// 204 直写底层 ResponseWriter：手写通配路由的注册形态
				// 在中间件返回后不再编码响应，无二次写头。
				if w, ok := kratoshttp.ResponseWriterFromServerContext(ctx); ok {
					w.WriteHeader(nethttp.StatusNoContent)
				}
				return nil, nil
			}
			return handler(ctx, req)
		}
	}
}

// MethodNotAllowed 包装 gorilla/mux 层的 405 出口：预检在此应答（含
// CORS 头与 204），其余请求回落既有行为（kratos 默认 http.DefaultServeMux）。
func (cr *CORS) MethodNotAllowed(fallback nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if isPreflightRequest(r.Method, r.Header.Get("Access-Control-Request-Method")) {
			if cr.originAllowed(r.Header.Get("Origin")) {
				w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Expose-Headers", corsExposeHeaders)
			cr.setPreflightEcho(hset(w.Header()), r.Header.Get("Access-Control-Request-Method"),
				r.Header.Get("Access-Control-Request-Headers"))
			w.WriteHeader(nethttp.StatusNoContent)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

// hset 把 nethttp.Header 适配为写函数形态（与 transport.Header 共用逻辑）。
func hset(h nethttp.Header) func(string, string) {
	return func(key, value string) { h.Set(key, value) }
}

// isPreflight 判定链内请求是否浏览器预检：OPTIONS + Access-Control-
// Request-Method 双条件，缺一不为预检；取不到 HTTP 请求对象（非 HTTP
// 形态传输）视为非预检。
func isPreflight(tr transport.Transporter) bool {
	type requestGetter interface{ Request() *nethttp.Request }
	rg, ok := tr.(requestGetter)
	if !ok || rg.Request() == nil {
		return false
	}
	return isPreflightRequest(rg.Request().Method, tr.RequestHeader().Get("Access-Control-Request-Method"))
}

// isPreflightRequest 是双条件的纯判定（两条路径共用）。
func isPreflightRequest(method, requestMethod string) bool {
	return method == nethttp.MethodOptions && requestMethod != ""
}

// replyPreflight 应答链内预检：方法与请求头按预检请求回显——「请求什么
// 放行什么」比静态清单更贴合多端接入，避免清单漂移拒真。
func (cr *CORS) replyPreflight(tr transport.Transporter) {
	h := tr.ReplyHeader()
	cr.setPreflightEcho(func(key, value string) { h.Set(key, value) },
		tr.RequestHeader().Get("Access-Control-Request-Method"),
		tr.RequestHeader().Get("Access-Control-Request-Headers"))
}

// setPreflightEcho 写预检回显头与可选 Max-Age，两条路径共用。
func (cr *CORS) setPreflightEcho(set func(key, value string), requestMethod, requestHeaders string) {
	set("Access-Control-Allow-Methods", requestMethod)
	if requestHeaders != "" {
		set("Access-Control-Allow-Headers", requestHeaders)
	}
	if cr.maxAge > 0 {
		set("Access-Control-Max-Age", strconv.FormatInt(int64(cr.maxAge/time.Second), 10))
	}
}

// validateOrigin 校验 origin 条目形态：必须 scheme://host[:port]，不带
// path/query/fragment/userinfo——CORS 的 Origin 头本就是该形态，清单
// 条目偏离即配置错误（比对恒不命中，fail-fast 暴露笔误）。
func validateOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil {
		return fmt.Errorf("cors: origin %q is not a valid url", o)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("cors: origin %q must be scheme://host[:port]", o)
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("cors: origin %q must not contain path, query, fragment or userinfo", o)
	}
	if strings.Contains(u.Host, ":") {
		// 带端口或 IPv6 形态须可拆 host:port；无端口主机名（默认端口）不进此支。
		if _, _, err := net.SplitHostPort(u.Host); err != nil {
			return fmt.Errorf("cors: origin %q has malformed host[:port]", o)
		}
	}
	return nil
}
