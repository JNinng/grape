package pg_sqlc

import "github.com/google/wire"

// ProviderSet 是 pg_sqlc 能力（关系存储）的 Wire 依赖注入集合。
// NewSystemData 当前无消费方，wire 不实例化；留在集合里供将来系统库
// 消费方按用途直接注入 SystemData。向量存储（pgvector 介质与检索留痕
// 装饰器）在 vector 子包，其 ProviderSet 由 data 层与本集合并列聚合；
// 本包提供它依赖的 *Data、检索留痕仓储（NewRetrievalLogRepo）与默认
// 嵌入空间解析器（NewEmbedderResolver，读 sys_tenant_config）。
var ProviderSet = wire.NewSet(NewData, NewSystemData, NewUserRepo, NewSessionRepo, NewRetrievalLogRepo, NewAuditRepo, NewGrantRepo, NewDiagnostics, NewSessionMaintenance, NewEmbedderResolver)
