package middleware

import (
	"github.com/google/wire"
)

// ProviderSet 是入口中间件层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewAuthMiddleware, NewTraceMiddleware)
