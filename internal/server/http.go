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
func NewHTTPServer(c *conf.Server, traceMiddleware entrymw.TraceMiddleware, authMiddleware middleware.Middleware, metrics nethttp.Handler, user *service.UserService, session *service.SessionService) *http.Server {
	var opts = []http.ServerOption{
		http.Middleware(
			recovery.Recovery(),
			middleware.Middleware(traceMiddleware),
			authMiddleware,
		),
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
	return srv
}
