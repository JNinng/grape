# agent

trpc-agent-go / trpc_agent_go_ack 框架的适配层：模型 provider、LLMAgent、runner、AG-UI handler 与 ack 许可网关只在这里构造，由 server 经 service 唯一挂载。本层是框架与 grape 分层之间的唯一接缝，框架类型不向两侧渗透。

## 定位与边界

- 框架对象的装配与生命周期（Close/cleanup）全部收敛在本层；`server` 只拿 `Stack.Handler()` 挂路由，`service` 只做手写端点注册与链上桥接，都不重复装配框架对象。
- `biz` 禁止 import 本包与任何框架包（trpc-agent-go / ack）；领域用例与 DO 保持纯领域形态。业务工具在本层声明、内部调 biz 用例完成数据访问（RLS/@scope 由存储层会话门面强制，工具不直连 SQL、不感知租户过滤），框架不直连存储。
- 框架对 grape 存储的入口以接口注入：会话存储（`session.Service`；生产装配走框架 `session/postgres`——独立 schema `agent` + `agent_` 表前缀，部署面预建表，运行期 `WithSkipDBInit`，属文档化「设施面」例外：表无 RLS、不存租户业务数据，租户隔离靠 userKey 应用层编码，见根目录 permission.md；单测装配走 `NewMemorySessionService`，进程内重启即失。会话表 DDL 见 [../data/pg_sqlc/sqlc/agent_session.sql](../data/pg_sqlc/sqlc/agent_session.sql)，不入 sqlc catalog，升级以框架建表模板 diff）与向量存储（`knowledge/vectorstore.VectorStore`，由 [vectorstore.go](vectorstore.go) 把 biz 契约适配为框架形状（租户隔离的强制应用层适配器）；框架 pgvector store 自建连接且启动建表，不引入）。
- 身份与租户：AG-UI 的 UserIDResolver、ack 的 WithUserFunc 与业务工具的执行身份都从认证 ctx（`biz.IdentityFromContext`）取，userKey 编码（`tenant_id:user_id`）共用 `EncodeUserKey` 单一代码点；身份缺失一律 fail-closed，禁止回落框架默认用户 `"user"`。
- 工具授权：ack 网关持有工具分级静态表（见下）与全量工具集（`WithStaticTools`，恢复路径代执行按名解析）；每次 AG-UI 运行（含恢复路径续跑）经 `gateway.RunOptionResolver()` 挂许可策略，Confirm 工具阻塞等待人工决定。许可源是 [grants.go](grants.go) 的 `storedGrants`：ack 的 string UserID（userKey）经 `DecodeUserKey` 映射内部身份，评估走 `biz.GrantUsecase.Resolve`（会话级先于全局级）落 `agent_tool_grant` 表（IdentityTx + RLS + 应用层租户过滤双保险），同意 + remember 落库（会话级/全局级两形态，同键活跃许可幂等跳过），吊销后 Lookup 不命中自然重新确认；一切失败按未命中处理（fail-closed，代价只是重复确认）。策略源恒为弃权空实现（`noopPolicy`，abstain 不介入裁决；具名形态接入约束见该文件注释），分级表 conf 化暂缓。许可命中的授权终态行由授权终态观察挂点统一落行（[grants.go](grants.go) 无审计钩子，许可命中不与人工决定双写）。防风暴：LLMAgent 恒设 `WithMaxToolIterations`（单次运行工具调用轮数上限，拒绝后模型无视「不得重试」循环重试时按上限以 RUN_ERROR 终止）；未决确认由 AG-UI 运行超时（conf `agent.agui.timeout`，缺省 30m）结算为拒绝。
- ack 路由归属守卫：ack 的 live 决定路径不校验决定者与会话归属（等待者按会话+确认 ID 键控；仅恢复路径经 userKey 会话定位天然隔离），ackhttp 自身不做鉴权——grape 在唯一挂载点以 `ackOwnerGuard` 强制归属：threadId 取值源与 ackhttp 严格对齐（POST 决定只认请求体、GET 列表只认查询参数），目标会话不在调用者 userKey 名下即 404（不区分不存在与非你所有）。上游缺陷备案（升级 ack 或排查历史数据时需知）：若 live 路径被跨用户触达（守卫缺失或绕过），`settleLive` 的 savePending 以调用者 uid 定位会话失败仅记日志（ack confirm.go），工具执行不受阻，而属主会话状态里的待确认指针无人清理——归属者后续的合法决定会因等待者已消失被误路由到恢复路径（代执行重放，二次执行风险）；根目录 operations.md 的故障诊断口径引用本条。
- 业务审计写入：`AuditHook` 挂点经 `WithAuditHook` 可选注入（wire 生产装配传 biz.AuditUsecase，与复现视图查询端点共用同一用例实例；缺省 nil 即不审计，装配形态与接线前完全一致）。两个写入点：工具执行终态——[tool_audit.go](tool_audit.go) 的 wrapper 包住全量工具（echo 含），同时覆盖运行内执行（调用 ID 由框架注入 ctx）与恢复路径网关代执行（确认 ID 由决定审计面注入，ack Payload 两字段同源）；ack 决定结算（人工决定）——[ack_audit.go](ack_audit.go) 在归属守卫内层拦截决定路由，恰在 `gateway.Decide` 返回后写确认行（live 工具行由执行终态自然落库；recovery 代执行工具行与确认行组合批量原子落库，Pending 预读失败时已归集的代执行工具行回退单边写，不丢审计行）；系统自动决定——[permission_audit.go](permission_audit.go) 的授权终态观察转写器把 ack `PermissionObserver` 的自动授权终态（层级放行/拒绝、策略判定、许可命中、取消/持久化失败结算）转写为授权确认审计行（决定词形 `auto` / `auto_deny` / `{name}_auto` / `{name}_deny`，path 恒 auto，confirmation_id 即 tool_call_id，decided_by 取评估 ctx 属主 userKey）。两类授权写入点互斥且共同覆盖：每次工具调用恰好一条授权行。语义纪律：deny 归一 `Remember=""`（ack 静默忽略、biz 校验拒绝非空）；decided_by 取网关身份回填值（userKey）；重试数记 0（框架不暴露，最终口径见根目录 operations.md）；摘要传原文、脱敏与截断归 biz 用例（脱敏注册表）；挂点写入失败只记日志、不中断运行（审计是旁路观察面），写入走独立短超时预算（WithoutCancel 保留身份与链路事实 + 5s，存储悬挂不反噬主流程）；工具自身 panic 同为执行终态，wrapper 落一条 status=error 审计行后原样 re-panic。
- 运行并发限流与观测：[concurrency.go](concurrency.go) 的 `runGate` 对 chat 运行入口（POST 前缀根）按认证身份 userKey 限制在途运行数（conf `agent.agui.max_concurrent_runs`，0 = 关闭默认，handler 原样透传零开销），超限 429 固定文案、身份缺失 fail-closed 401；只做并发上限单一维度（取舍声明见文件头注），计数为进程内内存态（多实例时是每实例上限，部署口径见根目录 operations.md）。观测经 `WithRunObserver` 注入 biz.AgentRunObserver（wire 生产装配传 ops 的 RuntimeMetrics）：门控占槽/释放配对推活跃连接数、拒绝推拒绝计数；工具终态 wrapper 与审计共用单一包装点顺带推工具计数/耗时（panic 记 error、代执行同样计数）。两者皆 nil 时零行为（既有测试语义不变）。
- 会话能力：/history 快照与 tail follow 开启（AG-UI track 事件持久化进 `agent.agent_session_track_events`）、cancel 生效；TTL 经 conf `agent.session_storage` 注入框架（0 值语义见 conf.proto 注释；track TTL 仅在正值时传入——框架对显式 0 的解释是禁用过期而非沿用 session TTL）。grape 不用 app 级 state 存租户业务数据（ack 的待确认指针存 Session.State，属框架会话设施内的授权状态而非租户业务数据，随会话生命周期管理）。agent 会话列表端点同在本层装配（[sessions.go](sessions.go)）：GET `{basePath}/sessions` 按认证身份 userKey 列出会话元数据（仅元数据 + offset 分页），HTTP 形态经 `Stack.SessionsHandler` 交 service 注册。
- 上下文压缩：conf `agent.compression` 翻译为 LLMAgent 同名选项（总开关关闭时不改写任何工具结果）；压缩只作用于请求投影副本，session 事件流保留原文，占位符携带 event_id 恢复引用。LLMAgent 恒开 `WithEnableOnDemandSession`，session_load 经框架 WindowService（session/postgres 已实现）取回原文片段。压缩占位符的消费端点同在本层装配（[eventwindow.go](eventwindow.go)）：GET `{basePath}/event-window?threadId&eventId&before&after` 按锚点事件取当前认证 userKey 名下会话的事件窗口（session 事件流即原文），供运维复现视图做「占位 ↔ 原文」对照与执行轨迹回查；before/after 各上限 50（fail-closed 不静默截断），HTTP 形态经 `Stack.EventWindowHandler` 交 service 注册。会话摘要仅留配置位 `agent.summarization`（当前恒关闭，未实现）。
- 观测面：ack 网关经 `ack.WithLogger` 桥 grape logger（本层装配）；trpc-agent-go 全框架日志门面与 LLM/工具 span 的 Tracer 桥接归 [../telemetry/](../telemetry/)（进程级全局桥接 + trace_id 注入与局限声明），不在本层重复装配。
- 模型层可靠性：conf `agent.llm.failover / hedge / logging` 三段各自独立启用（默认全关，零包装），装配在 [llm.go](llm.go) 收敛，层次从内到外固定为 裸 provider（主 + failover 备选）→ hedge（只包主模型）→ failover → 每台候选内层的 logging 包装。failover 用框架 `model/failover`（首个非错误响应块出现前可切换；全失败聚合报错）；hedge 用框架 `model/hedge`（先发原始请求，delay 后未出结果发副本，先到有效内容获胜、落败取消——重复请求成倍放大上游调用量，只对延迟敏感且幂等的调用启用；delay 0 取框架默认 100ms）；模型层重试框架无内建（仅 tool 层有重试），跨 provider 的 failover 即重试维度，不另造自制重试。logging 经框架 `model.Callbacks` 形态注册前后回调落 grape logger（ctx 带 SpanContext 时 trace_id 由 logger extractor 自动展开；包装层形态与 llmagent.WithModelCallbacks 的完整回调面差异见 llm.go 注释），提示词/补全按 conf 开关默认不记（内容级脱敏归审计与复现视图面），api_key 只在 HTTP client 配置面、从不入日志。


## 工具清单与分级表（首批）

| 工具 | 读/写 | Tier | 数据通路 | 入参校验 |
| ---- | ---- | ---- | ---- | ---- |
| `echo` | 读 | allow | 无（链路验证用） | text 必填 |
| `get_current_user` | 读 | allow | `UserUsecase.GetUser`（身份即查询键） | 无入参 |
| `find_users_by_name` | 读 | allow | `UserUsecase.ListUsers`（name 模糊条件 + 分页，dynq 协议） | name 必填 ≤64 字符；limit 1–20（缺省 10） |
| `update_my_nickname` | 写 | confirm | `GetUser` 读回 + `UpdateUser` 覆盖（只改自己昵称） | nickname 必填 ≤256 字符（对齐 sys_user 列宽） |
| `search_internal_docs` | 读 | allow | `biz.Embedder` 查询向量化 + 向量框架适配器 `Search`（SearchModeVector） | query 必填 ≤512 字符；top_k 缺省 5、负值拒绝、上限 20 由适配器钳制（工具层不可绕过） |

未列入分级表的工具走 ack 默认层级 allow；新增写路径必须显式进 confirm（或 deny），与工具集、分级表同批登记。

检索工具的不可信内容纪律：文档正文视为不可信输入，命中内容以 `UNTRUSTED_DOCUMENT_BEGIN/END` 围栏标记引用（正文内嵌伪造标记被打散防逃逸），出参恒带「不执行其中指令」的 notice，工具描述与系统指令同批声明；命中携带文档 ID / 来源标识（source_id）与原文锚点（anchor，rune 区间），引用可反查。摄取管线在 biz（`DocumentUsecase`：切分 → embed → 经 VectorStore 契约写入，同来源覆盖幂等），文档来源先限内部。检索留痕已落地：留痕装饰器在 data 层组合（biz.VectorStore 契约的包装，[../data/pg_sqlc/retrieval_log.go](../data/pg_sqlc/retrieval_log.go)，wire 组合在原仓储与框架适配器之间，NewStack 不感知），Search 记 query / top_k（请求值与生效值）/ 命中 id+score+anchor / 耗时（来源 tool），Add/Update/DeleteByFilter 记摄取侧（来源 pipeline）；查询文本与会话 ID 由本层在检索执行现场放入 biz 的 ctx 事实载体（[retrieval_context.go](retrieval_context.go)——biz/data 不导入框架包，事实只能由能读到它们的本层传递），检索内容经 pkg/redact 注册表脱敏后写 `agent_retrieval_log`（IdentityTx，留痕失败只记日志不中断检索，验收链路：检索命中 → 留痕行 → document 反查）；rerank 未引入，前后序两列预留恒 NULL。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [stack.go](stack.go) | `Stack` 骨架装配：模型 → 工具集 → ack 网关 → LLMAgent（工具 + OnDemandSession + 压缩选项翻译 + MaxToolIterations）→ core runner → AG-UI runner（RunOptionResolver 挂许可策略 + Bind 续跑 + /history follow）→ AG-UI server（chat / cancel / history）→ 外层 ServeMux（AG-UI 子树套运行并发门控，ack 子树套归属守卫，审计挂点非 nil 时再套决定路由审计面）；`WithAuditHook` 注入审计挂点（非 nil 时同时给 ack 网关挂授权终态观察转写器）、`WithRunObserver` 注入运行观测契约（非 nil 时工具清单包终态 wrapper），`Handler` 返回挂载物，`SessionsHandler` / `EventWindowHandler` 返回会话列表与事件窗口端点的挂载物，`BasePath` 供挂载方与超时跳过名单使用。 |
| [tool_user.go](tool_user.go) | 业务工具首批（上表）：框架 function 工具注册 → biz 用户用例调用；入参服务端校验与用户摘要投影（口令/审计列/归属不出工具层）。 |
| [tool_search.go](tool_search.go) | 检索工具 `search_internal_docs`：embedder 查询向量化 → 向量框架适配器 Search（唯一取用点）；不可信内容围栏（标记 + notice + 防逃逸打散）与来源引用投影；top_k 缺省 5、上限由适配器钳制。 |
| [tool_echo.go](tool_echo.go) | echo 最小只读工具，走框架 function 工具注册；业务工具经 biz 用例接入（tool_user.go），不在本层堆积。 |
| [ack_guard.go](ack_guard.go) | ack 路由会话归属守卫：解析 threadId（决定体/查询参数），会话不在调用者 userKey 名下即 404；形状错误放行给 ackhttp（400 语义不变）。 |
| [sessions.go](sessions.go) | agent 会话列表端点装配：`sessionList` 消费骨架既有的 session.Service 与 app 名（与 AG-UI / ack 同源），`listSessions` 程序化取用点（仅元数据 + offset 分页）与 HTTP 形态（分页非法 400、身份缺失 fail-closed 401、存储故障 500 固定文案、空会话返回空数组）；经 `Stack.SessionsHandler` 交 service 注册。 |
| [eventwindow.go](eventwindow.go) | 会话事件窗口端点装配：消费同一 session.Service 的 WindowService 能力取锚点事件窗口，投影为复现视图的原文 DTO（多 choice 正文拼接、工具调用全量展开）；参数缺失/非法 400、身份缺失 fail-closed 401、跨用户锚点统一 404、内部错误 500 固定文案，before/after 各上限 50；经 `Stack.EventWindowHandler` 交 service 注册（与 SessionsHandler 同序，先于 agent 前缀通配）。 |
| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码，日志经 WithLogger 落 grape logger（ack 日志 API 无 ctx，不带 trace_id——全框架日志桥接见 [../telemetry/README.md](../telemetry/README.md)）；WithTiers 静态分级（读 allow/写 confirm）、WithStaticTools 服务恢复代执行；许可源经参数注入（接授权存储，nil 供测试装配）、策略源恒弃权空实现（接入点预留）、观察挂点非 nil 时经 `WithPermissionObserver` 订阅自动授权终态（转写器见 [permission_audit.go](permission_audit.go)）。 |
| [vectorstore.go](vectorstore.go) | biz 向量契约 → 框架 `knowledge/vectorstore.VectorStore` 适配器：Add/Get/Update/Delete/Search 向量模式/DeleteByFilter/Count/GetMetadata 直映，Search 钳制 top_k 上限（20，检索 top_k 的唯一强制点）；keyword/hybrid 与 searchfilter DSL 无消费方、DeleteAll 归 super 管理口径、UpdateByFilter 无消费方，均显式 unsupported 并注明理由。 |
| [retrieval_context.go](retrieval_context.go) | 检索留痕的 ctx 事实接线：检索执行现场把会话 ID（框架 Invocation 携带）与查询文本放进 biz 中性载体（biz/data 不导入框架包，事实只能由能读到它们的本层传递），留痕装饰器从载体读取；事实缺失不伪造（留痕落 NULL）。 |
| [userkey.go](userkey.go) | userKey（`tenant_id:user_id`）编解码单一代码点：会话归属、许可归属与后续审计共用；缺租户/非正数用户拒绝。 |
| [grants.go](grants.go) | 工具授权装配：`storedGrants` 把 ack GrantSource 适配到 `biz.GrantUsecase`（userKey 解码 → 内部身份 → agent_tool_grant 表，失败按未命中 fail-closed）；`grantAdmin` 是授权管理端点（GET `{basePath}/grants?scope=self|tenant` 与 DELETE `{basePath}/grants/{id}`，挂进骨架 ServeMux）——本人列表/本人吊销不设码，全租户列表验 `agent:grant:query`、吊销他人验 `agent:grant:revoke`（403），不可见统一 404。 |
| [llm.go](llm.go) | 模型链装配：provider 名映射框架变体的 OpenAI 兼容适配器（base_url 留空走框架内置默认端点，超时逐候选落 HTTP client）；failover（主故障按优先级切备选、全失败聚合报错）、hedge（延迟敏感的重复请求、先到获胜落败取消）、logging（model.Callbacks 形态的脱敏回调日志）三段按 conf enabled 零包装接入，全缺省即裸 provider。 |
| [tool_audit.go](tool_audit.go) | 工具执行终态审计与观测 wrapper（审计与观测同点）：自建 CallableTool 包装器包住装配层全量工具（echo 含），Call 终态记工具调用行——调用 ID 取框架 ctx 注入（恢复路径代执行由决定审计面注入确认 ID）、会话 ID 取 Invocation、耗时自带计时、重试数记 0（框架不暴露，口径见根目录 operations.md）、参数/结果摘要传原文由 biz 侧脱敏；工具自身 panic 落一条 status=error 审计行后原样 re-panic；观测契约非 nil 时同点推送工具计数/耗时；命中恢复路径捕获器只归集不直写；审计写入走独立短超时预算（WithoutCancel + 5s）、挂点错误与 panic 只记日志不中断。选型说明：框架 AfterTool 回调看不到恢复路径代执行且无耗时，wrapper 是两条执行路径的单一覆盖面。 |
| [ack_audit.go](ack_audit.go) | ack 决定路由审计面：ackhttp.Mount 收具体网关无法装饰，在归属守卫内层拦截 `POST {prefix}/confirmation`——请求体得决定事实（与 ackhttp/归属守卫同一解码契约）、结算前 `gateway.Pending` 预读待确认负载（工具名/工具调用 ID，结算即删指针）、ctx 注入确认 ID 与捕获器供代执行归集、200 响应体得生效路径；live 只写确认行（工具行由执行终态自然落库）、recovery 与代执行工具行组合批量（无代执行单边批量 `RecordToolCallWithConfirmation(ctx, nil, conf)`；预读失败但捕获器已归集代执行行时回退单边写不丢工具行）、deny 归一 `Remember=""`、decided_by 取网关回填的同一 userKey；无结算（400/404/409）不审计。 |
| [permission_audit.go](permission_audit.go) | 授权终态观察转写器：把 ack `PermissionObserver` 的自动授权终态转写为授权确认审计行——放行 `auto`、拒绝 `auto_deny`、带来源为来源命名空间 `{source}_auto` / `{source}_deny`（构造与词形校验归 biz.SystemDecision），path 恒 auto、confirmation_id 即 tool_call_id、decided_by 取评估 ctx 属主 userKey、remember 恒空；来源名不合规/无调用 ID/无身份 fail-closed 记日志放弃，写入走同一短超时预算旁路纪律；人工决定不产生观察事件，仍由决定路由审计面落行（两路互斥，许可命中不双行）。 |
| [audit_hook.go](audit_hook.go) | 审计写入挂点 `AuditHook`：工具调用、授权确认与组合批量三个方法的形状，签名与 biz.AuditUsecase 同名方法一致（编译期断言固定）；Stack 经 `WithAuditHook` 可选注入（wire 生产装配传 biz.AuditUsecase），缺省 nil 即不审计（测试装配不受影响），写入失败只记日志、不中断运行。deny 决定按 ack 语义忽略 remember，接线处归一 `Remember=""` 再入参；系统决定（auto 词形）经观察挂点走同一挂点接口（见 [permission_audit.go](permission_audit.go)）。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/授权用例/embedder/运行观测契约/审计用例；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）、`NewVectorStore`（biz 向量契约的框架适配器；wire 侧向量供给经 data 层留痕装饰器两跳组合，见 [../data/pg_sqlc/provider.go](../data/pg_sqlc/provider.go)）。 |
| [concurrency.go](concurrency.go) | AG-UI 运行并发门控：chat 运行入口按 userKey 限制在途运行数（conf `agent.agui.max_concurrent_runs`，0 = 关闭默认），超限 429 固定文案、身份缺失 401；观测契约非 nil 时占槽/释放配对推活跃连接数、拒绝推拒绝计数；只做并发上限单一维度、进程内内存态（取舍与部署口径见文件头注与根目录 operations.md）。 |



## 与其他层的关系

```
server ──► service ──► agent（本层）──► trpc-agent-go / ack 框架
                │              │
                biz ◄──────────┘  （读认证身份 + 业务工具调用用例）
```

挂载链路与 SSE 超时治理见 [../server/README.md](../server/README.md) 与 [../service/README.md](../service/README.md)；本层不感知 HTTP 路由注册。

## 测试

测试与被测代码同目录：userKey 编解码边界、echo 工具行为、业务工具入参/身份面（[tool_user_test.go](tool_user_test.go)，经框架 CallableTool 转换面直调）、provider/配置缺省值、压缩选项接线与会话服务装配期校验；live 确认流（同意继续且工具真实执行、拒绝 denied 且不重试、跨用户决定 404、MaxToolIterations 上限终止）在 [confirm_live_test.go](confirm_live_test.go)（进程内会话存储 + acktest 脚本化模型 + 内存用户仓储）；审计接线（live 同意的工具行直写与确认行、deny+remember 归一、同意作用域原样入参、恢复路径同意的组合批量与拒绝的单边批量、挂点错误与 panic 不中断运行、echo 循环执行数与审计行数一致、wrapper 无调用 ID 跳过/失败终态/panic 不传播/捕获器归集）在 [audit_wire_test.go](audit_wire_test.go)（内存审计挂点替身，恢复路径以同一会话存储上的第二栈模拟重启）；审计加固（工具 panic 落 error 行后 re-panic、无结算 404 不审计三面全空、预读失败回退单边写、审计写入短超时预算放弃悬挂写、观测契约的成功/panic 终态计数与组合形态双路生效）同在该文件；授权终态观察转写器的转写契约（allow/deny 与来源命名空间映射、无调用 ID/无身份/来源名非法记日志放弃、写入失败与 panic 不传播）在 [permission_audit_test.go](permission_audit_test.go)，allow 层级（get_current_user）auto 行的真实落库与 RLS 租户边界在 [permission_audit_pg_test.go](permission_audit_pg_test.go)（`GRAPE_PERMISSION_TEST_ADMIN_DSN` 门控跳过）；运行并发门控（占槽/释放/按键隔离/关闭零包装的原语面，在途运行占槽、超限 429、他键不受限、关闭并发不受限、身份缺失 401、观测配对推送的装配面）在 [concurrency_test.go](concurrency_test.go)（受理证明以「待确认注册」承载：被拦运行到不了框架）；完整挂载链（无令牌 401、SSE 流、trace-id）守卫在 [../server/agent_route_test.go](../server/agent_route_test.go)，经 `NewStackWithOptions` 的 `WithModelOverride` 注入替身模型，不发真实网络请求；会话列表端点直测（分页缺省与边界、身份缺失与编码失败 fail-closed、userKey 归属过滤、存储故障 500 固定文案）在 [sessions_test.go](sessions_test.go)；事件窗口端点直测（参数缺失/非法 400、身份缺失与编码失败 fail-closed 401、跨用户锚点 404、窗口原文投影与助手工具调用展开、存储故障与 WindowService 缺失 500 固定文案）在 [eventwindow_test.go](eventwindow_test.go)，挂载链语义（无令牌 401、注册顺序先于 agent 前缀通配、跨用户不泄露、参数上限 400）在 [../server/agent_event_window_route_test.go](../server/agent_event_window_route_test.go)。

会话存储集成测试在 [session_pg_test.go](session_pg_test.go)（`GRAPE_SESSION_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_agent_session_test 上按 DeployFiles 全清单装载后，以真实 PostgreSQL 验证跨请求持久、跨用户 userKey 隔离、TTL 落库、压缩投影与事件原文分野、按 event_id 取回原文、/history 快照回放与重启持久、cancel 终止。

确认恢复路径集成测试在 [confirm_pg_test.go](confirm_pg_test.go)（`GRAPE_CONFIRM_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_agent_confirm_test 验证重启后孤儿确认经恢复路径的两种结算——同意侧网关代执行工具并拉起续跑（/history 可回放），拒绝侧合成 denied 续跑且工具不执行。「重启」以全新连接池 + 全新栈模拟（不 Close 旧栈：Close 会取消在途运行、按取消语义清理待确认指针，孤儿场景即被破坏）。

摄取检索集成测试在 [search_pg_test.go](search_pg_test.go)（`GRAPE_SEARCH_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_search_test 验证摄取→检索闭环（真实 VectorStore 契约实现 + 框架适配器 + 检索工具，embedding 用确定性替身不发真实网络请求）、RLS 关闭场景跨租户经工具与 biz 两路径均 0 命中、引用可反查（命中 ID 点查回切片原文）、同来源重摄整体覆盖、维度失配装配期拒绝、超限 top_k 被适配器收敛到上限。检索工具单测（入参边界、缺省 top_k、围栏防逃逸、来源引用投影、设施缺失错误透传）在 [tool_search_test.go](tool_search_test.go)。

工具授权单测在 [grants_test.go](grants_test.go)：GrantSource 适配器的 userKey 映射（畸形拒绝/失败 fail-closed）、会话级先于全局级命中、Save 的会话/全局作用域映射、管理端点 HTTP 面（列表/吊销/404/400/401 与权限码 403 边界）。集成测试在 [grants_pg_test.go](grants_pg_test.go)（`GRAPE_GRANT_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_grant_test 验证 live 全链路（remember=session 同意落库 → 同会话二次调用免确认，ack 评估 basis=grant）、吊销后重新确认、会话级/全局级作用域差异、重启（重建池）后许可仍生效、同键重复授权幂等跳过与吊销后重授、RLS 关闭场景跨租户不可见（应用层兜底）、管理端点权限码边界（种子角色经 fn_has_permission 库内判定）。

模型层可靠性单测在 [llm_reliability_test.go](llm_reliability_test.go)（替身模型不发真实网络请求；超时项用回环 httptest）：全缺省装配断言（failover/hedge/logging 全关返回裸 OpenAI 适配器，零包装）、failover 主故障切换与全失败聚合报错、hedge 先到获胜与落败副本取消（delay 取 1ms 极短固定值，胜者判定由慢/快候选的数量级差决定，无需时钟抽象）、hedge+failover 组合次序（failover 在外、hedge 只包主模型）、超时经 conf 真实生效、回调日志面（请求/响应元数据、提示词/补全按 conf 开关且默认不记、超长截断、错误响应升 WARN、ctx 带 SpanContext 时 trace_id 由 extractor 展开、api_key 恒不入日志、failover 每次候选尝试各记一条）。配置模板守卫（新段默认关闭）在 [../../cmd/configinit/main_test.go](../../cmd/configinit/main_test.go)。

留痕事实载体单测在 [retrieval_context_test.go](retrieval_context_test.go)：会话 ID 从框架 Invocation ctx 提取、非框架链路不伪造、空白查询不注入。检索→留痕行的集成验证（query/top_k/命中与实际一致、RLS 两口径、留痕失败不影响检索、管线摄取来源=pipeline）在 [../data/pg_sqlc/retrieval_pg_test.go](../data/pg_sqlc/retrieval_pg_test.go)（`GRAPE_RETRIEVAL_TEST_ADMIN_DSN` 门控跳过，专属库 grape_retrieval_test）。

