package biz

import (
	"github.com/google/wire"

	"grape/pkg/redact"
)

// ProviderSet 是 biz 层的 Wire 依赖注入集合。NewRedactRegistry 提供审计
// 表与复现视图（A3）共用的脱敏注册表（E1 单一代码点）：经 wire 装配为
// 同一实例，消费方按类别取用、不自行构造。
var ProviderSet = wire.NewSet(NewUserUsecase, NewSessionUsecase, NewAuthUsecase, NewAuditUsecase, NewDocumentUsecase, NewRedactRegistry)

// NewRedactRegistry 提供标准脱敏注册表（四类骨架默认值，装配方按需
// Register 覆盖）。独立成函数是为了让 wire 装配层有明确的提供点注释：
// 日志 debug tracer（data 层）当前自带同默认值实例，收拢到同一实例的
// 重构待其消费方变更窗口进行。
func NewRedactRegistry() *redact.Registry {
	return redact.NewStandardRegistry()
}
