# vectorstore.go — biz 向量契约的框架适配器

[vectorstore.go](vectorstore.go) 把 `biz.VectorStore` 契约适配为框架
`knowledge/vectorstore.VectorStore` 形状（`vectorStoreAdapter`）。这是向量
层租户隔离的**强制应用层适配器**：框架 pgvector store 自建连接池、启动建
表建索引且无法接 grape 的 pgx 池与身份事务，不引入；租户隔离由 biz 契约
的 data 实现（[../../data/pg_sqlc/vector/](../../data/pg_sqlc/vector/)）从
认证 ctx 强制，本适配器只做形状映射，不自行取用身份（避免第二套隔离
逻辑）。

## 方法映射

| 框架方法             | 映射                     | 说明                                                            |
| ---------------- | ---------------------- | ------------------------------------------------------------- |
| `Add`            | `store.Add`            | 形状映射；身份、维度与空间声明校验都在契约实现内                                      |
| `Get`            | `store.Get`            | `biz.ErrVectorDocNotFound` 透传，不二次包装丢失判定能力                     |
| `Update`         | `store.Update`         | 整体覆盖；嵌入空间取 ctx 事实（空间变更即显式重嵌入）                                 |
| `Delete`         | `store.Delete`         | 直接映射                                                          |
| `Search`         | `store.Search`         | 只落 `SearchModeVector`；top_k 超限钳到 `maxSearchTopK`（20），不报错也不放行 |
| `DeleteByFilter` | `store.DeleteByFilter` | `DeleteAll`（租户级全量清理）是 super 管理口径，拒绝                           |
| `UpdateByFilter` | —                      | 无消费方，`errVectorUnsupported` 显式拒绝                              |
| `Count`          | `store.Count`          | metadata 过滤计数                                                 |
| `GetMetadata`    | `store.GetMetadata`    | 框架侧 Limit/Offset 校验先行，分页语义与契约一致                               |
| `Close`          | —                      | 无自有资源；连接池与仓储生命周期归 data 层 cleanup                              |

## 检索逻辑

```
query 文本（工具层校验 ≤512 字符，top_k 缺省 5）
  → resolver.ResolveDefaultEmbedder → embedder.Embed（查询向量化）
  → 适配器 Search（SearchModeVector；Limit 超限钳到 maxSearchTopK=20）
  → 契约实现 resolveSpace（空间键/维度/向量长度三方对账）
  → 单阶段 HNSW 向量检索（无重排）→ 围栏包装出参
```

- **单阶段检索，无重排（rerank）**：当前管线只有 HNSW 余弦向量检索一段，
  命中即出参，没有 cross-encoder 重排或分数二次校准环节。若引入重排，
  落点在契约实现返回 `ScoredVectorDocument` 之后、检索工具围栏包装之前
  （工具层对框架 SearchQuery 无感知，适配器不应承载重排语义）。
- **top_k 三层收敛**：工具层缺省 5（`defaultSearchTopK`）→ 适配器硬上限
  20（超限静默钳制，模型侧无法绕过）→ 存储层 Limit 非正取默认页大小 10
  （正常链路到不了，是契约语义兜底）。三层均为代码级默认，不进 conf。
- **分数语义**：归一化余弦相似度 `1.0 - dist/2`，映射到 [0,1]（同向 1.0、
  正交 0.5），与框架 pgvector 实现一致；查询向量 float64 降精度 float32
  是存储语义。`MinScore > 0` 时作为 SQL WHERE 条件下推（`score >= $n`）。
- **排序与索引同源**：ORDER BY 走余弦距离算子本身，与表达式部分 HNSW
  索引表达式逐字一致（`::vector(N)` cast + `embed_dim = N` 谓词）才命中
  索引，否则退化为顺序扫描——维度由 `resolveSpace` 对账后内联，不是
  参数占位。
- **过滤与租户边界取交集**：`tenant_id`（认证 ctx）+ `embedder`/`embed_dim`
  （空间事实）+ 固定条件（状态/有效期/可选 kb_id，经中性载体）+ IDs/
  metadata 过滤同批 AND；过滤计划见
  [vector_repo.go](../../data/pg_sqlc/vector/vector_repo.go) 的
  `planVectorFilter`（键与值全走占位符）。

## 关键纪律

- **嵌入空间走 ctx 事实**：框架的 document/SearchQuery 类型不携带空间
  概念，`requireEmbedderSpace` 从 `biz.EmbedderSpaceFromContext` 读取
  （检索工具在 embed 现场经 `biz.WithEmbedderSpace` 注入），缺失即拒绝
  （fail-closed），不猜测空间——跨空间混排破坏相似度语义。
- **固定检索条件走中性载体**：状态/有效期/可选知识库条件由检索工具经
  `biz.WithRetrievalFilter` 传入（框架 SearchQuery 不携带条件 DSL），
  适配器读出后并入契约过滤。
- **top_k 上限在适配器强制**：模型侧可指定的返回条数统一收敛，检索工具
  只经本适配器访问向量、无法绕过；存储层语义（Limit 非正取默认页大小）
  在其下保持不变。上限是防滥用的代码级约束，不进 conf——conf 化反而
  给了绕过面。
- **显式拒绝优于静默降级**：keyword/hybrid 检索（需 tsvector 列与
  embedder 管线配合）、searchfilter 条件 DSL 翻译、DeleteAll、批量更新
  均以 `errVectorUnsupported` 报错，不静默降级成纯向量而改变检索行为。
- **精度转换是存储语义**：列类型 float4，`toFloat32` 对 float64 入参的
  降精度是存储语义的一部分，非适配缺陷。
- **metadata 只读冗余**：`toFrameworkDocument` 把嵌入空间身份复制进
  metadata 副本（列是本体），命中结果可观测所属空间，不回写调用方原 map。

## 测试

形状映射、空间事实缺失拒绝、top_k 钳制与未映射操作拒绝在
[vectorstore_test.go](vectorstore_test.go)（纯内存替身）；摄取→检索闭环、
跨租户隔离与 top_k 钳制的真实库面在 [search_pg_test.go](search_pg_test.go)
（`GRAPE_SEARCH_TEST_ADMIN_DSN` 门控）。
