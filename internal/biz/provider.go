package biz

import (
	"github.com/google/wire"

	"grape/pkg/redact"
)

// ProviderSet 是 biz 层的 Wire 依赖注入集合。NewRedactRegistry 提供审计
// 表与复现视图共用的脱敏注册表（单一代码点）：经 wire 装配为
// 同一实例，消费方按类别取用、不自行构造。知识库用例同时绑定为检索的
// 可见库集合解析接缝（单一实现，检索与列库工具同源）。
var ProviderSet = wire.NewSet(NewUserUsecase, NewAssignmentUsecase, NewDeptUsecase, NewUserGroupUsecase, NewRoleUsecase, NewMenuUsecase, NewPermissionUsecase, NewDictUsecase, NewConfigUsecase, NewTenantConfigUsecase, NewFeatureFlagUsecase, NewTranslationUsecase, NewTenantUsecase, NewAuditLogUsecase, NewSessionUsecase, NewAuthUsecase, NewAuditUsecase, NewDocumentUsecaseDefault, NewGrantUsecase, NewRetrievalLogUsecase, NewQueryUsecase, NewRedactRegistry, NewKnowledgeUsecase, NewKnowledgeSearchUsecase, NewModelUsecase,
	wire.Bind(new(KnowledgeLibraryDirectory), new(*KnowledgeUsecase)))

// NewDocumentUsecaseDefault 是 NewDocumentUsecase 的无选项装配形态：缺省
// 切分参数由构造函数内置，Wire 不需要 DocumentOption 提供方。
func NewDocumentUsecaseDefault(store VectorStore, embedder Embedder, resolver EmbedderResolver, completer Completer) (*DocumentUsecase, error) {
	return NewDocumentUsecase(store, embedder, resolver, completer)
}

// NewRedactRegistry 提供标准脱敏注册表（四类骨架默认值，装配方按需
// Register 覆盖）；审计、检索留痕与复现视图经 wire 共用同一实例。
func NewRedactRegistry() *redact.Registry {
	return redact.NewStandardRegistry()
}
