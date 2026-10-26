package telemetry

import (
	"github.com/google/wire"
)

// ProviderSet 是观测装配层的 Wire 依赖注入集合。NewSetup 产出 *Setup
// 依赖锚点：server（otel 中间件开关）与后续消费方持有时序保证。
var ProviderSet = wire.NewSet(NewSetup)
