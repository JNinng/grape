package biz

import (
	"github.com/google/wire"

	"grape/pkg/redact"
)

// ProviderSet 是 biz 层的 Wire 依赖注入集合。NewRedactRegistry 提供审计
// 表与复现视图共用的脱敏注册表（单一代码点）：经 wire 装配为
// 同一实例，消费方按类别取用、不自行构造。
var ProviderSet = wire.NewSet(NewUserUsecase, NewAssignmentUsecase, NewDeptUsecase, NewUserGroupUsecase, NewRoleUsecase, NewMenuUsecase, NewPermissionUsecase, NewDictUsecase, NewConfigUsecase, NewTenantConfigUsecase, NewFeatureFlagUsecase, NewTranslationUsecase, NewTenantUsecase, NewAuditLogUsecase, NewSessionUsecase, NewAuthUsecase, NewAuditUsecase, NewDocumentUsecase, NewGrantUsecase, NewRetrievalLogUsecase, NewQueryUsecase, NewRedactRegistry)

// NewRedactRegistry 提供标准脱敏注册表（四类骨架默认值，装配方按需
// Register 覆盖）；审计、检索留痕与复现视图经 wire 共用同一实例。
func NewRedactRegistry() *redact.Registry {
	return redact.NewStandardRegistry()
}
