package biz

import "github.com/google/wire"

// ProviderSet 是 biz 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewUserUsecase, NewOrderUsecase)
