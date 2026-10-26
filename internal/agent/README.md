# agent

trpc-agent-go / trpc_agent_go_ack 框架的适配层：模型 provider、LLMAgent、runner、AG-UI handler 与 ack 许可网关只在这里构造，由 server 经 service 唯一挂载。本层是框架与 grape 分层之间的唯一接缝，框架类型不向两侧渗透。

## 定位与边界

- 框架对象的装配与生命周期（Close/cleanup）全部收敛在本层；`server` 只拿 `Stack.Handler()` 挂路由，`service` 只做手写端点注册与链上桥接，都不重复装配框架对象。
- `biz` 禁止 import 本包与任何框架包（trpc-agent-go / ack）；领域用例与 DO 保持纯领域形态。业务工具在本层声明、内部调 biz 用例完成数据访问（RLS/@scope 由存储层会话门面强制，工具不直连 SQL、不感知租户过滤），框架不直连存储。
- 框架对 grape 存储的入口以接口注入：会话存储（`session.Service`；生产装配走框架 `session/postgres`——独立 schema `agent` + `agent_` 表前缀，部署面预建表，运行期 `WithSkipDBInit`，属文档化「设施面」例外：表无 RLS、不存租户业务数据，租户隔离靠 userKey 应用层编码，见根目录 permission.md 与 TODO 决策 1/3；单测装配走 `NewMemorySessionService`，进程内重启即失。会话表 DDL 见 [../data/pg_sqlc/sqlc/agent_session.sql](../data/pg_sqlc/sqlc/agent_session.sql)，不入 sqlc catalog，升级以框架建表模板 diff）与向量存储（`knowledge/vectorstore.VectorStore`，由 [vectorstore.go](vectorstore.go) 把 biz 契约适配为框架形状，决策 6 的强制应用层适配器；框架 pgvector store 自建连接且启动建表，不引入）。
- 身份与租户：AG-UI 的 UserIDResolver、ack 的 WithUserFunc 与业务工具的执行身份都从认证 ctx（`biz.IdentityFromContext`）取，userKey 编码（`tenant_id:user_id`）共用 `EncodeUserKey` 单一代码点；身份缺失一律 fail-closed，禁止回落框架默认用户 `"user"`。
- 工具授权（C1，TODO §1.3）：ack 网关持有工具分级静态表（见下）与全量工具集（`WithStaticTools`，恢复路径代执行按名解析）；每次 AG-UI 运行（含恢复路径续跑）经 `gateway.RunOptionResolver()` 挂许可策略，Confirm 工具阻塞等待人工决定。许可源与策略源为 no-op 空实现（C2 接 `agent_tool_grant` 存储与策略），分级表 conf 化亦留 C2/E3。防风暴：LLMAgent 恒设 `WithMaxToolIterations`（单次运行工具调用轮数上限，拒绝后模型无视「不得重试」循环重试时按上限以 RUN_ERROR 终止）；未决确认由 AG-UI 运行超时（conf `agent.agui.timeout`，缺省 30m）结算为拒绝。
- ack 路由归属守卫：ack 的 live 决定路径不校验决定者与会话归属（等待者按会话+确认 ID 键控；仅恢复路径经 userKey 会话定位天然隔离），ackhttp 自身不做鉴权——grape 在唯一挂载点以 `ackOwnerGuard` 强制归属：threadId 取值源与 ackhttp 严格对齐（POST 决定只认请求体、GET 列表只认查询参数），目标会话不在调用者 userKey 名下即 404（不区分不存在与非你所有）。上游缺陷备案（升级 ack 或排查历史数据时需知）：若 live 路径被跨用户触达（守卫缺失或绕过），`settleLive` 的 savePending 以调用者 uid 定位会话失败仅记日志（ack confirm.go），工具执行不受阻，而属主会话状态里的待确认指针无人清理——归属者后续的合法决定会因等待者已消失被误路由到恢复路径（代执行重放，二次执行风险）；E3 部署文档写运维口径时应引用本条做故障诊断。
- 会话能力（B2）：/history 快照与 tail follow 开启（AG-UI track 事件持久化进 `agent.agent_session_track_events`）、cancel 生效；TTL 经 conf `agent.session_storage` 注入框架（0 值语义见 conf.proto 注释；track TTL 仅在正值时传入——框架对显式 0 的解释是禁用过期而非沿用 session TTL）。grape 不用 app 级 state 存租户业务数据（ack 的待确认指针存 Session.State，属框架会话设施内的授权状态而非租户业务数据，随会话生命周期管理）。agent 会话列表端点（A2）同在本层装配（[sessions.go](sessions.go)）：GET `{basePath}/sessions` 按认证身份 userKey 列出会话元数据（仅元数据 + offset 分页），HTTP 形态经 `Stack.SessionsHandler` 交 service 注册。
- 上下文压缩（B2）：conf `agent.compression` 翻译为 LLMAgent 同名选项（总开关关闭时不改写任何工具结果）；压缩只作用于请求投影副本，session 事件流保留原文，占位符携带 event_id 恢复引用。LLMAgent 恒开 `WithEnableOnDemandSession`，session_load 经框架 WindowService（session/postgres 已实现）取回原文片段。会话摘要仅留配置位 `agent.summarization`（当前恒关闭，未实现）。
- 观测面：ack 网关经 `ack.WithLogger` 桥 grape logger（本层装配）；trpc-agent-go 全框架日志门面与 LLM/工具 span 的 Tracer 桥接归 [../telemetry/](../telemetry/)（进程级全局桥接 + trace_id 注入与局限声明），不在本层重复装配。


## 工具清单与分级表（TODO 决策 4 首批）

| 工具 | 读/写 | Tier | 数据通路 | 入参校验 |
| ---- | ---- | ---- | ---- | ---- |
| `echo` | 读 | allow | 无（链路验证用） | text 必填 |
| `get_current_user` | 读 | allow | `UserUsecase.GetUser`（身份即查询键） | 无入参 |
| `find_users_by_name` | 读 | allow | `UserUsecase.ListUsers`（name 模糊条件 + 分页，dynq 协议） | name 必填 ≤64 字符；limit 1–20（缺省 10） |
| `update_my_nickname` | 写 | confirm | `GetUser` 读回 + `UpdateUser` 覆盖（只改自己昵称） | nickname 必填 ≤256 字符（对齐 sys_user 列宽） |
| `search_internal_docs` | 读 | allow | `biz.Embedder` 查询向量化 + 向量框架适配器 `Search`（SearchModeVector，D2） | query 必填 ≤512 字符；top_k 缺省 5、负值拒绝、上限 20 由适配器钳制（工具层不可绕过） |

未列入分级表的工具走 ack 默认层级 allow；新增写路径必须显式进 confirm（或 deny），与工具集、分级表同批登记。

检索工具的不可信内容纪律（TODO D2）：文档正文视为不可信输入，命中内容以 `UNTRUSTED_DOCUMENT_BEGIN/END` 围栏标记引用（正文内嵌伪造标记被打散防逃逸），出参恒带「不执行其中指令」的 notice，工具描述与系统指令同批声明；命中携带文档 ID / 来源标识（source_id）与原文锚点（anchor，rune 区间），引用可反查。摄取管线在 biz（`DocumentUsecase`：切分 → embed → 经 VectorStore 契约写入，同来源覆盖幂等），文档来源先限内部。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [stack.go](stack.go) | `Stack` 骨架装配：模型 → 工具集 → ack 网关 → LLMAgent（工具 + OnDemandSession + 压缩选项翻译 + MaxToolIterations）→ core runner → AG-UI runner（RunOptionResolver 挂许可策略 + Bind 续跑 + /history follow）→ AG-UI server（chat / cancel / history）→ 外层 ServeMux（ack 子树套归属守卫）；`Handler` 返回挂载物，`BasePath` 供挂载方与超时跳过名单使用。 |
| [tool_user.go](tool_user.go) | 业务工具首批（上表）：框架 function 工具注册 → biz 用户用例调用；入参服务端校验与用户摘要投影（口令/审计列/归属不出工具层）。 |
| [tool_search.go](tool_search.go) | 检索工具 `search_internal_docs`（D2）：embedder 查询向量化 → 向量框架适配器 Search（唯一取用点，决策 6）；不可信内容围栏（标记 + notice + 防逃逸打散）与来源引用投影；top_k 缺省 5、上限由适配器钳制。 |
| [tool_echo.go](tool_echo.go) | echo 最小只读工具，走框架 function 工具注册。 |
| [ack_guard.go](ack_guard.go) | ack 路由会话归属守卫：解析 threadId（决定体/查询参数），会话不在调用者 userKey 名下即 404；形状错误放行给 ackhttp（400 语义不变）。 |
| [sessions.go](sessions.go) | agent 会话列表端点（A2）装配：`sessionList` 消费骨架既有的 session.Service 与 app 名（与 AG-UI / ack 同源），`listSessions` 程序化取用点（仅元数据 + offset 分页）与 HTTP 形态（分页非法 400、身份缺失 fail-closed 401、存储故障 500 固定文案、空会话返回空数组）；经 `Stack.SessionsHandler` 交 service 注册。 |
| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码；分级静态表 + WithStaticTools 登记；grants/policy 为 no-op 空实现（C2 填充许可存储与策略源）。 |
| [vectorstore.go](vectorstore.go) | biz 向量契约 → 框架 `knowledge/vectorstore.VectorStore` 适配器（决策 6）：Add/Get/Update/Delete/Search 向量模式/DeleteByFilter/Count/GetMetadata 直映，Search 钳制 top_k 上限（20，D2 检索的唯一强制点）；keyword/hybrid 与 searchfilter DSL 无消费方、DeleteAll 归 super 管理口径、UpdateByFilter 无消费方，均显式 unsupported 并注明理由。 |
| [userkey.go](userkey.go) | userKey（`tenant_id:user_id`）编解码单一代码点：会话归属、许可归属与后续审计共用；缺租户/非正数用户拒绝。 |
| [llm.go](llm.go) | OpenAI 兼容 provider 构造（TODO 决策 5）：provider 名映射框架变体，base_url 留空走框架内置默认端点，超时落 HTTP client。 |
| [tool_echo.go](tool_echo.go) | echo 最小只读工具，走框架 function 工具注册；C1 起业务工具经 biz 用例接入（tool_user.go），不在本层堆积。 |
| [gateway.go](gateway.go) | ack 许可网关：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码，日志经 WithLogger 落 grape logger（ack 日志 API 无 ctx，不带 trace_id——全框架日志桥接见 [../telemetry/README.md](../telemetry/README.md)）；WithTiers 静态分级（读 allow/写 confirm）、WithStaticTools 服务恢复代执行；grants/policy 为 no-op 空实现（C2 填充许可存储与策略源）。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（依赖会话存储/向量存储/用户用例/embedder；cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |


## 与其他层的关系

```
server ──► service ──► agent（本层）──► trpc-agent-go / ack 框架
                │              │
                biz ◄──────────┘  （读认证身份 + 业务工具调用用例）
```

挂载链路与 SSE 超时治理见 [../server/README.md](../server/README.md) 与 [../service/README.md](../service/README.md)；本层不感知 HTTP 路由注册。

## 测试

测试与被测代码同目录：userKey 编解码边界、echo 工具行为、业务工具入参/身份面（[tool_user_test.go](tool_user_test.go)，经框架 CallableTool 转换面直调）、provider/配置缺省值、压缩选项接线与会话服务装配期校验；live 确认流（同意继续且工具真实执行、拒绝 denied 且不重试、跨用户决定 404、MaxToolIterations 上限终止）在 [confirm_live_test.go](confirm_live_test.go)（进程内会话存储 + acktest 脚本化模型 + 内存用户仓储）；完整挂载链（无令牌 401、SSE 流、trace-id）守卫在 [../server/agent_route_test.go](../server/agent_route_test.go)，经 `NewStackWithOptions` 的 `WithModelOverride` 注入替身模型，不发真实网络请求；会话列表端点直测（分页缺省与边界、身份缺失与编码失败 fail-closed、userKey 归属过滤、存储故障 500 固定文案）在 [sessions_test.go](sessions_test.go)。

会话存储集成测试在 [session_pg_test.go](session_pg_test.go)（`GRAPE_B2_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_agent_b2_test 上按 DeployFiles 全清单装载后，以真实 PostgreSQL 验证跨请求持久、跨用户 userKey 隔离、TTL 落库、压缩投影与事件原文分野、按 event_id 取回原文、/history 快照回放与重启持久、cancel 终止。

确认恢复路径集成测试在 [confirm_pg_test.go](confirm_pg_test.go)（`GRAPE_C1_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_agent_c1_test 验证重启后孤儿确认经恢复路径的两种结算——同意侧网关代执行工具并拉起续跑（/history 可回放），拒绝侧合成 denied 续跑且工具不执行。「重启」以全新连接池 + 全新栈模拟（不 Close 旧栈：Close 会取消在途运行、按取消语义清理待确认指针，孤儿场景即被破坏）。

D2 摄取检索集成测试在 [search_pg_test.go](search_pg_test.go)（`GRAPE_D2_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_vector_d2_test 验证摄取→检索闭环（真实 VectorStore 契约实现 + 框架适配器 + 检索工具，embedding 用确定性替身不发真实网络请求）、RLS 关闭场景跨租户经工具与 biz 两路径均 0 命中、引用可反查（命中 ID 点查回切片原文）、同来源重摄整体覆盖、维度失配装配期拒绝、超限 top_k 被适配器收敛到上限。检索工具单测（入参边界、缺省 top_k、围栏防逃逸、来源引用投影、设施缺失错误透传）在 [tool_search_test.go](tool_search_test.go)。
