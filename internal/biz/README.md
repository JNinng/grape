# biz

领域层：只认 DO。定义 DO、用例、仓储接口（依赖反转的接缝）、类型化错误与选项类型
（如 `SessionOptions`）；领域规则（必填、缺省值、跨字段规则）只在这里落地。动态 List
以 `*dynq.Query` 作为查询协议随仓储接口跨界，biz 只透传、不解析。

依赖方向恒为 service/task → biz → data：biz 声明仓储接口，data 实现；对 `api/...` 的
唯一依赖是错误 reason 枚举，不感知 DTO、PO 与存储标签。分层图、跨层对象形态与新增
资源速查见 [internal/README.md](../README.md)。

## 关键约束

- 租户边界与行级数据范围由存储层强制（RLS + `@scope`/写前函数），biz 不重复判定；
  功能权限码由入口层判定。三层模型与落点见 [permission.md](../../permission.md)。

- `super` 方法仅用于身份已成立、接口有意绕过租户/范围约束的写面（菜单、全局配置
  经受控写函数）；租户实体管理面的 `super` 是对象粒度约定，约束照常生效。语义与
  例外清单见 [permission.md](../../permission.md)。

- 仓储契约的身份入口分两类：关系与授权仓储收 `Identity`（租户边界与审计操作者）；
  向量/审计/留痕契约不接收身份，实现从认证 ctx 提取并 fail-closed。身份成立前的
  凭据/令牌查询与登录日志追加属文档化例外（以租户 ID 或令牌哈希为定位事实），
  不使用 `super`，见 [permission.md](../../permission.md)。

- 同一 DO 贯穿 service/task → biz → data，禁止中途换形；流向图与跨层对象形态见
  [internal/README.md](../README.md)「分层与数据流向」。

- 口令哈希与令牌哈希只在 biz 单点生成/校验，仓储出入参只见哈希，明文不进日志；
  登录点查对「不存在」与「口令错误」同因返回（防枚举）。

- 全局配置写路径经 `PlatformTenantGuard` 强制平台根租户；其余配置族为租户表，
  走 RLS 与普通 DML。边界规则见 [permission.md](../../permission.md)。

- 受控写面（菜单、全局配置）由部署面 SECURITY DEFINER 函数承担，运行角色无直写
  权限；部署顺序见 [deploy_db.md](../../deploy_db.md)，SQL 对象索引见
  [sqlc/README.md](../data/pg_sqlc/sqlc/README.md)。

- Agent 工具调用/授权确认审计、检索留痕与模型调用留痕的证据面、写入点与
  关联键见 [replay.md](../../replay.md)。

- 工具授权：`FindActive*` 未命中以 `(nil, nil)` 表达正常控制流，`Resolve` 会话级先于
  全局级，`Create` 同键幂等；吊销对不存在与跨租户统一 404，无码调用方对他人授权
  得 403（同租户内 id 存在性可枚举，id 无内容泄露）。

- 向量设施的嵌入空间以 `Embedder` + `Dimension` 成对声明，实现按注册表对账；默认
  空间的有效优先级为请求显式 > 租户配置 > 全局 conf。介质语义见
  [data/README.md](../data/README.md)。

## 关键文件导览

| 文件 | 职责 |
| --- | --- |
| [user.go](user.go) | 用户 DO 与仓储/用例：创建缺省归属人与状态、个人中心本人读写与改密、角色分配/归属转移入口。 |
| [assignment.go](assignment.go) | 分配选择器与角色整组替换：候选查询归一化、版本 hash、`managed`/`locked` 分区的入参侧。 |
| [dept.go](dept.go) | 部门与组织树：创建/移动的父级与防环校验、树装配；闭包表是数据库派生数据。 |
| [usergroup.go](usergroup.go) | 用户组与成员映射：批量增删的去重排序、成员分页归一；组软删后映射残留由活跃过滤排除。 |
| [role.go](role.go) | 角色与数据范围/菜单授权整组替换；创建强制租户自定义类型与仅本人范围，预置角色不可删。 |
| [menu.go](menu.go) | 菜单树用例：父级/防环/软删校验与树装配，写方法带 `Super`。 |
| [dict.go](dict.go) | 字典类型与数据两级共用仓储；`value_type` 白名单在用例、宿主存在性在仓储事务内判定。 |
| [config.go](config.go) | 四类配置族（全局/租户/功能开关/翻译）的 DO、仓储与用例。 |
| [tenant.go](tenant.go) | 平台级租户实体管理：租户树创建规则、自身租户守卫、停用/恢复与软删。 |
| [auditlog.go](auditlog.go) | 登录/操作日志的只读查询与 append-only 追加入口。 |
| [auth.go](auth.go) | 登录编排：租户回落、防枚举、口令通过后判禁用、透明重哈希、委托会话签发；登录日志 best-effort。 |
| [session.go](session.go) | 会话与令牌生命周期（签发/校验/撤销/登出/刷新）及治理面 List；令牌只以哈希跨界。 |
| [password.go](password.go) | 口令哈希单点：argon2id 写入，按自描述标识校验历史算法并给出重哈希信号。 |
| [identity.go](identity.go) | 服务端身份事实与私有 ctx 载体；仅认证中间件注入，缺失由调用方 fail-closed。 |
| [permission.go](permission.go) | 功能权限码常量、入口判定用例与权限码枚举；判定每请求库内直查、应用内无缓存。 |
| [grant.go](grant.go) | Agent 工具授权：许可解析与本人/管理面吊销；管理面权限码在用例层判定。 |
| [audit.go](audit.go) | Agent 工具调用/授权确认审计：入参校验、决定词表配对、摘要脱敏与截断。 |
| [retrieval_log.go](retrieval_log.go) | 检索留痕契约与查询用例：读写 DO、`RetrievalLogRepo` 端口、查询过滤校验与 ctx 事实载体（会话/查询文本/来源/留痕抑制标记）。写入方有两处——data 层装饰器（单路直调与摄取侧）与检索编排层（混合检索自写完整行，经抑制标记跳过逐路留痕），脱敏义务随写入方。 |
| [model_call_log.go](model_call_log.go) | 模型调用留痕契约：场景词表与列宽把门、查询用例、`ModelCallObserver` 观察面（身份缺失放弃、脱敏截断、5s 短超时旁路写入、失败只记日志）与场景/文档 ctx 载体；四个能力目录经接口消费。 |
| [embedder.go](embedder.go) | 文本向量化与默认空间解析契约；嵌入空间经 ctx 载体传给框架适配器。 |
| [completer.go](completer.go) | 单次文本生成契约（质量增强面）：实现按当前身份解析租户文本模型，消费方必须把失败降级为主流程。 |
| [reranker.go](reranker.go) | 检索重排契约（query + documents 逐段打分，质量增强面）与重排模型目录解析：未配置（`ErrRerankUnavailable`）按跳过该层处理，调用失败降级融合序；实现按当前身份解析租户重排模型，解析出的模型键随 `RerankResult` 返回供阶段留痕记录。 |
| [tokenizer.go](tokenizer.go) | 词元切分契约（词法召回的正确性前提）：摄取建索引与查询分词必须共用同一实现实例（口径漂移即静默漏召回），本层只声明形状不消费。 |
| [objectstore.go](objectstore.go) | 对象存储契约（Put/Get/Delete/PresignGet/PresignPut，S3 语义）与端点解析契约：端点事实在 sys_config 的 oss.* 键，AK/SK 明文口径见 permission.md。 |
| [document.go](document.go) | 摄取管线：按 kind 分派切分（text 区分串切段（缺省段落空行，空串关闭）+ 超长段 rune 窗口二次切分；markdown/file 标题切分，节超长回退块级装箱与固定窗口）、heading_path 元数据、内容哈希切片 ID（`<source>#<index>#<hash8>`，空间参与哈希）、逐块 embed（库级上下文检索开关开启时先为每块生成上下文前缀，文档摘要一次、前缀只拼进 embed 输入，失败降级）、按新批 ID 差量对账写入（仍在 → Update 刷新并复活、消失 → 标记退役、全新 → Add）；重摄即迁移路径。 |
| [vectorstore.go](vectorstore.go) | 向量设施契约：文档/过滤/评分形状与读写方法面（多嵌入空间、metadata 条件（含 is_null 键缺失算子）与批量更新/退役标记 `MarkSuperseded`、枚举读面 `List`/`GetMetadata`、词法检索 `SearchLexical`——ts_rank_cd 分数未归一、与向量相似度跨方法不可比；`Count` 只计现行切片）。 |
| [knowledge.go](knowledge.go) | 知识库与条目用例：库/条目校验（按 kind 分支——file 条目 content 可空、原件事实必填）、切分参数（大小/重叠/区分串/上下文检索开关）与嵌入模型选择、chunk metadata 同步、删除清理（file 条目随宿主软删介质行并尽力删 OSS 对象）与重建索引（file 分支重取原件重解析）、条目实际分片枚举（按分片序，取自向量存储）与单切片反查（按切片 ID 点查原文与所属条目摘要，含退役标记，供留痕历史命中反查）、可见库集合解析（`VisibleLibraries`，检索与列库工具共用；条目读面随所属库可见性收窄）。 |
| [knowledge_file.go](knowledge_file.go) | 文件摄取用例（kind=file）：上传（识别 → 介质行 → OSS → 目录行 → 解析 → 摄取，介质成立前失败无副作用、之后失败留痕可重解析）、重解析（reparseDocument，解析产物回写目录行后按路由档位切分摄取）、失败路径的介质回收（cleanupMedia）。 |
| [fileparser.go](fileparser.go) | FileParser 契约与 MIME 路由档位（direct / native / external / unsupported）：嗅探 MIME 是唯一路由依据，扩展名只参与快速拒绝与纯文本的 markdown 判定；错误语义（类型不支持 / 乱码 / 过短）。 |
| [fileidentify.go](fileidentify.go) | 文件识别三层校验：扩展名白名单快速拒绝 → 内容嗅探（mimetype）→ 完整性限流（20MB + zip 炸弹预检：entry 数 512 / 解压总量 96MB）；嗅探回落 octet-stream 时按扩展名放行，扩展名与嗅探不一致记风险日志。 |
| [filecontent.go](filecontent.go) | 内容识别处理（解析产物入库前、切分前）：编码判定（BOM / UTF-8 严格性 / charset 判定 / GB18030 回落）→ 乱码占比阈值拒绝 → 换行规范化与规则级清洗（围栏逐字保留、孤立数字行与重复行剔除）→ 标题层级推断（第X章 / 一、 / （一） / 1.1 编号模式 + 孤行特征，跳级钳制）——heading_path 的产出点。 |
| [knowledge_search.md](knowledge_search.md) | 知识检索专题文档：管线总览（召回→融合→重排→回包）、参数纪律、可见性与固定检索条件、错误降级矩阵、留痕与 stages 口径的权威说明。 |
| [knowledge_search.go](knowledge_search.go) | 知识检索用例（前端与 agent 的统一入口）：入参校验与 top_k 钳制、可选查询变体（multi-query 的 biz 入口，改写落 agent 侧）、可见库集合现场解析（`library_id` 不可见/不存在统一 `ErrLibraryNotFound`）、召回集逐条双路召回（向量 + 词法，各取 2×top_k）后 RRF 融合（Σ 1/(k+rank)，同分按首次出现序）、装配重排层时对前 2×top_k 候选重排（未配置跳过、失败降级融合序；Score 在有/无重排时分别为 rerank 分/RRF 分）取 top_k、固定检索条件（状态/有效期 + `kb_id IN` 可见库集合）、错误语义（向量路失败整次失败，词法路失败降级仅向量路）、来源缺省 console 留痕声明（调用方已声明时保留）、编排路径自写一行完整留痕（经抑制标记跳过装饰器逐路留痕，stages 记阶段时间线，见 knowledge_search_stages.go）。 |
| [knowledge_search_pack.go](knowledge_search_pack.go) | 检索回包聚合（pack）：命中块按来源自动分档（同节 ≥2 命中整节、无 heading 结构相邻窗口、独占命中的短文档整文档、其余直回，粒度参数不暴露与 top_k 硬钳同纪律）、来源数上限 5、经 `store.List` 取来源全量块（失败/超防线降级仅命中块直回）、固定 4K token 预算两段装配（广度段每源至少一块、深度段按分补深、整文档超预算降级并同源收编），未入包单元按装配标记归因（merged 同源收编 / redundant 保底直回 / truncated 预算截断），返回 `KnowledgeSearchUnit`（console 展平 Hits、agent 工具逐单元围栏）。 |
| [knowledge_search_stages.go](knowledge_search_stages.go) | 检索分阶段留痕（stages）：阶段元素与回包单元的落库形态（含未入包 reason 三态）、阶段收集器（候选/单元上限 50、序列化载体）与编排层的 retrieval 类别脱敏义务（512 截断 + 4096 兜底，主查询与变体同口径）。 |
| [model.go](model.go) | 文本模型目录与密钥加解密契约；嵌入空间形状与模型类别/维度常量。 |
| [model_manage.go](model_manage.go) | 模型目录用例：租户/平台行 CRUD、密钥只写不读、embedding 维度 1024 与 model_key 白名单校验。 |
| [maintenance.go](maintenance.go) | 设施无关的诊断/维护契约；ops 与 task 只认契约。 |
| [metrics.go](metrics.go) | Agent 运行观测契约（运行数/限流拒绝/工具终态），由 ops 实现。 |
| [query.go](query.go) | 动态查询能力出口：资源 List schema 转发，不触库、无租户边界。 |
| [provider.go](provider.go) | Wire `ProviderSet`：用例构造函数与共用脱敏注册表。 |

## 测试

- 用例层以 mock 仓储验证领域规则：必填/列宽/状态哨兵、缺省值与归一化、树防环、
  去重排序、权限码与平台守卫、版本冲突与错误透传（覆盖组织、RBAC、字典、配置族、
  租户、用户与分配）。
- 安全单点：口令多算法校验与重哈希、登录防枚举与登录日志 best-effort、会话令牌
  哈希/元数据截断/登出幂等、身份 ctx 往返。
- Agent 面：审计词表配对与脱敏截断、授权会话优先/幂等/权限码边界、检索留痕分页
  与 fail-closed、模型调用留痕的观察面纪律（脱敏截断、身份缺失放弃、旁路失败
  不反噬、超时预算、panic 恢复、身份与动态查询透传）、摄取切分（固定窗口与区分串、markdown 标题切分）/切片 ID 内容哈希（同内容同 ID、改内容或换空间出新 ID）/维度对账/
  重摄差量对账（刷新/退役/新增三分支、内容改回旧版本复活）/中途失败/上下文前缀生成与降级（开关关闭零调用、失败降级无前缀、前缀只进 embed
  输入）、条目 kind 校验矩阵（创建接受/拒绝、更新不可变）、知识检索的
  校验/钳制/固定条件/设施缺失透传、RRF 融合（多路累加、并列名次按首次
  出现序、top_k 截断、nil 命中不计名次）、变体召回（逐条 trim 与上限校验、
  变体分别召回、词法路失败降级不失败）、重排编排（分数降序还原、候选
  2×top_k 截断与最终 top_k、同分保持融合序、无重排层/未配置/调用失败的
  降级路径）与回包聚合（整节/窗口/整文档/直回分档矩阵、预算两段装配的
  广度降级与深度跳过、整文档超预算降级与同源收编、来源数截断、命中块到
  回包单元的映射、全量块拉取失败/超防线降级、token 估算精确值）。
- 文件摄取：识别三层校验（改扩展名、zip 炸弹、超限、空文件）、内容识别
  （编码判定与 GB18030 回落、乱码占比拒绝、围栏保留与清洗、标题推断与
  跳级钳制）、上传用例的失败语义（识别拒绝无副作用、解析失败留痕目录行
  保留）、重解析的取回-解析-切分链路与删除的介质清理。
