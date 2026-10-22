package biz

import "context"

// Identity 是服务端认证上下文中的身份事实：TenantID 注入存储层会话
// 变量 app.tenant_id（RLS 租户边界），UserID 是权限推导与审计列的
// 操作者。只由服务端构造，永不受前端请求影响；来源是认证中间件——
// 令牌校验命中后自会话行取出，经 WithIdentity 注入。
type Identity struct {
	TenantID string
	UserID   int64
}

// identityKey 是 Identity 的上下文键：零大小私有类型，注入点
// 只在 biz 包内暴露，包外无法伪造身份。
type identityKey struct{}

// WithIdentity 把身份事实注入 ctx。仅认证中间件调用：身份只来自
// 令牌校验命中后的会话行，不经任何请求侧输入。
func WithIdentity(ctx context.Context, auth Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, auth)
}

// IdentityFromContext 读取认证中间件注入的身份事实；缺失返回 false，
// 调用方按未认证 fail-closed 处理。
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	auth, ok := ctx.Value(identityKey{}).(Identity)
	return auth, ok
}
