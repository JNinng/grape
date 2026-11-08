package server

import (
	nethttp "net/http"

	auditv1 "grape/api/audit/v1"
	configv1 "grape/api/config/v1"
	dictv1 "grape/api/dict/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	queryv1 "grape/api/query/v1"
	sessionv1 "grape/api/session/v1"
	tenantv1 "grape/api/tenant/v1"
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

// NewHTTPServer 构造 HTTP 服务器并注册生成路由、agent 手写路由、运维
// 直挂与 SPA 静态托管。中间件链序、直挂端点链外免认证、路由级超时与
// otel 信任模型见同目录 README。
func NewHTTPServer(
	c *conf.Server,
	tele *telemetry.Setup,
	traceMiddleware entrymw.TraceMiddleware,
	routeTimeout RouteTimeoutMiddleware,
	authMiddleware middleware.Middleware,
	auditMiddleware entrymw.OperationAuditMiddleware,
	permMiddleware entrymw.PermissionMiddleware,
	metrics nethttp.Handler,
	user *service.UserService,
	dept *service.DeptService,
	userGroup *service.UserGroupService,
	role *service.RoleService,
	menu *service.MenuService,
	dict *service.DictService,
	config *service.ConfigService,
	tenantConfig *service.TenantConfigService,
	featureFlag *service.FeatureFlagService,
	translation *service.TranslationService,
	tenant *service.TenantService,
	perm *service.PermissionService,
	session *service.SessionService,
	sessionAdmin *service.SessionAdminService,
	auditLog *service.AuditService,
	query *service.QueryService,
	agent *service.AgentService,
	agentSessions *service.AgentSessionsService,
	agentEventWindow *service.AgentEventWindowService,
	agentAudit *service.AgentAuditService,
	agentRetrieval *service.AgentRetrievalService,
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
	// 操作审计在认证之后、权限之前：身份已成立，权限拒绝（403）也入账。
	// 测试装配可不注入审计中间件（nil 跳过）。
	mws = append(mws, authMiddleware)
	if auditMiddleware != nil {
		mws = append(mws, middleware.Middleware(auditMiddleware))
	}
	mws = append(mws, middleware.Middleware(permMiddleware))
	var opts = []http.ServerOption{
		http.Middleware(mws...),
		// 关闭 StrictSlash：默认规整会把 "/agui/" 反向 301 到 "/agui"，
		// 与内层 ServeMux 互踢成重定向环。关闭后无尾斜杠形态由内层以
		// 307（保方法）规整，生成路由的尾斜杠变体由 301 变为 404。
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
	orgv1.RegisterDeptServiceHTTPServer(srv, dept)
	orgv1.RegisterUserGroupServiceHTTPServer(srv, userGroup)
	permv1.RegisterRoleServiceHTTPServer(srv, role)
	permv1.RegisterMenuServiceHTTPServer(srv, menu)
	dictv1.RegisterDictServiceHTTPServer(srv, dict)
	configv1.RegisterConfigServiceHTTPServer(srv, config)
	configv1.RegisterTenantConfigServiceHTTPServer(srv, tenantConfig)
	configv1.RegisterFeatureFlagServiceHTTPServer(srv, featureFlag)
	configv1.RegisterTranslationServiceHTTPServer(srv, translation)
	tenantv1.RegisterTenantServiceHTTPServer(srv, tenant)
	auditv1.RegisterSessionAdminServiceHTTPServer(srv, sessionAdmin)
	auditv1.RegisterAuditServiceHTTPServer(srv, auditLog)
	permv1.RegisterPermissionServiceHTTPServer(srv, perm)
	sessionv1.RegisterSessionServiceHTTPServer(srv, session)
	queryv1.RegisterQueryServiceHTTPServer(srv, query)
	// agentSessions 为 nil 仅出现在不触达该路由的测试装配；生产恒非 nil。
	// 必须先于 agent 注册：gorilla/mux 按注册顺序匹配，精确路径
	// /agui/sessions 只有排在 agent 前缀通配之前才不被吞并。
	if agentSessions != nil {
		agentSessions.RegisterHTTP(srv)
	}
	// agentEventWindow 为 nil 仅出现在不触达该路由的测试装配；生产恒非 nil。
	// 事件窗口在 agent 前缀内，同 sessions 须先于前缀通配注册。
	if agentEventWindow != nil {
		agentEventWindow.RegisterHTTP(srv)
	}
	// agentAudit 为 nil 仅出现在不触达该路由的测试装配；生产恒非 nil。
	// 路由（/v1/agent/audit/*）不在 agent 前缀内，注册顺序无冲突。
	if agentAudit != nil {
		agentAudit.RegisterHTTP(srv)
	}
	// agentRetrieval 为 nil 仅出现在不触达该路由的测试装配；生产恒非 nil。
	// 路由（/v1/agent/retrieval-logs）不在 agent 前缀内，注册顺序无冲突。
	if agentRetrieval != nil {
		agentRetrieval.RegisterHTTP(srv)
	}
	// agent 为 nil 仅出现在不触达该路由的测试装配；生产恒非 nil。
	if agent != nil {
		agent.RegisterHTTP(srv)
	}
	// web 为 nil 仅出现在不触达静态路由的测试装配；生产恒非 nil。
	// 必须最后注册：SPA fallback 的语义即「其余路由全部未命中」。
	if web != nil {
		srv.HandlePrefix("/", nethttp.Handler(web))
	}
	return srv, nil
}
