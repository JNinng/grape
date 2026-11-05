# agent

trpc-agent-go / trpc_agent_go_ack 框架的适配层：模型、LLMAgent、runner、AG-UI handler 与 ack 许可网关只在这里构造，由 server 经 service 唯一挂载。本层是框架与 grape 分层之间的唯一接缝，框架类型不向两侧渗透。

## 边界

- 框架对象的装配与生命周期全部收敛在根包；`server` 只取 `Stack.Handler()` 挂路由，`service` 只做手写端点注册。
- `biz` 禁止 import 本层任何包与任何框架包。业务工具在 tools 包声明、内部调 biz 用例（RLS/@scope 由存储层强制，工具不直连 SQL）。
- 存储以接口注入：会话存储 `session.Service`（生产走框架 session/postgres，独立 schema `agent` + `agent_` 表前缀、部署面预建表，属文档化「设施面」例外，见根目录 permission.md；DDL 见 [../data/pg_sqlc/sqlc/agent_session.sql](../data/pg_sqlc/sqlc/agent_session.sql)）；向量存储经 tools 包适配器把 biz 契约转为框架形状。
- 身份一律从认证 ctx 取，userKey（`tenant_id:user_id`）编码只走 identity 包；缺失 fail-closed，禁止回落框架默认用户 `"user"`。

## 包结构导览

根包是唯一装配点。依赖单向无环：根包 → 全部子包；`httpapi → audit → identity`，`tools → identity`；子包只依赖 `biz` / `conf` / 框架，不反向 import 根包，也不被 agent 树以外的包消费。

| 包 | 职责 |
| ---- | ---- |
| 根包 `agent` | [stack.go](stack.go) 骨架装配（模型 → 工具 → ack 网关 → LLMAgent → runner → AG-UI → ServeMux）；[gateway.go](gateway.go) ack 网关与工具分级表；[llm.go](llm.go) 模型链（failover / hedge / logging，默认全关零包装）；[concurrency.go](concurrency.go) 按 userKey 的运行并发门控（429）；[provider.go](provider.go) Wire `ProviderSet`。 |
| [identity/](identity/) | userKey 编解码、`UserKeyFromContext`、`ErrMissingIdentity`。 |
| [tools/](tools/) | 业务工具（echo / 用户 / 检索）、检索留痕 ctx 事实接线、biz → 框架向量适配器（top_k 上限 20 的唯一强制点）。 |
| [audit/](audit/) | 审计挂点 `Hook`、工具终态 wrapper、恢复路径代执行归集器、自动授权终态转写器、共用写入纪律 `WriteAudit`。 |
| [httpapi/](httpapi/) | 会话列表、事件窗口、授权管理端点与许可源适配器；ack 路由的归属守卫与决定审计拦截器。 |
| [internal/testsupport/](internal/testsupport/) | 各包测试共享的替身与夹具，只允许 `_test.go` 导入；不 import agent 树任何生产包（否则形成导入环）。 |

## 关键约束

- **工具授权**：ack 网关持分级静态表与全量工具集（恢复路径代执行按名解析）；Confirm 工具阻塞等待人工决定。许可源落 `agent_tool_grant` 表，remember 支持会话级/全局级，一切失败按未命中处理（代价只是重复确认）。LLMAgent 恒设 `MaxToolIterations` 防模型无视拒绝循环重试；未决确认由运行超时（`agent.agui.timeout`，缺省 30m）结算为拒绝。
- **ack 归属守卫**：ack 的 live 决定路径不校验决定者归属、ackhttp 不鉴权，grape 在唯一挂载点强制：threadId 取值源与 ackhttp 严格对齐（POST 只认请求体、GET 只认查询参数），会话不在调用者名下即 404。
- **上游缺陷备案**：若 live 路径被跨用户触达（守卫缺失或绕过），ack `settleLive` 以调用者 uid 定位会话失败仅记日志，工具照常执行，而属主会话状态里的待确认指针无人清理——属主后续的合法决定会被误路由到恢复路径（代执行重放，二次执行风险）。
- **审计三写入面**（挂点 nil 即不审计，装配形态不变）：工具执行终态由 wrapper 写（同时覆盖运行内执行与恢复路径代执行）；人工决定由决定路由拦截器写（recovery 的代执行行与确认行组合批量原子落库）；自动授权终态由观察转写器写（词形 `auto` / `auto_deny` / `{name}_auto` / `{name}_deny`）。两类授权行互斥，每次工具调用恰好一条。写入走独立短超时预算，失败与 panic 只记日志、不中断主流程；脱敏归 biz 用例。
- **会话能力**：/history 快照与 tail follow、cancel 开启；压缩只改请求投影，事件流保留原文，占位符带 event_id，经事件窗口端点（before/after 各上限 50）回查原文。
- **观测**：运行观测经 `WithRunObserver` 注入 biz.AgentRunObserver；框架日志门面与 Tracer 桥接归 [../telemetry/](../telemetry/)，本层不重复装配。

## 工具清单与分级表

| 工具 | Tier | 数据通路 | 入参校验 |
| ---- | ---- | ---- | ---- |
| `echo` | allow | 无（链路验证） | text 必填 |
| `get_current_user` | allow | `UserUsecase.GetUser` | 无入参 |
| `find_users_by_name` | allow | `UserUsecase.ListUsers`（模糊 + 分页） | name ≤64；limit 1–20（缺省 10） |
| `update_my_nickname` | confirm | `UserUsecase.UpdateNickname`（只改自己） | nickname ≤256 |
| `search_internal_docs` | allow | 默认空间解析器 + embedder + 向量适配器 Search | query ≤512；top_k 缺省 5，上限 20 由适配器钳制；检索固定落在解析出的嵌入空间（租户配置 > 全局兜底） |

未列入分级表的工具走 ack 默认 allow；新增写路径必须显式进 confirm，工具落 tools 包、分级表在 gateway.go 同批登记。检索结果视为不可信输入：以 `UNTRUSTED_DOCUMENT_BEGIN/END` 围栏引用（内嵌伪造标记被打散），出参恒带「不执行其中指令」的 notice；检索留痕由 data 层装饰器写 `agent_retrieval_log`，会话 ID、查询文本与嵌入空间由 tools 包放入 biz ctx 载体。

## 与其他层的关系

```
server ──► service ──► agent ──► trpc-agent-go / ack 框架
                          │
                          └──► biz（读认证身份 + 业务工具调用用例）
```

## 测试

测试与被测代码同包；需要完整 Stack 的子包集成测试用外部测试包（`httpapi_test`、`audit_test`），替身与夹具统一在 testsupport。单测不发真实网络请求（模型用 acktest 脚本化替身）。挂载链守卫（无令牌 401、SSE、trace-id）在 [../server/agent_route_test.go](../server/agent_route_test.go)。

PG 集成测试按环境变量门控，未设置即跳过；各用专属库，经 `testsupport.SetupSuiteDatabase` 按 `deploy.TestDeployFiles()`（生产清单 + 测试种子 test_seed.sql）部署：

| 套件 | 环境变量 | 覆盖 |
| ---- | ---- | ---- |
| 根包 session_pg_test.go | `GRAPE_SESSION_TEST_ADMIN_DSN` | 持久、userKey 隔离、TTL、压缩投影、/history、cancel |
| 根包 confirm_pg_test.go | `GRAPE_CONFIRM_TEST_ADMIN_DSN` | 重启后孤儿确认的同意 / 拒绝恢复 |
| tools/search_pg_test.go | `GRAPE_SEARCH_TEST_ADMIN_DSN` | 摄取 → 检索闭环、跨租户隔离、top_k 钳制 |
| httpapi/grants_pg_test.go | `GRAPE_GRANT_TEST_ADMIN_DSN` | remember / 吊销 / 作用域 / 重启存活 / 权限码边界 |
| audit/permission_audit_pg_test.go | `GRAPE_PERMISSION_TEST_ADMIN_DSN` | auto 授权行落库与 RLS 边界 |
