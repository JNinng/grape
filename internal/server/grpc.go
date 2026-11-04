package server

import (
	auditv1 "grape/api/audit/v1"
	dictv1 "grape/api/dict/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	queryv1 "grape/api/query/v1"
	sessionv1 "grape/api/session/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/service"
	"grape/internal/telemetry"

	"github.com/go-kratos/kratos/contrib/otel/v3/tracing"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport/grpc"
)

// NewGRPCServer 构造 gRPC 服务器并注册各资源的 gRPC 服务。otel 中间件
// 的入链语义与 HTTP 同（见 http.go）：enabled 时在链路中间件之内产生
// server span。
func NewGRPCServer(c *conf.Server, tele *telemetry.Setup, traceMiddleware entrymw.TraceMiddleware, authMiddleware middleware.Middleware, permMiddleware entrymw.PermissionMiddleware, user *service.UserService, dept *service.DeptService, userGroup *service.UserGroupService, role *service.RoleService, menu *service.MenuService, dict *service.DictService, perm *service.PermissionService, session *service.SessionService, sessionAdmin *service.SessionAdminService, auditLog *service.AuditService, query *service.QueryService) *grpc.Server {
	mws := []middleware.Middleware{
		recovery.Recovery(),
		middleware.Middleware(traceMiddleware),
	}
	if tele.Enabled() {
		mws = append(mws, tracing.Server())
	}
	mws = append(mws, authMiddleware, middleware.Middleware(permMiddleware))
	var opts = []grpc.ServerOption{
		grpc.Middleware(mws...),
	}
	if c.Grpc.Network != "" {
		opts = append(opts, grpc.Network(c.Grpc.Network))
	}
	if c.Grpc.Addr != "" {
		opts = append(opts, grpc.Address(c.Grpc.Addr))
	}
	if c.Grpc.Timeout != nil {
		opts = append(opts, grpc.Timeout(c.Grpc.Timeout.AsDuration()))
	}
	srv := grpc.NewServer(opts...)
	userv1.RegisterUserServiceServer(srv, user)
	orgv1.RegisterDeptServiceServer(srv, dept)
	orgv1.RegisterUserGroupServiceServer(srv, userGroup)
	permv1.RegisterRoleServiceServer(srv, role)
	permv1.RegisterMenuServiceServer(srv, menu)
	dictv1.RegisterDictServiceServer(srv, dict)
	auditv1.RegisterSessionAdminServiceServer(srv, sessionAdmin)
	auditv1.RegisterAuditServiceServer(srv, auditLog)
	permv1.RegisterPermissionServiceServer(srv, perm)
	sessionv1.RegisterSessionServiceServer(srv, session)
	queryv1.RegisterQueryServiceServer(srv, query)
	return srv
}
