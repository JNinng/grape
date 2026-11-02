// Package vector 是 pg_sqlc 能力内的向量存储设施：biz.VectorStore 契约的
// pgvector 实现与检索留痕装饰器。DDL 是超级用户面 ../sqlc/agent_vector.sql
// （不进 sqlc catalog，存取 SQL 手写参数化，不经 gen）；租户隔离由应用层
// 强制（每条 SQL 带租户过滤、缺认证身份拒绝），写读都经根包 IdentityTx
// 门面。本包依赖根包（*Data、IdentityTx、检索留痕仓储契约），根包不反向
// 引用本包。
package vector

import "github.com/google/wire"

// ProviderSet 是向量存储的 Wire 依赖注入集合：NewVectorStoreSupply（原
// pgvector 仓储供给，装配期维度探针随之生效）+ NewLoggedVectorStore（检索
// 留痕装饰器）两跳产出对外的 biz.VectorStore——框架适配器与摄取管线拿到
// 的都是装饰后实例。NewVectorStore 本体不入集合，留给测试与非 wire 消费方
// 直用。
var ProviderSet = wire.NewSet(NewVectorStoreSupply, NewLoggedVectorStore)
