package server

import (
	"github.com/google/wire"
)

// ProviderSet 是 server 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewAuthMiddleware, NewGRPCServer, NewHTTPServer)
