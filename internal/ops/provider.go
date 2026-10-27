package ops

import (
	"github.com/google/wire"

	"grape/internal/biz"
)

// ProviderSet 是运维出口的 Wire 依赖注入集合：NewRuntimeMetrics 提供并
// 绑定为 biz.AgentRunObserver（agent 框架适配层经该契约推送运行事实，
// 本包消费同一实例注册进出口）。
var ProviderSet = wire.NewSet(
	NewMetricsHandler,
	NewRuntimeMetrics,
	wire.Bind(new(biz.AgentRunObserver), new(*RuntimeMetrics)),
)
