package service

import (
	context "context"
	nethttp "net/http"

	"grape/internal/agent"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// agentRoutePrefixSuffix 是前缀通配路由的 gorilla/mux 尾段：覆盖前缀下
// 全部子路径（chat、cancel、ack 的确认路由），空尾段也命中。
const agentRoutePrefixSuffix = "/{rest:.*}"

// AgentService 承载 Agent 骨架的手写 HTTP 端点：不实现 proto 契约，只把
// agent.Stack 的外层 ServeMux 经 srv.Route + ctx.Middleware 挂进 Kratos
// 中间件链（recovery → trace → auth → 路由级超时），认证与链路对 AG-UI
// 与 ack 的全部子路径生效。本层不装配框架对象，DTO 也不经过——SSE 事件
// 流由框架直写 ResponseWriter。
type AgentService struct {
	stack *agent.Stack
}

// NewAgentService 构造 Agent 手写端点服务。
func NewAgentService(stack *agent.Stack) *AgentService {
	return &AgentService{stack: stack}
}

// RegisterHTTP 把 AG-UI 与 ack 的前缀路由注册到 Kratos HTTP 服务器。
// 注册两条路由：精确路径（无尾斜杠形式，交由内层 ServeMux 以 307 保
// 方法规整到前缀根）与前缀通配（全部子路径）。方法取 "*"：方法分发在
// 内外两层 ServeMux 内完成，405 语义不归 Kratos。手写端点的操作名即
// 路径模板，认证中间件按它判定非公开端点，缺令牌 fail-closed 返回 401。
func (s *AgentService) RegisterHTTP(srv *kratoshttp.Server) {
	handle := s.routeHandler(s.stack.Handler())
	base := s.stack.BasePath()
	route := srv.Route("/")
	route.Handle("*", base, handle)
	route.Handle("*", base+agentRoutePrefixSuffix, handle)
}

// BasePath 返回 Agent 路由前缀，server 的路由级超时中间件以它为跳过
// 名单（SSE 长流不受请求超时约束）。
func (s *AgentService) BasePath() string { return s.stack.BasePath() }

// routeHandler 把原生 ServeMux 包成 Kratos 的 HandlerFunc：先经
// ctx.Middleware 入链（身份注入认证 ctx、trace-id 写回响应头），再把
// 链上产出的 ctx 回绑到请求上交给外层 ServeMux——AG-UI 的运行 ctx 与
// ack 的身份解析都从这个请求 ctx 取事实。
func (s *AgentService) routeHandler(outer nethttp.Handler) func(kratoshttp.Context) error {
	return func(ctx kratoshttp.Context) error {
		inner := func(mctx context.Context, _ any) (any, error) {
			outer.ServeHTTP(ctx.Response(), ctx.Request().WithContext(mctx))
			return nil, nil
		}
		_, err := ctx.Middleware(middleware.Handler(inner))(ctx, nil)
		return err
	}
}
