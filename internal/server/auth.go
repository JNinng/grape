package server

import (
	"context"
	"strings"

	sessionv1 "grape/api/session/v1"
	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// authorizationKey / bearerScheme 是令牌携带方式：Authorization: Bearer
// <token>，不走 cookie。gRPC 侧同名元数据键不区分大小写。
const (
	authorizationKey = "Authorization"
	bearerScheme     = "Bearer"
)

// publicOperations 是免认证端点：登录与刷新在身份成立之前调用，凭据
// 分别是用户名口令与刷新令牌，不参与访问令牌校验。
var publicOperations = map[string]struct{}{
	sessionv1.OperationSessionServiceLogin:   {},
	sessionv1.OperationSessionServiceRefresh: {},
}

// NewAuthMiddleware 构造认证中间件：对非公开端点提取 Bearer 访问令牌，
// 经 SessionUsecase 校验（令牌生成与哈希纪律单点），命中后把会话行内
// 身份事实注入 ctx，服务层经 biz.IdentityFromContext 读取后显式传给用例。
// 取不到传输上下文即拒绝：无法判定端点是否公开，按体系缺身份默认拒绝
// 的口径 fail-closed。
func NewAuthMiddleware(sessions *biz.SessionUsecase) middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return nil, biz.ErrSessionInvalid
			}
			if isPublicOperation(tr.Operation()) {
				return handler(ctx, req)
			}
			auth, err := authenticate(ctx, tr, sessions)
			if err != nil {
				return nil, err
			}
			return handler(biz.WithIdentity(ctx, auth), req)
		}
	}
}

// authenticate 提取并校验 Bearer 令牌，返回会话身份事实。缺头、格式
// 非法与令牌无效统一失败于 ErrSessionInvalid：401 语义单一，不向调用
// 方区分具体原因（与会话校验的口径一致）。
func authenticate(ctx context.Context, tr transport.Transporter, sessions *biz.SessionUsecase) (biz.Identity, error) {
	scheme, token, _ := strings.Cut(tr.RequestHeader().Get(authorizationKey), " ")
	if !strings.EqualFold(scheme, bearerScheme) || token == "" {
		return biz.Identity{}, biz.ErrSessionInvalid
	}
	session, err := sessions.Validate(ctx, token)
	if err != nil {
		return biz.Identity{}, err
	}
	return session.Identity(), nil
}

// isPublicOperation 判定端点是否免认证。
func isPublicOperation(operation string) bool {
	_, ok := publicOperations[operation]
	return ok
}
