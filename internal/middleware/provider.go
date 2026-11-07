package middleware

import (
	"context"

	"grape/internal/biz"

	"github.com/google/wire"
)

// NewAuditUsernameResolver 构造操作日志的冗余用户名解析器：经本人资料
// 回读（目标恒取认证身份，RLS 仍限租户）补齐 username 列。解析失败由
// 中间件回落为空，不阻断业务操作。
func NewAuditUsernameResolver(users biz.UserRepo) AuditUsernameResolver {
	return func(ctx context.Context, auth biz.Identity) (string, error) {
		u, err := users.GetMyProfile(ctx, auth)
		if err != nil {
			return "", err
		}
		if u == nil {
			return "", nil
		}
		return u.Name, nil
	}
}

// ProviderSet 是入口中间件层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(
	NewAuthMiddleware,
	NewPermissionMiddleware,
	NewTraceMiddleware,
	NewOperationAuditMiddleware,
	NewAuditUsernameResolver,
)
