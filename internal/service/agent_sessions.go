package service

import (
	context "context"
	nethttp "net/http"

	"grape/internal/agent"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// AgentSessionsService 承接 agent 会话级只读端点的手写路由注册面（非
// proto 生成）：GET {basePath}/sessions 列出当前认证用户的会话元数据，
// GET {basePath}/session-usage 按 threadId 返回会话内的模型调用事实
// （模型型号与 token 用量等）。两者的装配、认证与 JSON 形态都在
// internal/agent 的 Stack handler 内（与 AG-UI 共享 session.Service 实例、
// app 命名空间与 userKey 编码）；会话范围只认服务端认证身份，跨用户不
// 可见，本类型只负责注册进 Kratos 中间件链。
type AgentSessionsService struct {
	handler  nethttp.Handler
	usage    nethttp.Handler
	basePath string
}

// NewAgentSessionsService 构造会话级端点服务：handler 与路由前缀取自同一
// 骨架装配物，保证与 AG-UI 会话的命名空间一致。
func NewAgentSessionsService(stack *agent.Stack) *AgentSessionsService {
	return &AgentSessionsService{
		handler:  stack.SessionsHandler(),
		usage:    stack.SessionUsageHandler(),
		basePath: stack.BasePath(),
	}
}

// RegisterHTTP 把会话级路由注册到 Kratos HTTP 服务器。必须先于
// AgentService 的前缀通配路由注册（见 server.NewHTTPServer 的注册顺
// 序）：gorilla/mux 按注册顺序匹配，精确路径只有排在 /agui/{rest:.*}
// 之前才会命中本服务端点。方法取 GET：只读语义；操作名即路径模板，认证
// 中间件按它判定非公开端点，缺令牌 fail-closed 返回 401。
func (s *AgentSessionsService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, s.basePath+"/sessions", s.routeHandler(s.handler))
	route.Handle(nethttp.MethodGet, s.basePath+"/session-usage", s.routeHandler(s.usage))
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
