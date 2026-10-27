# 生产部署与运维口径

grape 单二进制（Go 后端 + 内嵌前端 SPA）的生产化部署与运维口径：多实例粘性、SSE 超时治理、优雅停机、保留期与归档、密钥管理、
框架升级检查单等。数据库部署执行顺序与升级步骤见
[deploy_db.md](deploy_db.md)，权限模型见 [permission.md](permission.md)，
本文不重复。

## 构建与发布

```bash
# 1. 前端构建（产物进 web/dist，编译期被 go:embed 收录；顺序不可颠倒）
make web
# 2. 后端编译（单二进制交付）
go build -o grape ./cmd/server
```

- `go:embed` 在 Go 编译期收录 `web/dist`，只改 Go 不改前端时无需重跑 npm；
  只改前端必须重新编译二进制。`web/dist` 缺失时有占位页兜底，`go build`
  不会失败，但交付前必须执行 `make web`。
- 运行配置初始化：`make config-init ARGS="--dir <目录>"` 从
  [cmd/configinit/template/config.yaml](cmd/configinit/template/config.yaml)
  输出模板（正文与 `configs/config.yaml` 逐字节一致，仅多一份头注说明，有测试强制），敏感项填值
  后即可 `go run ./cmd/server -conf <dir>` 启动。

## 配置与密钥管理

- 密钥一律经 `${ENV:default}` 占位注入（如 `agent.llm.api_key`、
  `vector.embedding.api_key`），仓库与配置模板不落明文；env source 仅识别
  `KRATOS_` 前缀——环境变量形态为 `KRATOS_<层级路径大写>`（如
  `KRATOS_DATABASE_SOURCE` 覆盖 `data.database.master.source`），无默认值
  的占位在变量缺失时展开为空串，应用侧按必填校验拒绝启动（fail-closed）。
- LLM 与 embedding 的 api_key 只在 HTTP 客户端配置面存在，从不进日志
  （模型层 logging 回调显式排除）；口令/令牌类日志面统一走
  [pkg/redact](pkg/redact/redact.go) 注册表（password/token/tool_param/
  retrieval 四类，日志 debug tracer、审计表、复现视图共用同一注册表）。
- 配置结构（[internal/conf/conf.proto](internal/conf/conf.proto)）变更时
  同批更新 `configs/config.yaml` 与 configinit 模板（测试强制逐字节一致）。

## 数据库部署与角色拆分

- 执行顺序的单一事实源是 `internal/data/pg_sqlc/deploy.go` 的 `DeployFiles`
  清单（19 个文件），一键部署 `make dbdeploy`（单事务、失败整体回滚），
  手工顺序与升级步骤见 [deploy_db.md](deploy_db.md)。
- 角色拆分（[role.sql](internal/data/pg_sqlc/sqlc/role.sql)）：`grape_u`
  供应用运行连接（真实承受 RLS 的非超级用户），`test_u` 供演示/验收。
  口令以 `{{GRAPE_U_PASSWORD}}` / `{{TEST_U_PASSWORD}}` 占位，dbdeploy
  执行前在内存中一次性替换，仓库与日志不落明文。CONNECT 授权依赖库名，
  无法写进 role.sql，由 dbdeploy 在事务内以 `current_database()` 动态补齐
  （手工部署见 deploy_db.md「部署后授权」）。
- 应用 DSN 必须以 `grape_u` 连接；超级用户绕过 RLS，禁止作为运行角色。

## Agent 设施面例外（agent schema 无 RLS）

- 框架会话存储六张表（`agent.agent_session_states` 等，部署文件
  [agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql)）是
  trpc-agent-go 框架自管表：无 RLS、不存租户业务数据，属文档化的
  「设施面」例外。租户隔离靠 userKey（`tenant_id:user_id`）应用层编码
  （`EncodeUserKey` 单一代码点，AG-UI / ack / 审计共用），见
  [permission.md](permission.md)。
- 访问控制口径：库内授权只授予 `grape_u`（部署文件内 GRANT），会话事件
  含原始提示词与工具结果，属敏感数据面——保留期见下文统一节；不得把
  业务数据写入框架会话表（应用层不使用 app 级 state 存租户业务数据）。
- 向量表 `agent.agent_document` 同为设施面（无 RLS，租户隔离由应用层
  VectorStore 适配器强制），授权收窄到该表、不覆盖 agent
  schema 全表。

## SSE 与超时治理

AG-UI 长流的治理方案（`configs/config.yaml` 有逐项注释）：

- `server.http.timeout: 0s`——Kratos 入站 filter 不再对全部路由统一限时
  （1s 会截断 AG-UI 的 SSE 长流：SSE 写循环监听请求 ctx.Done）；
- `server.http.request_timeout: 1s`——普通路由的请求超时由 server 层的
  路由级超时中间件按此值恢复；agent 前缀（`agent.agui.base_path`，默认
  `/agui`）在跳过名单内不施加；
- `agent.agui.timeout`（默认 1800s）——单次 AG-UI 运行的服务端兜底超时，
  同时是未决确认的最终结算时钟（超时由 ack 结算为拒绝）；
- `agent.agui.heartbeat_interval`（默认 15s）——SSE 心跳，保持中间设备
  不掐空闲流；反向代理/负载均衡的读超时必须大于心跳间隔。

部署检查：前置 LB/Nginx 的 proxy_read_timeout、连接空闲超时须大于
`agent.agui.timeout` 或至少显著大于心跳间隔，否则长流被中间层静默掐断。

## 运行并发限流

`agent.agui.max_concurrent_runs`（默认 0 = 关闭）：按认证身份（userKey）
限制同时在途的 chat 运行数，超限以 429 拒绝（固定文案，前端经错误横幅
可见）。`/history`、`/cancel`、`/sessions`、事件窗口与 ack 确认路由不受
限。取舍与语义：

- 只做「并发上限」单一维度，请求速率维度不做（速率突刺由运行超时与
  MaxToolIterations 收敛）；
- 恢复路径续跑（ack 决定触发的网关代执行/续跑运行）不经 chat 入口，
  不受门控限制、也不计入活跃连接 gauge——在途决定数与活跃连接数存在
  口径差，属既定取舍（风险由运行超时与 MaxToolIterations 收敛）；
- 计数为进程内内存态，多实例部署时是**每实例**上限而非全局上限；全局
  并发治理需外部协调面（网关侧按身份做信号量或接入配额服务）。

## 多实例与会话粘性

ack 的已知限制：**确认决定与运行必须同进程**。live 路径的
确认等待者在本进程内存中，决定请求被 LB 分到另一实例时等价于孤儿确认，
走 recovery 路径（网关代执行或合成拒绝）而非原运行继续。多实例部署口径：

1. 负载均衡按会话粘性路由：粘性键用会话归属（AG-UI/ack 请求体或查询参
   里的 threadId；不可得时退化按认证身份 userKey 粘性——同一用户的会话
   归属稳定）。cookie/源 IP 粘性在会话跨设备场景下不成立，不推荐。
2. 实例缩容/崩溃时，其会话的在途确认由接管实例的 recovery 路径恢复
   （重启后孤儿确认：网关代执行工具并续跑，或合成拒绝续跑）——功能上
   不丢决定，但 live 等待被拆成「拒绝 + 恢复续跑」两段，前端表现为一次
   额外的工具执行或拒绝结算；这是粘性失效时的既定降级语义，不是缺陷。
3. 故障诊断备案：ack live 决定路径不校验决定者与会话归属，grape 在挂载
   点以归属守卫（internal/agent/ack_guard.go）强制 404；上游缺陷备案
   （等待者消失后属主会话状态里的待确认指针无人清理）见 agent/README.md
   「ack 路由归属守卫」一节，排查「合法决定被误路由到恢复路径」时引用。

## 优雅停机

停机走 SIGTERM（kratos 监听后逆序停传输面），禁止 SIGKILL——进程强杀
可能丢失 flush 周期内的最后一条 SSE 事件（ack 已知限制）。

1. Kratos 停传输面（HTTP / gRPC / 维护任务，停止接入新连接）；
2. main 的 defer 触发 wire cleanup，逆序于构造：agent core runner 关闭
  （在途运行被取消，按取消语义清理待确认指针——粘性实例缩容时在途确认
  由其他实例 recovery 兜底）→ embedder → 向量留痕装饰器与向量仓储 →
  框架会话池 → grape 业务连接池 → OTel exporter shutdown（span flush）。

完整顺序以 `cmd/server/wire_gen.go` 生成的 cleanup 链为准，改依赖注入后
`make generate` 刷新，本文只描述语义不复制清单。

## 保留期与归档统一口径

各数据面的保留期策略与执行机制（统一裁定；**归档/清理任务不在本期
实现**，执行归运维——cron 定时或手动 SQL，口径如下）：

| 数据面 | 保留策略 | 执行口径 |
| --- | --- | --- |
| 框架会话（agent.agent_* 六表） | conf `agent.session_storage` 四项 TTL（0 = 永不过期，默认）；track 事件 TTL 0 = 沿用 session TTL | TTL 由框架 session/postgres 自带清理生效，无需外部任务；调整即改 conf 重启 |
| ack 未决确认 | **不设 TTL**：保持到决定或会话删除，与会话 TTL 同生命周期，不与终态审计混为一套清理 | 无需执行；泄漏指针的故障口径见上文粘性节备案 |
| 终态审计（agent_tool_call_log / agent_confirmation_log） | 证据面，默认全量保留；建议生产口径按合规要求定（如 180 天） | 超期行 `DELETE WHERE created_at < now() - interval '…'` 分批删除；归档目标（冷表/对象存储）由运维环境定，应用不参与 |
| 检索留痕（agent_retrieval_log） | 同审计口径（append-only 证据面，保留与执行口径以本节为准） | 同审计执行口径；两表可共用同一 cron 窗口 |
| 授权吊销行（agent_tool_grant.revoked_at 非空行） | 吊销是审计语义行（deploy_db.md 步骤 18 声明保留与清理口径）：保留供追溯，查询面已过滤不命中 | 同审计执行口径批量清理；清理后同键重授无历史可查，属预期 |
| 向量切片（agent.agent_document） | 无独立保留期 | 见下「向量孤儿」结论 |

**向量孤儿结论**（不设清理任务）：「文档删除后残留切片」的孤儿
面在当前代码形态下不存在——摄取管线（`biz.DocumentUsecase.Ingest`）的
覆盖语义是先 `DeleteByFilter(source_id)` 清旧再整批写入，且当前没有任何
绕过该语义的文档删除入口（无独立删除用例；`DeleteByFilter` 是唯一删除
通路，跨租户管理操作归 super 口径）。残余风险仅剩「DeleteByFilter 与
Add 之间崩溃」的中间态，重摄同一来源即可补偿（确定性 chunk ID + 覆盖
幂等）。若将来引入不经 DeleteByFilter 的文档删除路径，必须同步在
permission.md 的 super 管理口径下补清理义务说明（应用层孤儿清扫或运维
SQL），否则将产生真孤儿。

## 指标与告警

`/metrics`（免认证、链外直挂，Prometheus 文本格式）承载两族指标：

- 设施诊断（既有）：data 各能力目录的 `FacilityDiagnostics` 原始样本 +
  `grape_diagnostics_scrape_errors_total`（按设施分列的采集失败计数）；
- agent 运行面：`grape_agent_sse_active_connections`（活跃 SSE 运
  行数 gauge）、`grape_agent_run_limit_rejections_total`（并发限流拒绝
  counter）、`grape_agent_tool_calls_total`（工具终态 counter，tool/
  status 分列）、`grape_agent_tool_call_duration_seconds`（工具耗时
  histogram）。扩展点（检索耗时、LLM 计数与 token 用量、压缩指标）的
  接线落点见 [internal/ops/runtime_metrics.go](internal/ops/runtime_metrics.go)
  头注备案。

告警规则建议归监控配置层：拒绝计数突增（限流阈值过低或客户端失控）、
活跃连接长时高位（运行悬挂）、工具 error 占比突增（下游故障）、设施采集
失败（存储异常）。

## API 契约面与 openapi 口径

`openapi.yaml` 由 proto 生成（`make api`），**只覆盖 proto 契约面**
（`/v1/auth/*`、`/v1/users/*` 等）。手写端点不在 openapi 中，也**不做
假条目**——手写端点没有 proto 契约，openapi 条目只能手维护，必然漂移；
契约真相在各层 README 与本清单：

| 端点 | 归属 | 说明 |
| --- | --- | --- |
| `GET /metrics` | internal/ops | 运维指标，链外免认证 |
| `POST /agui/` | internal/agent | AG-UI chat 运行（SSE） |
| `POST /agui/history` | internal/agent | messages snapshot 回放（SSE） |
| `POST /agui/cancel` | internal/agent | 取消在途运行 |
| `GET /agui/sessions` | internal/agent | agent 会话列表 |
| `GET /agui/event-window` | internal/agent | 会话事件窗口（压缩对照） |
| `GET /agui/grants`、`DELETE /agui/grants/{id}` | internal/agent | 授权管理（权限码） |
| `POST /agui/ack/confirmation`、`GET /agui/ack/confirmations` | internal/agent | ack 决定与待确认查询（归属守卫内） |
| `GET /v1/agent/audit/tool-calls`、`GET /v1/agent/audit/confirmations` | internal/service | 审计查询，支持 `tool_call_id` / `confirmation_id` 点查 |
| `GET /v1/agent/retrieval-logs` | internal/service | 检索留痕查询 |
| `GET /`（及其余未命中路径） | internal/server | SPA 静态托管与 fallback（保留 API 前缀内 404） |

全部手写业务端点经 Kratos 中间件链（认证生效，无令牌 401 有测试守卫）；
`/metrics` 与静态托管链外免认证。新增手写端点按
[internal/README.md](internal/README.md)「手写端点归属」归类并同步本表。

## 框架升级检查单

trpc-agent-go / trpc_agent_go_ack / pgx 升级时逐项核对（当前基线：
trpc-agent-go v1.11.2 + server/agui v1.11.0 + session/postgres v1.11.0 +
ack e0d2276 + pgx v5.11.0；对照各上游 tag 仓库）：

1. **session schema diff 义务**：框架版本升级时以新版本 session/postgres
   建表模板 diff [agent_session.sql](internal/data/pg_sqlc/sqlc/agent_session.sql)
   （文件头注即义务声明：表结构须与框架 verifySchema 期望逐列一致，索引
   名规则由框架内部 builder 计算、改名即断）；历史库另出迁移脚本，运行期
   `WithSkipDBInit(true)` 不做 DDL。漏 diff 的症状是启动后首个会话读写报
   结构错误。
2. **ack decide 响应形状**：grape 的决定审计面（internal/agent/ack_audit.go）
   解析 200 响应体取生效路径（`{"path": ...}` 字段），ackhttp 的 decide
   响应结构变更（字段改名/形态变化）会让审计面静默放弃确认行（记日志）。
   升级 ack 后必跑 internal/agent/audit_wire_test.go 全套。
3. **上游缺陷（track follow 同毫秒事件丢失）是否已修**：前端以「快照即回
   放 + 手动刷新」兜底（web/README.md 复现视图节），修复后可评估启用 follow 流；
   核对方式：上游 track follow 游标过滤从「时间戳严格大于」改为「大于等于」
   或引入单调序号的提交。
4. **快照运行对无事件会话的行为**：无 track 事件会话的 /history 500
   （RunStartedEvent validation failed）目前由前端空态降级兜底；上游修复
   （空会话返回空快照）后前端降级分支自然不再触发，可保留作防御。
5. **pgx 升级**：ack 引入时已把 pgx 抬到 v5.11.0；后续 pgx 升级须回归
   RLS 三层判定与 IdentityTx 的 `set_config` 行为（go test 全套 +
   `GRAPE_TEST_ADMIN_DSN` 门控的存储集成测试）。
6. **框架观测面**：trpc-agent-go 日志门面与 Tracer 桥接在
   internal/telemetry（进程级全局桥接），上游观测 API 变更时只动该层。

## 工具重试次数口径（最终）

审计与复现视图的工具重试次数（`agent_tool_call_log.retry_count`）恒记 0：
工具重试在框架执行面内部收敛，终态只见最终一次调用，框架不对外暴露重试
计数（v1.11.2 无可采集回调）。前端已有消歧呈现（「0（框架未暴露）」，见
web/src/pages/replay/ReplayPage.tsx），后端不做补采——若上游将来在工具终
态回调透出重试计数，经 auditTool wrapper 单点补采即可，契约字段已就位。
