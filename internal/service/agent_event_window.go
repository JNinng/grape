package service

import (
	context "context"
	nethttp "net/http"

	"grape/internal/agent"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// AgentEventWindowService 承载 agent 会话事件窗口手写端点（非 proto 生成，
// A3）的路由注册面：GET {basePath}/event-window 按 threadId + eventId 取
// 锚点事件窗口（before/after 可选），供运维复现视图做压缩占位符的原文
// 对照与执行轨迹回查。窗口装配与 HTTP 形态收在 internal/agent
// （Stack.EventWindowHandler：与 AG-UI / ack 同一框架 session.Service 实
// 例的 WindowService 能力、同一 app 名命名空间、同一 userKey 编码），本
// 类型只把该 handler 注册进 Kratos 中间件链——参数校验、认证归属（跨用
// 户锚点不可见统一 404）与错误语义都在装配面内实现。
type AgentEventWindowService struct {
	handler  nethttp.Handler
	basePath string
}

// NewAgentEventWindowService 构造事件窗口端点服务：handler 与路由前缀取
// 自同一骨架装配物，保证与 AG-UI 会话的命名空间一致。
func NewAgentEventWindowService(stack *agent.Stack) *AgentEventWindowService {
	return &AgentEventWindowService{
		handler:  stack.EventWindowHandler(),
		basePath: stack.BasePath(),
	}
}

// RegisterHTTP 把事件窗口路由注册到 Kratos HTTP 服务器。必须先于
// AgentService 的前缀通配路由注册（与 AgentSessionsService 同序，见
// server.NewHTTPServer）：gorilla/mux 按注册顺序匹配，/agui/event-window
// 精确路径只有排在 /agui/{rest:.*} 之前才会命中本端点。方法取 GET：只读
// 语义；操作名即路径模板，认证中间件按它判定非公开端点，缺令牌
// fail-closed 返回 401。
func (s *AgentEventWindowService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, s.basePath+"/event-window", s.routeHandler(s.handler))
}

// routeHandler 把骨架产出的 handler 包成 Kratos 的 HandlerFunc：与
// AgentSessionsService 的入链形态一致（ctx.Middleware 注入身份与链路事
// 实）。Kratos 的 wrapper 不暴露请求替换点，中间件链产出的 ctx 以显式派
// 生请求传入 handler。
func (s *AgentEventWindowService) routeHandler(outer nethttp.Handler) func(kratoshttp.Context) error {
	return func(ctx kratoshttp.Context) error {
		req, res := ctx.Request(), ctx.Response()
		inner := func(mctx context.Context, _ any) (any, error) {
			outer.ServeHTTP(res, req.WithContext(mctx))
			return nil, nil
		}
		_, err := ctx.Middleware(middleware.Handler(inner))(ctx, nil)
		return err
	}
}
