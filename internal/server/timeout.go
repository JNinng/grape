package server

import (
	"context"
	"strings"

	"grape/internal/conf"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// RouteTimeoutMiddleware 是路由级请求超时中间件的注入类型。它与链路
// 中间件同理以命名类型承载，避免 wire 按参数类型装配时与普通
// middleware.Middleware 混淆。
type RouteTimeoutMiddleware middleware.Middleware

// NewRouteTimeoutMiddleware 构造路由级请求超时中间件：SSE 治理把
// server.http.timeout 归零后，普通 HTTP 路由由本中间件按
// server.http.request_timeout 恢复请求限时；agent 前缀（AG-UI / ack，
// SSE 长流）在跳过名单内不施加。跳过判定按操作名（手写路由即路径
// 模板）前缀匹配，名单取 agent 服务暴露的挂载前缀。request_timeout
// 未设置或非正时中间件退化为直通。
func NewRouteTimeoutMiddleware(c *conf.Server, agent *service.AgentService) RouteTimeoutMiddleware {
	d := c.GetHttp().GetRequestTimeout().AsDuration()
	basePath := ""
	if agent != nil {
		basePath = agent.BasePath()
	}
	return RouteTimeoutMiddleware(func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if d <= 0 {
				return handler(ctx, req)
			}
			if tr, ok := transport.FromServerContext(ctx); ok && basePath != "" {
				if strings.HasPrefix(tr.Operation(), basePath) {
					return handler(ctx, req)
				}
			}
			tctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return handler(tctx, req)
		}
	})
}
