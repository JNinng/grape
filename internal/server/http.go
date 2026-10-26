package server

import (
	nethttp "net/http"

	sessionv1 "grape/api/session/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/service"
	"grape/internal/telemetry"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/http"
)

// NewHTTPServer 构造 HTTP 服务器并注册各资源的 HTTP 路由。
// 运维出口（如 metrics）由 ops 包构造，经 srv.Handle 直挂：业务中间件
// （recovery、链路、认证）只作用于生成代码端点内的 ctx.Middleware，直挂的
// 原始 handler 天然在其链外——/metrics 免认证即可访问。手写业务端点
// 不走 srv.Handle，归属与入链要求见 internal/README.md「手写端点归属」。
// 前端静态托管（web fs）经 srv.HandlePrefix 直挂在全部业务路由之后：
// gorilla/mux 按注册顺序匹配，API / AG-UI / metrics 未命中的 GET 才会
// 落入 SPA fallback；与 /metrics 同类，静态内容免身份（登录页本身即静态
// 壳，鉴权发生在 API 调用），且 handler 内部对保留 API 前缀返回 404，
// 不受方法失配（如 GET 打在 POST 路由上）影响的路径绝不返回页面壳。
// SSE 治理：server.http.timeout 归零后传输层不再统一限时，普通路由的
// 请求超时由路由级超时中间件（recovery 之后入链）按 request_timeout
// 恢复；agent 前缀路由（AG-UI / ack 的 SSE 长流）被其跳过，运行时长
// 由 agent.agui.timeout 兜底。
// 遥测：server.telemetry.enabled 时 kratos contrib otel 的 tracing.Server()
// 入链产生 server span 并经全局 provider 导出；位置在链路中间件之内、
// 认证之外——不可信 traceparent 已被链路中间件剥除，otel 的 Extract 以
// 其注入的 SpanContext 为父，可信网络的 fail-closed 信任模型不被绕过。
// agent 前缀手写路由经 ctx.Middleware 入链，同被 span 覆盖。
func NewHTTPServer(
	c *conf.Server,
	tele *telemetry.Setup,
	traceMiddleware entrymw.TraceMiddleware,
	routeTimeout RouteTimeoutMiddleware,
	authMiddleware middleware.Middleware,
	metrics nethttp.Handler,
	user *service.UserService,
	session *service.SessionService,
	agent *service.AgentService,
	agentSessions *service.AgentSessionsService,
	web WebHandler,
) (*http.Server, error) {
	cors, err := entrymw.NewCORS(c)
	if err != nil {
		return nil, err
	}
	mws := []middleware.Middleware{recovery.Recovery()}
	if cors != nil {
		mws = append(mws, cors.Middleware())
	}
	mws = append(mws,
		middleware.Middleware(routeTimeout),
		middleware.Middleware(traceMiddleware),
	)
	if tele.Enabled() {
		mws = append(mws, tracing.Server())
	}
	mws = append(mws, authMiddleware)
	var opts = []http.ServerOption{
		http.Middleware(mws...),
		// 关闭全局尾斜杠规整：kratos 默认 StrictSlash 会把 agent 前缀的
		// "/agui/" 反向 301 到 "/agui"，与内层 ServeMux 的 "/agui → /agui/"
		// 规整形成重定向环。关闭后无尾斜杠形式由内层 ServeMux 以 307
		// （保方法）规整到 "/agui/"；生成代码路由的尾斜杠变体由 301 变为
		// 404，属收紧而非放宽。
		http.StrictSlash(false),
	}
	// CORS 启用时兜住生成代码路由的预检：OPTIONS 与注册方法不匹配在
	// gorilla/mux 层即被 405 拦下、进不了中间件链，预检须在此层应答；
	// 非 OPTIONS（真 405）回落 kratos 默认行为（http.DefaultServeMux）。
	if cors != nil {
		opts = append(opts, http.MethodNotAllowedHandler(cors.MethodNotAllowed(nethttp.DefaultServeMux)))
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
	// agentSessions 为 nil 仅出现在不触达业务路由的测试装配里；生产装配
	// 恒非 nil。必须先于 agent 注册：gorilla/mux 按注册顺序匹配，
	// /agui/sessions 精确路径只有排在 agent 前缀通配之前才不被吞并。
	if agentSessions != nil {
		agentSessions.RegisterHTTP(srv)
	}
	// agent 为 nil 仅出现在不触达业务路由的测试装配里；生产装配恒非 nil。
	if agent != nil {
		agent.RegisterHTTP(srv)
	}
	// web 为 nil 仅出现在不触达静态路由的测试装配里；生产装配恒非 nil。
	// 必须最后注册：SPA fallback 的语义就是「其余路由全部未命中」。
	if web != nil {
		srv.HandlePrefix("/", nethttp.Handler(web))
	}
	return srv, nil
}
