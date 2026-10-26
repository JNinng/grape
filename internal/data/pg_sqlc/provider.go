package pg_sqlc

import "github.com/google/wire"

// ProviderSet 是 pg_sqlc 能力（关系存储）的 Wire 依赖注入集合。
// NewSystemData 当前无消费方，wire 不实例化；留在集合里供将来系统库
// 消费方按用途直接注入 SystemData。向量存储经 NewVectorStoreSupply
// （原仓储供给）+ NewLoggedVectorStore（D3 检索留痕装饰器）两跳产出
// 对外的 biz.VectorStore：框架适配器与摄取管线拿到的都是装饰后实例，
// 装配期维度探针随原仓储构造生效；NewVectorStore 本体不入集合，留给
// 测试与非 wire 消费方直用。
var ProviderSet = wire.NewSet(NewData, NewSystemData, NewUserRepo, NewSessionRepo, NewVectorStoreSupply, NewLoggedVectorStore, NewRetrievalLogRepo, NewAuditRepo, NewGrantRepo, NewDiagnostics, NewSessionMaintenance)
