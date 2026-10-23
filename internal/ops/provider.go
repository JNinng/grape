package ops

import (
	"github.com/google/wire"
)

// ProviderSet 是运维出口的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewMetricsHandler)
