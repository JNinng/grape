package identity

import (
	"context"
	"errors"

	"grape/internal/biz"
)

// ErrMissingIdentity 是认证身份缺失的统一错误：AG-UI UserIDResolver、
// ack WithUserFunc 与业务工具都以它 fail-closed，禁止回落框架默认用户
// "user"。文案是既有契约：端点 401 响应体与测试断言依赖。
var ErrMissingIdentity = errors.New("agent: missing identity in context")

// UserKeyFromContext 把认证身份编码为框架会话存储键（单一代码点）：
// AG-UI UserIDResolver、ack WithUserFunc、运行并发门控、会话列表与事件
// 窗口端点、ack 归属守卫与授权审计面共用。身份缺失返回
// ErrMissingIdentity，fail-closed 形态由调用方决定。
func UserKeyFromContext(ctx context.Context) (string, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return "", ErrMissingIdentity
	}
	return EncodeUserKey(id.TenantID, id.UserID)
}
