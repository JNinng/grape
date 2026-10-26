package server

import (
	nethttp "net/http"

	sessionv1 "grape/api/session/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/http"
)

// NewHTTPServer 构造 HTTP 服务器并注册各资源的 HTTP 路由。
// 运维出口（如 metrics）由 ops 包构造，经 srv.Handle 直挂：业务中间件
// （recovery、链路、认证）只作用于生成代码端点内的 ctx.Middleware，直挂的
// 原始 handler 天然在其链外——/metrics 免认证即可访问。手写业务端点
// 不走 srv.Handle，归属与入链要求见 internal/README.md「手写端点归属」。
// SSE 治理：server.http.timeout 归零后传输层不再统一限时，普通路由的
// 请求超时由路由级超时中间件（recovery 之后入链）按 request_timeout
// 恢复；agent 前缀路由（AG-UI / ack 的 SSE 长流）被其跳过，运行时长
// 由 agent.agui.timeout 兜底。
func NewHTTPServer(
	c *conf.Server,
	traceMiddleware entrymw.TraceMiddleware,
	routeTimeout RouteTimeoutMiddleware,
	authMiddleware middleware.Middleware,
	metrics nethttp.Handler,
	user *service.UserService,
	session *service.SessionService,
	agent *service.AgentService,
) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
			middleware.Middleware(routeTimeout),
			middleware.Middleware(traceMiddleware),
			authMiddleware,
		),
		// 关闭全局尾斜杠规整：kratos 默认 StrictSlash 会把 agent 前缀的
		// "/agui/" 反向 301 到 "/agui"，与内层 ServeMux 的 "/agui → /agui/"
		// 规整形成重定向环。关闭后无尾斜杠形式由内层 ServeMux 以 307
		// （保方法）规整到 "/agui/"；生成代码路由的尾斜杠变体由 301 变为
		// 404，属收紧而非放宽。
		http.StrictSlash(false),
	}
	if c.Http.Network != "" {
		opts = append(opts, http.Network(c.Http.Network))
	}
	if c.Http.Addr != "" {
		opts = append(opts, http.Address(c.Http.Addr))
	}
	if c.Http.Timeout != nil {
		opts = append(opts, http.Timeout(c.Http.Timeout.AsDuration()))
	}
	srv := http.NewServer(opts...)
	srv.Handle("/metrics", metrics)
	userv1.RegisterUserServiceHTTPServer(srv, user)
	sessionv1.RegisterSessionServiceHTTPServer(srv, session)
	// agent 为 nil 仅出现在不触达业务路由的测试装配里；生产装配恒非 nil。
	if agent != nil {
		agent.RegisterHTTP(srv)
	}
	return srv
}
