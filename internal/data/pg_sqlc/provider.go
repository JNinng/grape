package pg_sqlc

import (
	"grape/internal/biz"

	"github.com/google/wire"
)

// ProviderSet 是 pg_sqlc 能力（关系存储）的 Wire 依赖注入集合。向量存储
// 与内嵌 OSS 在 vector / oss 子包，其 ProviderSet 与本集合并列聚合；本包
// 提供它们依赖的 *Data、检索留痕与模型调用留痕仓储、模型目录（文本模型
// + 嵌入空间解析）、OSS 端点解析与模型密钥加解密设施。NewSystemData
// 当前无消费方，留在集合里供将来系统库消费方按用途注入 SystemData。
var ProviderSet = wire.NewSet(
	NewData, NewSystemData, NewUserRepo, NewAssignmentOptionRepo, NewDeptRepo,
	NewUserGroupRepo, NewRoleRepo, NewMenuRepo, NewDictRepo, NewConfigRepo,
	NewTenantConfigRepo, NewFeatureFlagRepo, NewTranslationRepo, NewTenantRepo,
	NewPlatformTenantGuard, NewSessionRepo, NewAuditLogRepo, NewRetrievalLogRepo,
	NewModelCallLogRepo, NewAuditRepo, NewGrantRepo, NewPermissionRepo,
	NewQuerySchemaRepo, NewDiagnostics, NewSessionMaintenance, NewModelCatalog,
	NewSecretCipher, NewKnowledgeRepo, NewKnowledgeFileRepo, NewModelRepo,
	NewOSSConfigResolver,
	wire.Bind(new(biz.TextModelCatalog), new(*modelResolver)),
	wire.Bind(new(biz.EmbedderResolver), new(*modelResolver)),
	wire.Bind(new(biz.RerankModelCatalog), new(*modelResolver)),
	wire.Bind(new(biz.OSSConfigResolver), new(*ossConfigResolver)),
)
