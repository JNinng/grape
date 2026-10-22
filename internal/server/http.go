package server

import (
	nethttp "net/http"

	sessionv1 "grape/api/session/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/conf"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/http"
)

// NewHTTPServer 构造 HTTP 服务器并注册各资源的 HTTP 路由。
// metrics 是原始指标出口，经 srv.Handle 直挂 /metrics：业务中间件
// （recovery、认证）只作用于生成代码端点内的 ctx.Middleware，直挂的
// 原始 handler 天然在其链外——/metrics 免认证即可访问。
func NewHTTPServer(c *conf.Server, traceMiddleware TraceMiddleware, authMiddleware middleware.Middleware, metrics nethttp.Handler, user *service.UserService, session *service.SessionService) *http.Server {
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
