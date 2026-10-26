# agent

trpc-agent-go / trpc_agent_go_ack 框架的适配层：模型 provider、LLMAgent、runner、AG-UI handler 与 ack 许可网关只在这里构造，由 server 经 service 唯一挂载。本层是框架与 grape 分层之间的唯一接缝，框架类型不向两侧渗透。

## 定位与边界

- 框架对象的装配与生命周期（Close/cleanup）全部收敛在本层；`server` 只拿 `Stack.Handler()` 挂路由，`service` 只做手写端点注册与链上桥接，都不重复装配框架对象。
- `biz` 禁止 import 本包与任何框架包（trpc-agent-go / ack）；领域用例与 DO 保持纯领域形态。C1 起业务工具在本层声明、内部调 biz 用例完成数据访问，框架不直连存储。
- 框架对 grape 存储的唯一入口是会话存储（`session.Service`），以接口注入：生产装配走框架 `session/postgres`（独立 schema `agent` + `agent_` 表前缀，部署面预建表，运行期 `WithSkipDBInit`），属文档化「设施面」例外（表无 RLS、不存租户业务数据，租户隔离靠 userKey 应用层编码，见根目录 permission.md 与 TODO 决策 1/3）；单测装配走 `NewMemorySessionService`（进程内，重启即失）。会话表 DDL 见 [../data/pg_sqlc/sqlc/agent_session.sql](../data/pg_sqlc/sqlc/agent_session.sql)（不入 sqlc catalog，升级以框架建表模板 diff）。
- 身份与租户：AG-UI 的 UserIDResolver 与 ack 的 WithUserFunc 都从认证 ctx（`biz.IdentityFromContext`）取身份并经 `EncodeUserKey` 编码为 `tenant_id:user_id`；身份缺失一律 fail-closed，禁止回落框架默认用户 `"user"`。
- 会话能力（B2）：/history 快照与 tail follow 开启（AG-UI track 事件持久化进 `agent.agent_session_track_events`）、cancel 生效；TTL 经 conf `agent.session_storage` 注入框架（0 值语义见 conf.proto 注释；track TTL 仅在正值时传入——框架对显式 0 的解释是禁用过期而非沿用 session TTL）。grape 不用 app 级 state 存租户业务数据。
- 上下文压缩（B2）：conf `agent.compression` 翻译为 LLMAgent 同名选项（总开关关闭时不改写任何工具结果）；压缩只作用于请求投影副本，session 事件流保留原文，占位符携带 event_id 恢复引用。LLMAgent 恒开 `WithEnableOnDemandSession`，session_load 经框架 WindowService（session/postgres 已实现）取回原文片段。会话摘要仅留配置位 `agent.summarization`（当前恒关闭，未实现）。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [stack.go](stack.go) | `Stack` 骨架装配：模型 → LLMAgent（echo 工具 + OnDemandSession + 压缩选项翻译）→ core runner → ack 网关 → AG-UI runner（Bind 续跑 + /history follow）→ AG-UI server（chat / cancel / history）→ 外层 ServeMux；`Handler` 返回挂载物，`BasePath` 供挂载方与超时跳过名单使用。 |
| [userkey.go](userkey.go) | userKey（`tenant_id:user_id`）编解码单一代码点：会话归属、许可归属与后续审计共用；缺租户/非正数用户拒绝。 |
| [llm.go](llm.go) | OpenAI 兼容 provider 构造（TODO 决策 5）：provider 名映射框架变体，base_url 留空走框架内置默认端点，超时落 HTTP client。 |
| [tool_echo.go](tool_echo.go) | echo 最小只读工具，走框架 function 工具注册；C1 起业务工具经 biz 用例接入，不在本层堆积。 |
| [gateway.go](gateway.go) | ack 许可网关骨架：app 名与 AG-UI runner 一致，身份经 WithUserFunc 取同一编码；grants/policy 为 no-op 空实现（C1/C2 填充分级表、许可存储与策略源）。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewStack`（cleanup 释放 runner）、`NewSessionService`（框架 session/postgres：schema agent + agent_ 表前缀 + SkipDBInit，TTL 按 conf 注入，连接复用 master DSN——框架强制自建池）、`NewMemorySessionService`（单测/本地体验用进程内实现）。 |

## 与其他层的关系

```
server ──► service ──► agent（本层）──► trpc-agent-go / ack 框架
                │              │
                biz ◄──────────┘  （仅 IdentityFromContext 读取认证身份）
```

挂载链路与 SSE 超时治理见 [../server/README.md](../server/README.md) 与 [../service/README.md](../service/README.md)；本层不感知 HTTP 路由注册。

## 测试

测试与被测代码同目录：userKey 编解码边界、echo 工具行为、provider/配置缺省值、压缩选项接线与会话服务装配期校验；完整挂载链（无令牌 401、SSE 流、trace-id）守卫在 [../server/agent_route_test.go](../server/agent_route_test.go)，经 `NewStackWithOptions` 的 `WithModelOverride` 注入替身模型，不发真实网络请求。

会话存储集成测试在 [session_pg_test.go](session_pg_test.go)（`GRAPE_B2_TEST_ADMIN_DSN` 门控跳过）：专属库 grape_agent_b2_test 上按 DeployFiles 全清单装载后，以真实 PostgreSQL 验证跨请求持久、跨用户 userKey 隔离、TTL 落库、压缩投影与事件原文分野、按 event_id 取回原文、/history 快照回放与重启持久、cancel 终止。
