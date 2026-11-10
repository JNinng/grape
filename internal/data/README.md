# data

存储基础设施集合。一能力一子目录，目录内自包含（装配、实现、声明资产、
生成产物）；能力目录实现 `biz` 声明的仓储接口，DO ↔ PO 转换与驱动错误
映射收在仓储内。接入规范见 [internal/README.md](../README.md) 的
[能力目录接入规范](../README.md#能力目录接入规范)。

## 能力目录

| 目录 | 职责 |
| --- | --- |
| [pg_sqlc](pg_sqlc/) | 关系存储：连接门面、关系仓储、声明资产与生成产物 |
| [pg_sqlc/vector](pg_sqlc/vector/) | 向量存储介质与检索留痕装饰器 |
| [pg_sqlc/deploy](pg_sqlc/deploy/) | 声明资产部署面：执行清单、口令占位替换、跨进程锁，不依赖运行期门面 |
| [openaiembedder](openaiembedder/) | 文本向量化设施（OpenAI 兼容端点） |

`data` 是存储后端选择点：[provider.go](provider.go) 聚合各能力
ProviderSet 与设施诊断面/维护面，当前静态选定 `pg_sqlc` 关系存储。
`pg_sqlc` 的两个子包只依赖根包，根包不反向引用。

`pg_sqlc` 内的声明资产（`sqlc/` 下的 schema、rls、operate、expose、
queries、seed）与生成产物（`gen/`，单 catalog）同目录；`gen/` 禁止手改。
SQL 对象的签名、属性与三态返回值见
[sqlc/README.md](pg_sqlc/sqlc/README.md)，数据库部署执行顺序见
[deploy_db.md](../../deploy_db.md)。

## 关键文件导览

| 文件 | 不可从名称直接推出的职责 |
| --- | --- |
| [pg_sqlc/data.go](pg_sqlc/data.go) | 双槽位（master/system）装配期校验；`Pool()` 只供无身份装配期探针，业务读写必须经事务门面 |
| [pg_sqlc/session.go](pg_sqlc/session.go) | 三类事务门面与 RLS 会话变量注入；Scope 只从这里进入查询 |
| [pg_sqlc/query_schema.go](pg_sqlc/query_schema.go) | 资源名→生成代码 schema 出口的纯函数注册表，只收录已暴露动态 List 的资源 |
| [pg_sqlc/config_repo.go](pg_sqlc/config_repo.go) | 模型目录解析：文本模型与嵌入空间选键（租户配置 > 全局配置 > conf），按租户行 > 平台行 > conf 找端点 |
| [pg_sqlc/model_manage.go](pg_sqlc/model_manage.go) | 模型目录仓储：租户行普通 DML、平台行经受控函数、密钥密文落库 |
| [pg_sqlc/secret_cipher.go](pg_sqlc/secret_cipher.go) | AES-256-GCM 密钥加解密（`GRAPE_MODEL_SECRET_KEY`，`v1:` 前缀） |
| [pg_sqlc/knowledge.go](pg_sqlc/knowledge.go) | 知识库与条目仓储（动态 List + 库内固定分页）；kb_library 的库级可见性经 `@scope` / `fn_check_visible` / `fn_can_operate_row` 强制，可见库枚举供检索用例与列库工具共用 |
| [pg_sqlc/session_repo.go](pg_sqlc/session_repo.go) | 会话仓储兼介质维护：过期清理与 token_hash 索引 REINDEX 判定 |
| [pg_sqlc/tenant_admin.go](pg_sqlc/tenant_admin.go) | 租户树管理、平台租户判定与删除前置受控计数 |
| [pg_sqlc/diagnostics.go](pg_sqlc/diagnostics.go) | PostgreSQL 原始压力指标采集（专题见 [diagnostics.md](pg_sqlc/diagnostics.md)） |
| [pg_sqlc/vector/vector_repo.go](pg_sqlc/vector/vector_repo.go) | 多嵌入空间向量介质的装配探针、metadata 条件翻译与手写参数化 SQL |
| [pg_sqlc/vector/retrieval_log.go](pg_sqlc/vector/retrieval_log.go) | 检索留痕装饰器与 wire 供给包装 `VectorStoreSupply` |
| [pg_sqlc/deploy/deploy.go](pg_sqlc/deploy/deploy.go) | `DeployFiles` 部署顺序单一事实源，测试种子在其后追加 |
| [openaiembedder/openaiembedder.go](openaiembedder/openaiembedder.go) | 按空间键动态解析端点、HTTP 客户端缓存、每次响应维度复核与禁用占位 |

## 事务门面

- `IdentityTx`：业务读写常规入口，注入事务级 `app.tenant_id` 并携带
  操作者，Scope 由此注入；身份缺失或非法直接拒绝。
- `TokenTx`：身份成立前按令牌哈希放行会话单行，用于校验、刷新与登出。
- `TenantTx`：只注入租户、不带操作者，仅用于登录点查与登录日志追加。
- 过期会话清理走池直连 SECURITY DEFINER 函数；受控写/计数函数在函数内
  复位或校验租户边界。完整例外清单见 [permission.md](../../permission.md)。

绕过门面即绕过 RLS 会话变量注入，普通连接不得直连数据库。三层权限模型
（租户边界、行级数据范围、功能权限码）与读写形态见
[permission.md](../../permission.md)。

关系仓储按领域分布在 `pg_sqlc` 根包：用户与分配、部门/用户组/角色/
菜单/字典、权限码与 Agent 工具授权、配置族、会话与审计、租户。各仓储
构造函数统一注册在 [pg_sqlc/provider.go](pg_sqlc/provider.go)。

## 审计与留痕

Agent 审计仓储 [audit_repo.go](pg_sqlc/audit_repo.go) 与检索留痕仓储
[retrieval_repo.go](pg_sqlc/retrieval_repo.go) 只做 append-only 写入与
分页读；租户边界以 RLS 为主，读侧 WHERE 再带同源租户谓词作应用层兜底，
RLS 关闭时仍不越租户。Agent 审计的批量写入在同一事务内原子落两表。

## 动态查询 schema 出口

[query_schema.go](pg_sqlc/query_schema.go) 把资源名映射到生成代码的 schema
出口，供查询协议接口取字段与操作符元数据。注册表只收录已暴露动态 List
的资源；新增资源接入 List API 时追加一行，出口不传能力收窄参数。

## 配置族与嵌入空间解析

全局配置 `sys_config` 是平台级表（无 tenant_id、无 RLS），写经受控写
函数；租户配置、功能开关与翻译是租户表，写为普通 DML，tenant_id 取
会话。平台信任边界（仅根租户可写全局配置）由 biz 用例经
`NewPlatformTenantGuard` 判定：`parent_id IS NULL` 即根租户，租户不存在
或已软删按非平台处理（fail-closed）。

[config_repo.go](pg_sqlc/config_repo.go) 的模型目录解析器同时实现
`biz.TextModelCatalog` 与 `biz.EmbedderResolver`：

- 选键：租户配置（`ai.text.model` / `ai.embedding.model`，兼容旧键
  `vector.embedder`）> 全局 `sys_config` 同名键 > conf（`agent.llm.model` /
  `vector.default_embedder`）。
- 找端点：`sys_model` 当前租户行 > 平台行 > conf 对应条目；停用、软删与
  未知键一律 fail-closed，不静默回落。
- 密钥：`api_key_cipher` 优先经 [secret_cipher.go](pg_sqlc/secret_cipher.go)
  解密，缺失时读 `api_key_env`；主密钥缺失或密文损坏按
  `ErrModelSecretUnavailable` 拒绝。
- 嵌入维度本期固定 1024；平台行只能经 `98_model_write.sql` 受控函数写入。

## 检索留痕装饰器

向量仓储经装饰器后对外供给（wire：`NewVectorStoreSupply` →
`NewLoggedVectorStore`），装饰器位于仓储之上、框架适配器与摄取管线之下。

- 留痕点：Search 记 query、请求/生效 top_k、过滤摘要、命中、耗时、
  来源取 ctx 声明（`biz.WithRetrievalSource`，知识检索用例缺省声明
  `console`，agent 工具链路显式声明 `tool`）；Add/Update/DeleteByFilter
  记摄取侧、来源 `pipeline`；
  Get/Delete/Count/GetMetadata/List 不留痕（点读与枚举不构成召回证据）。
- 每次操作一行，成败都记（status 区分）；每行一个独立 `IdentityTx`，
  留痕失败只记日志，不中断业务。
- query 与过滤摘要在落库前经 `pkg/redact` 的 retrieval 类别脱敏；
  query 按 4096 rune 截断；过滤摘要脱敏破坏 JSON 时降级为截断声明；
  命中最多保留 50 条。
- top_k 请求值取存储契约收到的 Limit（0 表示未指定），生效值在未指定
  或非正时取默认页大小 10。

## 向量介质

一表承载多嵌入空间：embedding 是裸 vector（维度随行），embedder 与
embed_dim 逐行声明身份，CHECK 把维度列与实际向量长度绑定。

- 检索以空间键等值 + 维度谓词命中对应维度的表达式部分 HNSW 索引；
  检索 SQL 的 `vector(N)` cast 必须与索引表达式逐字一致。
- 表无 RLS，租户隔离由应用层强制：每条 SQL 带 tenant_id，缺认证身份
  拒绝；写入侧身份同时冗余进 metadata，请求侧伪造不生效。
- 装配期探针校验部署表为多空间形状，从 `pg_indexes` 发现带
  `::vector(N)) vector_cosine_ops` 与 `embed_dim = N)` 的维度集合；conf
  声明的每个维度仍必须有索引（fail-fast），运行期空间校验以发现集合为准
  （动态模型目录产生的空间键在装配期未知）。
- `VectorFilter.Conditions` 支持 eq/ne/in/lt/lte/gt/gte 六类 metadata
  比较：键与值都走占位符，数值比较显式 `::bigint`；`UpdateMetadata` 以
  `metadata || patch` 合并，空过滤拒绝（用于知识库状态/有效期同步）。
- 框架适配器路径在 ctx 上携带固定条件（`doc_status` / `valid_from_unix` /
  `valid_to_unix` / 可选 `kb_id`），适配器并入 VectorFilter；知识检索用例
  路径直接把固定条件放进 `VectorFilter.Conditions`，不经 ctx 载体。

## 文本向量化设施

[openaiembedder](openaiembedder/) 不再在装配期固化空间端点：每次 Embed 经
`biz.EmbedderResolver.ResolveEmbeddingSpace` 按空间键动态解析（模型目录
租户/平台行优先，conf.VectorSpaces 兼容条目兜底），HTTP 客户端按
`base_url + timeout` 缓存；启用时仍逐空间要求 conf 条目 base_url 存在，
`vector.enabled=false` 返回禁用占位；每次 Embed 响应按解析出的空间维度
复核，未知空间按 `ErrEmbedderUnknown` 拒绝。

## 会话介质维护

[session_repo.go](pg_sqlc/session_repo.go) 同时实现
`biz.FacilityMaintenance`，动作先删后判：

- 过期清理：删除访问令牌过期超过宽限期（24h）或已撤销的会话，经池
  直连 SECURITY DEFINER 函数执行；跨租户资格由函数属主承担。
- 索引重建：`token_hash` 唯一索引的死元组绝对数超过 10000 且占比超过
  20% 才 REINDEX，判定放在清理之后；`REINDEX CONCURRENTLY` 必须以池直连
  顶层语句执行，权限不足（42501）按跳过处理并留痕。

## 租户管理与删除孤儿判定

租户仓储按租户树模型工作：每个租户管理自身与直接子租户，树外行等同
不存在；创建时 parent_id 只能省略或等于调用方租户，主键为 `t-` + 14 位
小写十六进制；停用/恢复是目标状态写，更新自身租户 status 与当前值不同
被拒绝。方法名带 `super` 表示管理整个租户实体，不表示绕过 RLS。

删除前在同一事务内锁行，并经 `fn_tenant_active_child_count` 统计未删除
直接子租户；有子拒绝。判定必须走受控函数：租户树 RLS 下祖父看不到孙行，
直接 count 会漏并留下孤儿；函数以 DEFINER 读取全局事实，但只返回计数、
不返回行数据，并在函数内校验 `app.tenant_id` 与目标可见性。创建路径对
父级行加锁，与删除锁串行化，杜绝「父删 → 子插」。

## 诊断面与维护面

诊断面实现 `biz.FacilityDiagnostics`，输出设施原始压力指标；维护面实现
`biz.FacilityMaintenance`，判定（阈值、对象选择）下沉介质，任务只消费
契约。

[provider.go](provider.go) 的 `NewFacilityDiagnostics` /
`NewFacilityMaintenance` 是聚合点，ops 将诊断样本统一输出到 `/metrics`。
postgres 是当前唯一设施，采集范围、指标清单、可见性边界与失败行为见
[pg_sqlc/diagnostics.md](pg_sqlc/diagnostics.md)。新设施按同一模式接入：
实现契约并追加到聚合点，不新增出口。

## 测试

测试按主题覆盖，不逐文件列断言：

- **租户隔离与事务纪律**：未设 `app.tenant_id` 读写全拒、跨租户不可见；
  审计、检索留痕与向量面在 RLS 关闭时仍不越租户。
- **仓储读写边界**：固定 CRUD 的软删复用、唯一冲突与错误映射
  （NotFound / AlreadyExists / InvalidArgument）；动态 List 的过滤、排序、
  分页，以及 `total` 不随游标漂移。
- **行级权限与分配**：`fn_can_*` 的可见性、归属、分配与撤销口径，候选
  过滤、整组替换的 manageable/locked 分区与自锁保护；受控写函数拒绝
  运行角色直写。
- **设施与介质**：诊断样本采集、会话清理与 REINDEX 判定、向量空间对账
  与索引命中、检索留痕装饰器的转发/失败不中断/脱敏/兜底；embedding 的
  多空间路由与维度漂移。
- **装配与部署**：连接槽位校验、debug 参数脱敏、部署清单与测试种子的
  追加关系。

集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 及向量、审计、
检索留痕三组专属 DSN 门控（分别使用 `grape_vector_test`、
`grape_audit_test`、`grape_retrieval_test` 库），未设置时自动跳过。
