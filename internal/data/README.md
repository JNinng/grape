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
| [pg_sqlc/oss](pg_sqlc/oss/) | 内嵌对象存储设施（gofakes3）：/s3/ 挂载面与 gofakes3.Backend 的 PG 实现 |
| [pg_sqlc/deploy](pg_sqlc/deploy/) | 声明资产部署面：执行清单、口令占位替换、跨进程锁，不依赖运行期门面 |
| [ossclient](ossclient/) | 对象存储 S3 客户端设施（aws-sdk-go-v2，端点取 sys_config oss.*） |
| [openaiembedder](openaiembedder/) | 文本向量化设施（OpenAI 兼容端点） |
| [openaicompleter](openaicompleter/) | 单次文本生成设施（OpenAI 兼容 chat completions） |
| [openaireranker](openaireranker/) | 检索重排设施（OpenAI 兼容 /rerank，Jina / Cohere 风格） |
| [gsetokenizer](gsetokenizer/) | 词元切分设施（biz.Tokenizer，go-ego/gse）：中文为第一目标，词典 embed 进二进制，摄取建索引与查询分词共用同一实例 |
| [docparse](docparse/) | 文件解析设施（biz.FileParser）：Go 内档 xlsx / docx / html 直接产 markdown，外部档（pdf / pptx / 图片）走独立解析服务 HTTP 端点 |

`data` 是存储后端选择点：[provider.go](provider.go) 聚合各能力
ProviderSet 与设施诊断面/维护面，当前静态选定 `pg_sqlc` 关系存储。
`pg_sqlc` 的三个子包只依赖根包，根包不反向引用。

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
| [pg_sqlc/config_repo.go](pg_sqlc/config_repo.go) | 模型目录解析：文本/重排模型与嵌入空间选键（租户配置 > 全局配置 > conf），按租户行 > 平台行 > conf 找端点；重排键完全未配置报 `ErrRerankUnavailable`（跳过该层哨兵），rerank 无 conf 兜底条目 |
| [pg_sqlc/model_manage.go](pg_sqlc/model_manage.go) | 模型目录仓储：租户行普通 DML、平台行经受控函数、密钥密文落库 |
| [pg_sqlc/secret_cipher.go](pg_sqlc/secret_cipher.go) | 模型密钥加解密：`module_key_encrypt` 组写入按 `write` 指针选版本，读取按自描述密文首段自动选材料（`pkg/secret` Registry 注入） |
| [pg_sqlc/knowledge.go](pg_sqlc/knowledge.go) | 知识库与条目仓储（动态 List + 库内固定分页）；kb_library 的库级可见性经 `@scope` / `fn_check_visible` / `fn_can_operate_row` 强制，可见库枚举供检索用例与列库工具共用 |
| [pg_sqlc/knowledge_file.go](pg_sqlc/knowledge_file.go) | 文件介质仓储（kb_file 固定 CRUD）：行 ID 派生对象键 `kb-file/<发号 ID>`，跨租户介质行受 RLS 隔离等同不存在 |
| [pg_sqlc/session_repo.go](pg_sqlc/session_repo.go) | 会话仓储兼介质维护：过期清理与 token_hash 索引 REINDEX 判定 |
| [pg_sqlc/tenant_admin.go](pg_sqlc/tenant_admin.go) | 租户树管理、平台租户判定与删除前置受控计数 |
| [pg_sqlc/diagnostics.go](pg_sqlc/diagnostics.go) | PostgreSQL 原始压力指标采集（专题见 [diagnostics.md](pg_sqlc/diagnostics.md)） |
| [pg_sqlc/vector/vector_repo.go](pg_sqlc/vector/vector_repo.go) | 多嵌入空间向量介质的装配探针、metadata 条件翻译与手写参数化 SQL |
| [pg_sqlc/vector/retrieval_log.go](pg_sqlc/vector/retrieval_log.go) | 检索留痕装饰器与 wire 供给包装 `VectorStoreSupply` |
| [pg_sqlc/model_call_repo.go](pg_sqlc/model_call_repo.go) | 模型调用留痕仓储（append-only 写入 + 过滤分页读）；场景/状态词表复检是落库前最后把门 |
| [pg_sqlc/oss/server.go](pg_sqlc/oss/server.go) | 内嵌 S3 服务的 /s3/ 挂载构造（挂载决策矩阵、外层 SigV4 校验 + 前缀剥离），专题见 [pg_sqlc/oss/README.md](pg_sqlc/oss/README.md) |
| [pg_sqlc/oss/backend.go](pg_sqlc/oss/backend.go) | gofakes3.Backend 的 PG 实现（oss_object 介质：覆盖写、Range、列举、限流、复制） |
| [pg_sqlc/deploy/deploy.go](pg_sqlc/deploy/deploy.go) | `DeployFiles` 部署顺序单一事实源，测试种子在其后追加 |
| [oss_config.go](oss_config.go) | biz.OSSConfigResolver 实现：sys_config oss.* 六键现场解析，键缺即按设施未配置拒绝 |
| [ossclient/ossclient.go](ossclient/ossclient.go) | biz.ObjectStore 的 S3 SDK 实现：端点按调用解析、客户端按端点+凭据缓存、预签名与错误映射 |
| [openaiembedder/openaiembedder.go](openaiembedder/openaiembedder.go) | 按空间键动态解析端点、HTTP 客户端缓存、每次响应维度复核与禁用占位；注入 ModelCallObserver 在端点事实成立后留痕（场景缺省回落 embed_document，输出记维度说明） |
| [openaicompleter/openaicompleter.go](openaicompleter/openaicompleter.go) | 按当前身份解析租户文本模型端点、单轮 user message 生成与 HTTP 客户端缓存；注入 ModelCallObserver 在端点事实成立后留痕（场景缺省回落 ingest_prefix，usage 随响应体采集） |
| [openaireranker/openaireranker.go](openaireranker/openaireranker.go) | biz.Reranker 的 /rerank 客户端：按当前身份解析重排模型端点、HTTP 客户端按端点+超时缓存、响应 results 按 index 还原为与输入对齐的分数（不覆盖全部下标按协议错误拒绝）；注入 ModelCallObserver 在端点事实成立后留痕（场景缺省回落 rerank，输出只记分数条数说明） |
| [gsetokenizer/gsetokenizer.go](gsetokenizer/gsetokenizer.go) | biz.Tokenizer 的纯 Go 分词实现（go-ego/gse）：DAG 无 HMM 确定性切分，词典/停用词 embed 装载，词元小写归一并过滤停用词；词法召回的正确性前提是摄取与查询两侧共用本实例 |
| [docparse/docparse.go](docparse/docparse.go) | biz.FileParser 两档实现：嗅探 MIME 是唯一路由依据；外部档 POST {endpoint}/parse（嗅探事实走自定义头），未配置端点按组合错误拒绝；注入 ModelCallObserver 只在外部档留痕（无场景回落放弃该行，原件字节不落留痕记 size/sha256） |

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

Agent 审计仓储 [audit_repo.go](pg_sqlc/audit_repo.go)、检索留痕仓储
[retrieval_repo.go](pg_sqlc/retrieval_repo.go) 与模型调用留痕仓储
[model_call_repo.go](pg_sqlc/model_call_repo.go) 只做 append-only 写入与
分页读；租户边界以 RLS 为主，读侧 WHERE 再带同源租户谓词作应用层兜底，
RLS 关闭时仍不越租户。Agent 审计的批量写入在同一事务内原子落两表。

模型调用留痕的写入方是 `biz.ModelCallObserver` 观察面
（`ModelCallLogUsecase` 实现，wire 绑定为同一实例）：四个模型/类模型调用
能力目录（openaicompleter / openaiembedder / openaireranker / docparse）
在「解析出端点事实之后、调用终态（含 error）」构造载体并交给观察面——
身份缺失放弃该行、场景词表由应用层把门、脱敏（`model_call` 类别
4096 rune）与错误摘要（512）在观察面完成、独立 5s 短超时旁路写入、失败
只记日志不反噬主流程。场景与作用文档由调用链路经 ctx 载体声明
（`biz.WithModelCallScene` / `WithModelCallDocument`），缺失按目录声明的
缺省回落（embed→embed_document、completer→ingest_prefix、
reranker→rerank；docparse 无回落放弃该行），不伪造其他值。向量产物与
原件字节不落留痕（embedder 记维度说明、reranker 记分数条数说明、docparse
记 size/sha256）。agent 对话 LLM 不经本面（会话事件流已承载，避免双写）。

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

- 选键：租户配置（`ai.text.model` / `ai.embedding.model`）> 全局
  `sys_config` 同名键 > conf（`agent.llm.model` / `vector.default_embedder`）。
- 找端点：`sys_model` 当前租户行 > 平台行 > conf 对应条目；停用、软删与
  未知键一律 fail-closed，不静默回落。
- 密钥：`api_key_cipher` 优先经 [secret_cipher.go](pg_sqlc/secret_cipher.go)
  解密，缺失时读 `api_key_env`；写入侧按 `module_key_encrypt` 组的
  `write` 指针选版本，读取按密文首段「基础名@版本」自动选材料。业务键
  未登记、注册表缺失或密文损坏一律按 `ErrModelSecretUnavailable` 拒绝。
- 嵌入维度本期固定 1024；平台行只能经 `98_model_write.sql` 受控函数写入。

## 检索留痕装饰器

向量仓储经装饰器后对外供给（wire：`NewVectorStoreSupply` →
`NewLoggedVectorStore`），装饰器位于仓储之上、框架适配器与摄取管线之下。

- 写入方有两处，共同保住「一次操作一行」：混合检索编排层（biz 知识检索
  用例）经 ctx 抑制标记（`biz.WithRetrievalLogSuppressed`）让装饰器跳过
  Search / SearchLexical 的逐路留痕，在完成处自写一行完整留痕（stages
  承载「召回 → 融合 → 重排 → 回包」阶段数组，查询文本与阶段内文本的
  retrieval 类别脱敏由编排层完成）；装饰器承接其余检索（框架侧单路直调，
  stages 为 NULL——不伪造空阶段）与摄取侧。
- 留痕点：Search 与 SearchLexical 记 query、请求/生效 top_k、过滤摘要、
  命中、耗时、来源取 ctx 声明（`biz.WithRetrievalSource`，知识检索用例缺省
  声明 `console`，agent 工具链路显式声明 `tool`，未声明回落 `tool`；词法路
  的 query 记分词前原文，脱敏口径与向量路一致）；Add/Update/MarkSuperseded/
  DeleteByFilter 记摄取侧、来源 `pipeline`（MarkSuperseded 是重摄差量对账
  的淘汰动作，条件摘要记被退役的切片 ID）；
  Get/Delete/Count/GetMetadata/List 不留痕（点读与枚举不构成召回证据）。
- 每次操作一行，成败都记（status 区分）；每行一个独立 `IdentityTx`，
  留痕失败只记日志，不中断业务。
- query 与过滤摘要在落库前经 `pkg/redact` 的 retrieval 类别脱敏；
  query 按 4096 rune 截断；过滤摘要脱敏破坏 JSON 时降级为截断声明；
  命中最多保留 50 条。
- top_k 请求值取存储契约收到的 Limit（0 表示未指定），生效值在未指定
  或非正时取默认页大小 10；编排层行的 top_k 两值取归一化后的 top_k
  （最终命中上限），过滤摘要记 `kb_ids` 可见库集合。

## 向量介质

一表承载多嵌入空间：embedding 是裸 vector（维度随行），embedder 与
embed_dim 逐行声明身份，CHECK 把维度列与实际向量长度绑定。

- 检索以空间键等值 + 维度谓词命中对应维度的表达式部分 HNSW 索引；
  检索 SQL 的 `vector(N)` cast 必须与索引表达式逐字一致。
- 词法召回（`SearchLexical`）与向量路共享同一文档宇宙：`lexical` 列由
  Tokenizer 分词构造 tsvector（Add/Update 同批填充），查询分词构造 OR
  tsquery，`ts_rank_cd` 排序（未归一，融合只看排名）；两路共用同一空间
  对账与条件计划器，GIN 部分索引只覆盖已建索引行（存量 NULL 重摄回填）。
- 表无 RLS，租户隔离由应用层强制：每条 SQL 带 tenant_id，缺认证身份
  拒绝；写入侧身份同时冗余进 metadata，请求侧伪造不生效。
- 装配期探针校验部署表为多空间形状（含 lexical 为 tsvector），从
  `pg_indexes` 发现带
  `::vector(N)) vector_cosine_ops` 与 `embed_dim = N)` 的维度集合；conf
  声明的每个维度仍必须有索引（fail-fast），运行期空间校验以发现集合为准
  （动态模型目录产生的空间键在装配期未知）。
- `VectorFilter.Conditions` 支持 eq/ne/in/lt/lte/gt/gte 与 is_null 七类
  metadata 比较：键与值都走占位符，数值比较显式 `::bigint`，is_null 判定
  `metadata->>key IS NULL`（键缺失或 JSON null，不消费比较值）；
  `UpdateMetadata` 以 `metadata || patch` 合并，`MarkSuperseded` 以同一
  合并写路径打 `superseded_at` 退役标记，二者空过滤拒绝。
- 重摄是差量对账（仍在新批的 ID 整行 Update、消失的现行行标退役、新 ID
  Add），退役行保留期内与现行行共存：检索固定条件、管理页分片枚举与
  `Count` 以 is-null 排除，`Get`（复现反查）不过滤。保留期与清理 SQL 见
  [operations.md](../../operations.md)「保留期与归档统一口径」。
- 框架适配器路径在 ctx 上携带固定条件（`doc_status` / `valid_from_unix` /
  `valid_to_unix` / 可选 `kb_id`），适配器并入 VectorFilter；知识检索用例
  路径直接把固定条件放进 `VectorFilter.Conditions`，不经 ctx 载体。

## 词元切分设施

[gsetokenizer](gsetokenizer/) 实现 `biz.Tokenizer`（纯 Go 分词，
go-ego/gse，中文为第一目标）：词典与停用词表 embed 进二进制（运行期不依赖
外部词典文件），DAG 无 HMM 确定性切分，词元小写归一并过滤停用词。分词口径
是词法召回的正确性前提——摄取写路径（切片建词法索引）与查询路径（查询
分词）必须共用同一实现实例，wire 单例透传保证；数据库侧不引入分词扩展
（zhparser / pgroonga 会把口径绑死在部署扩展上）。

## 文本向量化设施

[openaiembedder](openaiembedder/) 不再在装配期固化空间端点：每次 Embed 经
`biz.EmbedderResolver.ResolveEmbeddingSpace` 按空间键动态解析（模型目录
租户/平台行优先，conf.VectorSpaces 兼容条目兜底），HTTP 客户端按
`base_url + timeout` 缓存；启用时仍逐空间要求 conf 条目 base_url 存在，
`vector.enabled=false` 返回禁用占位；每次 Embed 响应按解析出的空间维度
复核，未知空间按 `ErrEmbedderUnknown` 拒绝。

## 单次文本生成设施

[openaicompleter](openaicompleter/) 实现 `biz.Completer`（单次文本生成，
摄取管线上下文前缀的生成点）：每次 Complete 经
`biz.TextModelCatalog.ResolveTextModel` 按当前身份解析租户文本模型端点，
单轮 user message 调用 OpenAI 兼容 chat completions；不设独立设施开关，
租户未配置文本模型时 Complete 按 `ErrTextModelUnavailable` 拒绝，由消费方
按质量增强降级处理（见 ingest.md）。

## 检索重排设施

[openaireranker](openaireranker/) 实现 `biz.Reranker`（检索重排，知识检索
管线 RRF 融合之后的质量增强层）：每次 Rerank 经
`biz.RerankModelCatalog.ResolveRerankModel` 按当前身份解析重排模型端点，
POST OpenAI 兼容 /rerank（Jina / Cohere 风格），响应 results 按 index
还原为与输入顺序对齐的分数（端点返回不覆盖全部下标按协议错误拒绝）；不设
独立设施开关，可用性完全由重排模型解析决定——租户与全局均未配置
`ai.rerank.model` 时按 `ErrRerankUnavailable` 拒绝（消费方按跳过该层
处理，行为与无重排一致），键有值而行停用/软删/类别不符按配置错误拒绝
（消费方降级为融合序并记日志）。

## 对象存储设施

内嵌 OSS 是两半结构，业务侧只认 S3 协议（`biz.ObjectStore`，本期无消费
方，文件解析摄取接入时启用）：

- **服务端**（[pg_sqlc/oss](pg_sqlc/oss/)）：`conf.oss.enabled=true` 且
  sys_config `oss.provider=local` 时由 `NewHandler` 产出挂载 Handler，
  server 层 `/s3/` 链外直挂（SigV4 自证身份，不走 JWT 中间件链）；外层
  以含前缀的原始路径校验签名后剥前缀交给 gofakes3 路由。数据落
  `oss_object`（设施面无 RLS，bucket 即命名空间），单对象 ≤ 20MB，
  本期只开简单上传。专题见 [pg_sqlc/oss/README.md](pg_sqlc/oss/README.md)。
- **客户端**（[ossclient](ossclient/)）：`biz.ObjectStore` 的
  aws-sdk-go-v2 实现（Put/Get/Delete/PresignGet/PresignPut），每次调用经
  `biz.OSSConfigResolver` 现场解析 sys_config 的 `oss.*` 六键（无行值
  缓存，配置变更下一次调用生效），`*s3.Client` 按端点+凭据缓存复用；
  provider 为 minio / s3 / aliyun 时客户端指向外部端点，本服务不挂
  `/s3/`。键缺即按 `ErrObjectStoreDisabled` 拒绝（fail-closed）。
- AK/SK 明文落 sys_config（与 jwt.secret 同口径的设施面文档化例外，
  见 permission.md）；预签名 URL 支撑前端直传直下载，反向代理须透传
  Host 头（运维口径见 operations.md）。

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
  审计、检索留痕、模型调用留痕与向量面在 RLS 关闭时仍不越租户。
- **仓储读写边界**：固定 CRUD 的软删复用、唯一冲突与错误映射
  （NotFound / AlreadyExists / InvalidArgument）；动态 List 的过滤、排序、
  分页，以及 `total` 不随游标漂移。
- **行级权限与分配**：`fn_can_*` 的可见性、归属、分配与撤销口径，候选
  过滤、整组替换的 manageable/locked 分区与自锁保护；受控写函数拒绝
  运行角色直写。
- **设施与介质**：诊断样本采集、会话清理与 REINDEX 判定、向量空间对账
  与索引命中、词法召回的中文命中/条件口径/特殊字符 round-trip/GIN 索引
  命中、检索留痕装饰器的转发/失败不中断/脱敏/兜底；embedding 的
  多空间路由与维度漂移、文本生成的请求形状与错误映射；内嵌 OSS 的
  Backend 存储语义（覆盖写、Range、列举、限流、复制）、/s3/ 回环的
  S3 SDK 语义与 SigV4 拒绝矩阵、ossclient 的契约错误映射与预签名；
  docparse 三档内档解析的 markdown 产出形状（测试内构造容器字节）与
  外部档 HTTP 协议（httptest 替身、未配置拒绝）。
- **装配与部署**：连接槽位校验、debug 参数脱敏、部署清单与测试种子的
  追加关系。

集成测试由 `GRAPE_TEST_ADMIN_DSN` / `GRAPE_TEST_DSN` 及向量、审计、
检索留痕、模型调用留痕、内嵌 OSS 等专属 DSN 门控（分别使用 `grape_vector_test`、
`grape_audit_test`、`grape_retrieval_test`、`grape_model_call_test`、
`grape_oss_test` 等库），未设置时自动跳过。
