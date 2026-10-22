package task

import "github.com/google/wire"

// ProviderSet 是 task 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewMaintenanceTask)
