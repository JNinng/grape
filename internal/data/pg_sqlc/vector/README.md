# vector — 向量存储设施（pgvector）

`biz.VectorStore` 的 PostgreSQL 实现（pgvector）与检索留痕装饰器。DDL 见
[../sqlc/agent_vector.sql](../sqlc/agent_vector.sql)（不进 sqlc catalog，存取
SQL 手写参数化落在本目录）；空间模型与探针契约见
[../../README.md](../../README.md)「向量介质」与「检索留痕装饰器」；
知识入库（摄取管线）逻辑见 [ingest.md](ingest.md)。

## 设计要点

- **多嵌入空间一表承载**：`agent.agent_document` 的 `embedding` 是裸 vector
  （无 typmod，维度随行），`embedder`/`embed_dim` 逐行声明空间身份，检索以
  空间键等值 + 维度谓词命中对应维度的表达式部分 HNSW 索引。
- **装配期 fail-fast**：`NewVectorStore` 校验空间注册表、表名白名单与
  `agent_` 前缀，并经启动探针核对部署表形状（裸 vector + embedder/embed_dim
  列齐全）与每个声明维度的表达式部分 HNSW 索引（`::vector(N)) vector_cosine_ops`
  与 `embed_dim = N)` 成对），缺一拒绝启动。动态模型目录（sys_model）产生的
  空间键不在 conf 注册表内，运行期以探针发现的索引维度集合为唯一校验依据
  （`resolveSpace`）。
- **租户隔离由应用层强制**：表无 RLS，身份只来自认证 ctx（`vectorIdentity`），
  每条 SQL 强制 `tenant_id` 过滤；不存在与跨租户统一落到
  `biz.ErrVectorDocNotFound`，不泄露存在性。写读经根包 `IdentityTx` 门面。
- **参数化纪律**：表名是唯一拼进 SQL 文本的配置值（构造期白名单拒绝注入
  形态）；过滤键/条件值全部走占位符，metadata 操作符只从封闭集合选择。
- **禁用语义**：`conf.vector.enabled=false` 时返回禁用占位实现，所有调用按
  `biz.ErrVectorStoreDisabled` fail-closed，不触库、不返回 nil。

## 文件导览

| 文件 | 内容 |
| --- | --- |
| [provider.go](provider.go) | Wire ProviderSet：`NewVectorStoreSupply` → `NewLoggedVectorStore` 两跳产出对外的 `biz.VectorStore` |
| [vector_repo.go](vector_repo.go) | 向量仓储介质实现：CRUD、余弦检索、条件删除/统计/枚举、空间解析与部署探针、禁用占位 |
| [retrieval_log.go](retrieval_log.go) | 检索留痕装饰器 `loggedVectorStore` 与 wire 供给包装 `VectorStoreSupply`：检索与摄取两链路一次操作一行留痕，脱敏与长度兜底 |
| [vector_test.go](vector_test.go) | 介质集成测试（真实 PostgreSQL，`GRAPE_VECTOR_TEST_ADMIN_DSN` / `GRAPE_VECTOR_TEST_DSN` 门控）：租户隔离、空间隔离、检索命中与分页 |
| [conditions_test.go](conditions_test.go) | metadata 条件与批量更新集成测试（同上门控）：固定条件检索、`UpdateMetadata` 只改 metadata、空过滤拒绝 |
| [vector_repo_test.go](vector_repo_test.go) | 仓储单测（纯内存）：配置校验、身份注入拒绝、注册表对账、过滤条件参数化翻译、禁用占位 |
| [retrieval_log_test.go](retrieval_log_test.go) | 留痕装饰器单测（替身仓储）：转发语义、留痕行内容、留痕失败不中断、脱敏与截断 |
| [retrieval_pg_test.go](retrieval_pg_test.go) | 留痕集成测试（真实 PostgreSQL，`GRAPE_RETRIEVAL_TEST_ADMIN_DSN` / `GRAPE_RETRIEVAL_TEST_DSN` 门控）：留痕落库、租户 RLS、管线摄取与分页 |

## 装配与留痕点

- ProviderSet 两跳装配避免原仓储与装饰器提供同一接口类型的装配环；
  `NewVectorStore` 不入集合，留给测试直用。
- 留痕点：`Search`（来源取 ctx 声明：console 或缺省 tool）、`Add`/`Update`/`DeleteByFilter`（来源
  pipeline）一次操作一行，失败行同样留痕；`Get`/`Delete`/`Count`/
  `UpdateMetadata`/`GetMetadata`/`List` 不留痕。留痕写入失败只记日志，不影响业务
  结果。
- top_k 口径：请求值取调用方 `Limit`（0=未指定），生效值在未指定时取存储
  默认页大小（10，与框架 pgvector 实现一致）。

## 测试

- 单测纯内存不触库；集成测试自建专属库（`grape_vector_test` /
  `grape_retrieval_test`）并按部署清单全量装载，以 `grape_u` 运行，未设
  对应 DSN 环境变量时自动跳过。
- 集成断言以无 RLS 场景验证跨租户不可召回（隔离正确性不依赖 RLS 存在）。
- 新增维度上线：`conf.vector.embedders` 加条目 +
  [../sqlc/agent_vector.sql](../sqlc/agent_vector.sql) 加同形态索引，探针
  装配期对账；集成测试维度取 1024（既有索引覆盖）。
