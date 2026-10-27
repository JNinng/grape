package service

import (
	context "context"
	nethttp "net/http"

	"grape/internal/agent"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// AgentSessionsService 承载 agent 会话列表手写端点（非 proto 生成）
// 的路由注册面：GET {basePath}/sessions 列出当前认证用户的 agent 会话
// 元数据，供前端对话页的会话切换使用。列表装配与 HTTP 形态收在
// internal/agent（Stack.SessionsHandler：与 AG-UI / ack 同一框架
// session.Service 实例、同一 app 名命名空间、同一 userKey 编码），本类型
// 只把该 handler 注册进 Kratos 中间件链——认证、分页校验、JSON 形态与
// 错误语义都在装配面内实现，会话范围以服务端认证身份编码的 userKey 为
// 唯一来源，跨用户天然不可见。
type AgentSessionsService struct {
	handler  nethttp.Handler
	basePath string
}

// NewAgentSessionsService 构造 agent 会话列表端点服务：handler 与路由
// 前缀取自同一骨架装配物，保证与 AG-UI 会话的命名空间一致。
func NewAgentSessionsService(stack *agent.Stack) *AgentSessionsService {
	return &AgentSessionsService{
		handler:  stack.SessionsHandler(),
		basePath: stack.BasePath(),
	}
}

// RegisterHTTP 把会话列表路由注册到 Kratos HTTP 服务器。必须先于
// AgentService 的前缀通配路由注册（见 server.NewHTTPServer 的注册顺
// 序）：gorilla/mux 按注册顺序匹配，/agui/sessions 精确路径只有排在
// /agui/{rest:.*} 之前才会命中本端点。方法取 GET：只读语义；操作名即
// 路径模板，认证中间件按它判定非公开端点，缺令牌 fail-closed 返回 401。
func (s *AgentSessionsService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, s.basePath+"/sessions", s.routeHandler(s.handler))
}

// routeHandler 把骨架产出的 handler 包成 Kratos 的 HandlerFunc：与
// AgentService 的入链形态一致（ctx.Middleware 注入身份与链路事实），
// 桥接逻辑单独成函数是因为 AgentService 的同名方法绑定在其类型上，本
// 类型不与其耦合。Kratos 的 wrapper 不暴露请求替换点，中间件链产出的
// ctx 以显式派生请求传入 handler，handler 不再经 wrapper 读原始请求。
func (s *AgentSessionsService) routeHandler(outer nethttp.Handler) func(kratoshttp.Context) error {
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
