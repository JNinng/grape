package server

import (
	"context"
	"strings"

	"grape/internal/conf"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// RouteTimeoutMiddleware 是路由级请求超时中间件的注入类型。以命名类型
// 承载，避免 Wire 按参数类型装配时与普通 middleware.Middleware 混淆。
type RouteTimeoutMiddleware middleware.Middleware

// NewRouteTimeoutMiddleware 构造路由级请求超时中间件：server.http.timeout
// 归零后，普通路由按 server.http.request_timeout 恢复限时；agent 前缀
// （SSE 长流）跳过。跳过按操作名边界匹配（等于前缀或子路径），防止
// /aguiX 类相邻操作被误跳过；request_timeout 非正时退化为直通。
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
				if op := tr.Operation(); op == basePath || strings.HasPrefix(op, basePath+"/") {
					return handler(ctx, req)
				}
			}
			tctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return handler(tctx, req)
		}
	})
}
