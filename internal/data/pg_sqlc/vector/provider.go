// Package vector 是 pg_sqlc 内的向量存储设施：biz.VectorStore 的 pgvector
// 实现与检索留痕装饰器。租户隔离由应用层强制，写读经根包 IdentityTx 门面；
// DDL 见 ../sqlc/agent_vector.sql（不进 sqlc catalog，SQL 手写参数化）。
package vector

import "github.com/google/wire"

// ProviderSet 是向量存储的 Wire 依赖注入集合：NewVectorStoreSupply（原仓储
// 供给，含装配期维度探针）→ NewLoggedVectorStore（留痕装饰器）两跳产出
// 对外的 biz.VectorStore。NewVectorStore 不入集合，留给测试直用。
var ProviderSet = wire.NewSet(NewVectorStoreSupply, NewLoggedVectorStore)
