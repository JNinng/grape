# Agent 运维复现：证据面、关联机制与操作口径

Agent 运维复现的目标是仅凭落库证据还原一次交互：谁在哪个租户、哪一轮
请求、调用了什么工具、参数与结果是什么、是否经过人工确认、检索召回了
哪些文档、耗时与链路如何。本文说明证据面、关联键与写入机制、查询面与
权限边界、标准复现流程与已知边界。复现视图的页面操作细节见
[web/README.md](web/README.md)「运维复现视图」；保留期与归档执行口径见
[operations.md](operations.md)；租户与行级安全（RLS）权限模型见
[permission.md](permission.md)。

## 证据面

| 证据面 | 事实粒度 | 介质 | 写入点 |
| --- | --- | --- | --- |
| 会话事件流 | 会话内的消息与工具事件 | `agent.agent_session_*`（框架六表） | 框架 `session/postgres` |
| 工具调用审计 | 一次工具调用的终态 | `public.agent_tool_call_log` | 工具执行审计包装器（覆盖运行内执行与恢复路径代执行） |
| 授权确认审计 | 一次人工决定的终态 | `public.agent_confirmation_log` | ack（工具授权确认网关）决定路由审计面 |
| 检索留痕 | 一次检索或摄取操作 | `public.agent_retrieval_log` | `biz.VectorStore` 留痕装饰器 |
| 运行指标 | 进程内聚合 | Prometheus `/metrics` | `biz.AgentRunObserver` 推送 |
| 链路与日志 | 一次入站请求的因果链 | 日志 `trace_id`/`span_id`、`x-trace-id` 响应头、OpenTelemetry（OTel）span | 链路中间件与 telemetry 桥接 |

前四面是可还原内容与决定的证据面，后两面提供时序、因果与聚合视角；
指标与日志不能替代内容证据。

授权状态（`public.agent_tool_grant`）不是按次调用的证据面，但解释
`confirm` 层级（需人工确认）的工具「为什么没有弹出确认卡」时需要对照
（`allow` 层级直接放行，本来就不弹卡）：未撤销行按
`(tenant_id, user_id, tool_name, session_id)` 匹配许可，`session_id` 为
NULL 即全局级、非空即会话级。授权与吊销时间落 `created_at`/`revoked_at`，
吊销不删行；表保存的是许可行本身，不是按时间的完整状态快照，多次授权/
吊销后的精确时点重建需结合确认审计的 `remember` 与决定时间。

## 关联键与粒度

| 键 | 含义 | 作用域 | 关联对象 |
| --- | --- | --- | --- |
| `session_id` | 框架会话 ID（前端称 threadId） | 用户（userKey） | 事件流、工具审计、确认审计、检索留痕 |
| `trace_id` | 一次入站请求的链路 ID | 请求 | 同一轮内的工具/确认/检索行、日志、span |
| `tool_call_id` | 模型生成的工具调用 ID | 租户内唯一 | 工具审计、确认审计、事件流中的工具调用 |
| `confirmation_id` | ack 确认 ID（与 `tool_call_id` 同源） | 租户内唯一 | 确认审计、恢复路径代执行的工具行 |
| `event_id` | 会话事件 ID | 会话内 | 压缩占位符与原文事件 |
| `document_id` / `source_id` / `anchor` | 检索命中标识与原文锚点 | 向量表内 | `agent.agent_document` 原文反查 |
| userKey | `tenant_id:user_id` 编码 | 会话归属 | 框架会话表、ack 归属守卫、审计归属 |

粒度关系：

```
session_id（会话）
  └─ trace_id（一轮请求）──┬─ tool_call_id（一次工具调用）── confirmation_id
                           ├─ 工具调用审计行
                           ├─ 授权确认审计行
                           ├─ 检索留痕行 ── document_id
                           └─ 日志 / span / x-trace-id
  └─ event_id（会话事件）── 压缩占位符对照
```

`trace_id` 按入站请求生成（仅可信网段的合法 W3C traceparent 头才继承），
不按会话统一：一轮对话是一次请求，因此同一轮内的多次工具调用共享 trace，
不同轮次的 trace 不同。会话级关联用 `session_id`；查询面与复现视图都
以会话过滤、会话内按 trace 分组。

确认交互跨两个请求，工具行与确认行的 trace 关系随路径不同：

- live：工具批准后在原 chat 运行内执行，工具行带原运行 trace；确认行由
  ack 决定请求写入，带 ack 请求 trace——两者 trace 不同。
- recovery：工具由 ack 请求内的网关代执行，工具行与确认行同带 ack 请求
  trace。

因此 trace_id 不是工具与确认的关联键；两者以 `confirmation_id` ==
`tool_call_id`（同源）与 `session_id` 关联。需要把一次工具调用与其确认
归为同一组时，用 `tool_call_id`/`confirmation_id`，不要把 trace 改成
会话级或跨请求复用。

## 写入机制

### 工具调用审计

- 装配期以审计包装器包住全量工具（含 echo），在调用终态（成功、失败、
  panic）记一行；运行内执行与恢复路径网关代执行是同一挂点。
- `tool_call_id` 取框架执行 ctx；恢复路径代执行时取确认 ID（同源）。
  缺失调用 ID 无法构造合法行，记警告并跳过。`session_id` 在 live 路径取
  框架 Invocation，恢复路径由确认捕获器补入；`trace_id` 取 ctx 的
  SpanContext。
- `duration_ms` 是本次调用墙钟耗时（毫秒）。重试在框架执行面内部收敛，
  终态只见最终一次；`retry_count` 恒记 0（框架不暴露）。
- 工具 panic 先落一条 `status=error` 行再原样 re-panic；审计不改变工具
  结果与错误。
- 写入走独立短超时预算（取消不连带丢失身份与链路事实，5s），失败只记
  日志、不中断运行；审计是旁路观察面。
- 表为 append-only，`(tenant_id, tool_call_id)` 唯一。

### 授权确认审计

- 路径语义：live 是原运行仍在本进程等待、决定直接唤醒；recovery 是原运行
  已不在本进程（重启或粘性失效），由 ack 网关代执行工具或合成拒绝后续跑。
- 在 ack 决定路由的归属守卫内层拦截 `POST {prefix}/confirmation`：请求体
  得决定事实，结算前预读待确认负载（工具名、工具调用 ID），200 响应体
  得生效路径。
- 只有终态决定（approved/denied）入审计，待确认（pending）不入；拒绝决定
  `remember` 归一为空。
- live 路径只写确认行（工具行由执行终态自然落库）；recovery 路径代执行
  工具行与确认行在同一审计事务内组合写入，无代执行时只写确认行；预读
  失败但已归集代执行行时回退单边写，不丢工具行。
- 拒绝决定不执行工具，因此只有确认行、没有对应工具行；同意在 live 由
  原运行继续、在 recovery 由网关代执行，工具行都会出现。
- 无结算（参数错误、归属不匹配、冲突）不审计。`decided_by` 取 ack 网关
  身份回填值（userKey 编码）。
- 表为 append-only，`(tenant_id, confirmation_id)` 唯一。

### 检索留痕

- 装饰器位于向量仓储与框架适配器之间，同时覆盖检索工具与摄取管线：
  `Search` 记来源 `tool`；`Add`/`Update`/`DeleteByFilter` 记来源
  `pipeline`；点读、点删、枚举统计不留痕。
- `Search` 记录查询文本、`requested_top_k`/`effective_top_k`、过滤摘要、
  命中（`document_id`/`score`/`anchor`）与耗时；摄取记录作用文档（单元素
  命中，锚点取切片元数据）与条件摘要。
- 查询文本与会话 ID 由 agent 层在检索执行现场放入 ctx 事实载体，装饰器
  读取；事实缺失如实留空（摄取管线无会话事实，`session_id` 与
  `query_text` 为 NULL）。
- `requested_top_k` 是存储契约收到的请求值（框架适配器的 20 硬上限钳制
  在其上游），`effective_top_k` 是实际 LIMIT（非正时默认 10）；模型原始
  入参可对照工具审计的参数摘要。
- 命中上限 50 条，仅存标识与数值，不含文档正文；rerank 两列预留，当前
  恒 NULL。
- 无论成败都记一行（`status` 区分）；留痕写入独立于被装饰操作，失败只
  记日志，不反噬检索；表为 append-only。

### 会话事件流

- 框架六表（`agent_session_states`、`agent_session_events`、
  `agent_session_track_events`、`agent_session_summaries`、
  `agent_app_states`、`agent_user_states`）存会话元数据、事件与 track
  事件；无 RLS、无租户列，归属由 userKey 应用层编码，跨用户不可见。
- 事件流保留原文：提示词、模型输出、工具调用与结果、确认事件。请求侧
  上下文压缩只作用于请求投影副本，事件流不被改写；压缩占位符携带
  `event_id`，可经事件窗口取回原文。
- 保留期由 conf `agent.session_storage` 四项 TTL 控制，0 = 永不过期
  （默认）；track 事件 TTL 0 沿用会话 TTL。会话事件是敏感数据面，
  访问控制与保留期口径见 operations.md。

### 链路与运行指标

- 链路中间件是所有经中间件链的入站请求的 trace 唯一生成点：仅可信网段的
  合法 W3C traceparent 头被继承，其余剥头新建根；响应恒回传 `x-trace-id`，
  `x-request-id` 有则原样回传、无则以 trace 兜底。
- 日志自动携带 ctx 的 `trace_id`/`span_id`（logger 的 trace 提取器展开
  SpanContext），恒可用；telemetry 开关只决定 span 是否导出与采样，不影响
  审计与日志中的 trace 事实。采样按 trace-id 比例判定，被采样掉的链路在
  OTel 后端没有 span，但审计行仍有 trace_id。
- `/metrics` 提供四族运行指标：活跃 SSE 运行数、并发限流拒绝数、工具
  调用计数（tool/status 分列）、工具耗时直方图（tool 分列）；无用户或
  租户标签。告警建议见 operations.md。

## 脱敏、截断与保留

- 脱敏注册表 `pkg/redact` 是日志、审计表与复现视图共用的单一代码点：
  `password`/`token` 固定掩码；`tool_param` 按键名清单对 JSON 结构中的
  敏感值掩码（默认键：password、passwd、secret、token、access_token、
  refresh_token、api_key、apikey、authorization）；`retrieval` 按 512
  rune 截断。
- 工具审计的参数摘要按 `tool_param`、结果摘要按 `retrieval` 脱敏，落库
  前再做 4096 rune 兜底截断；检索留痕的查询文本与过滤摘要按 `retrieval`
  脱敏并做 4096 rune 兜底，过滤摘要截断破坏 JSON 结构时降级为截断声明
  形态。
- 脱敏是类别化的确定性改写，不是内容级全文识别：未登记键名、非 JSON
  参数与自由文本可能保留原文；会话事件流本身不做脱敏，属敏感数据面。
- 两张审计表与检索留痕表 append-only，应用无更新/删除面；保留与归档由
  运维执行（默认全量保留，建议按合规要求定清理窗口），统一口径见
  operations.md「保留期与归档统一口径」。
- 会话事件 TTL 与审计/留痕保留期相互独立：可能出现「审计行在、会话
  事件已过期」或反之；复现前先确认目标证据面是否仍在保留期内。

## 查询面与权限边界

下表路径按默认 `agent.agui.base_path=/agui`（前缀可配置）；复现页面
`/replay` 是受保护的前端路由，鉴权发生在 API 调用。

| 端点 | 能力 | 过滤与分页 |
| --- | --- | --- |
| `GET /agui/sessions` | 列出本人会话元数据 | offset/limit 默认 20、上限 100 |
| `POST /agui/history` | 会话消息快照回放（SSE） | 按 threadId；前端以快照回放，不依赖 follow |
| `GET /agui/event-window` | 锚点事件窗口（压缩占位对照） | threadId/eventId/before/after，前后各上限 50 |
| `GET /v1/agent/audit/tool-calls` | 工具调用审计列表或点查 | session_id 过滤或 tool_call_id 点查；默认 20、上限 200 |
| `GET /v1/agent/audit/confirmations` | 授权确认审计列表或点查 | session_id 过滤或 confirmation_id 点查；默认 20、上限 200 |
| `GET /v1/agent/retrieval-logs` | 检索留痕列表 | session_id/source 过滤；默认 20、上限 200 |
| `GET /metrics` | 运行指标 | 链外免认证、只读 |

- 业务与复现端点都在 Kratos 中间件链内，缺令牌 401；参数非法 400，审计
  点查未命中或跨租户 404（不区分存在性），存储故障 500 固定文案。
- 会话列表、事件窗口与历史快照按 userKey 归属过滤，跨用户不可见。
- 工具/确认审计与检索留痕的查询面边界是认证 + 租户：仓储经 IdentityTx
  注入 `app.tenant_id`，表的行级安全策略（RLS）与应用层租户谓词双重强制；
  它们不按 `user_id` 过滤。前端复现视图只提供本人会话入口，但同租户内已
  认证用户若持有会话 ID，可经 API 读取该会话的证据行。需要用户级最小
  权限时，应在网关或部署层收口，或扩展端点的权限码与用户过滤，不要把
  当前边界当作按用户隔离。
- 响应是落库时已脱敏的形态，端点不二次脱敏、不回传原文；原文只在会话
  事件面（历史快照与事件窗口）可见。

## 标准复现流程

1. 收集线索：租户与用户、会话或 threadId、时间窗，以及
   `tool_call_id`/`confirmation_id`/`trace_id`（客户端可见 `x-trace-id`）。
2. 定位会话：用 `/agui/sessions` 或线索中的 session_id；打开 `/replay`
   选择会话。
3. 选交互：在会话内按 trace 下拉选择一轮；证据簇按出现频次排序，选中后
   过滤工具、确认、检索三面，并按 `toolCallId` 高亮对话流中的工具项。
   live 路径的确认行与工具行 trace 不同，看到确认行但工具面为空时按
   `confirmation_id`/`tool_call_id` 对账，不要按 trace 判缺失。
4. 读事实：工具行看状态、耗时、参数与结果摘要；确认行看决定、记住作用
   域与 live/recovery 路径；检索行看查询、top_k、过滤、命中与耗时。
5. 反查原文：压缩占位符经 `event-window` 按 `event_id` 取原文；检索命中
   经 `document_id` 反查 `agent.agent_document`；工具参数与结果原文以
   会话事件为准。
6. 关联链路：用 `trace_id` 检索日志；telemetry 启用且该链路被采样时在
   OTel 后端看 span；用 `/metrics` 判断是否为全局或下游问题而非单次异常。
7. 判定与补证：证据缺失时先查保留期、写入失败日志与链路采样，不把
   「查不到」直接归因为「没发生」。

常见现象与首查证据：

| 现象 | 首查证据 |
| --- | --- |
| 工具未执行或报错 | 工具审计的 status/result；服务端工具错误日志 |
| 参数或结果与预期不符 | 工具审计参数/结果摘要；会话事件原文（含压缩对照） |
| 回答引用不实或召回缺失 | 检索留痕的 query/top_k/filter/hits；document 反查 |
| 确认未生效或疑似重复执行 | 确认审计的 decision/remember/path；对应工具行与 ack 路径；无确认卡时对照 `agent_tool_grant` 许可 |
| 响应慢 | 工具耗时、检索耗时、OTel span；`/metrics` 聚合 |
| 审计行缺失 | 写入失败日志、调用 ID 缺失警告、保留期与采集开关 |

## 已知边界与降级

- `retry_count` 恒 0：框架不暴露重试计数，前端以「0（框架未暴露）」呈现，
  后端不补采。
- trace 是请求级：不同轮次不同 trace；ack 决定与恢复代执行带 ack 请求的
  trace。live 路径的确认行与工具行 trace 不同（分别属于 ack 请求与原运行
  请求），复现视图按 trace 分组时确认组不含工具行，需按
  `tool_call_id`/`confirmation_id` 对账。会话级关联始终用 `session_id`。
- 历史快照可能不可用：无 track 事件或事件已过保留期的会话，快照运行
  返回 500，复现视图降级为空态提示，工具、确认、检索三面照常装载。快照
  以手动刷新兜底上游 track follow 的同毫秒事件缺陷，不依赖 follow 流。
- 压缩占位符只有携带 `event_id` 且事件窗口可读时才能对照原文；否则只能
  标注「已压缩（不可反查）」。
- 审计与留痕写入是旁路：失败只记日志，行可能缺失；工具调用 ID 缺失时
  跳过工具行。排查缺失先看服务端日志。
- 待确认负载（Pending）预读失败或未命中、且没有已归集的代执行行时，
  确认行放弃并记日志；已归集的工具行仍回退单边写入，不丢。ack 200 响应
  体不可解析时整个决定审计面放弃（recovery 已归集的工具行同样不落库），
  记日志；升级 ack 后需按 operations.md 的升级检查单核对响应形状。
- 审计与留痕查询按租户而非用户隔离，见「查询面与权限边界」。
- 多实例部署下确认决定与运行须同进程：粘性失效时 live 决定退化为
  recovery（代执行或合成拒绝续跑），确认审计的 `path` 记录 recovery；
  这是既定降级语义，不是重复执行。
- 检索留痕的 pipeline 行没有会话与查询事实（分别为 NULL）；摄取证据用
  命中里的 `document_id` 反查。
- 保留期两侧独立：审计或留痕可长于会话事件；会话事件过期后仍可能有
  审计行但无法回放原文，反之亦然。

## 只读直查（可选）

API 是首选；需要跨会话或按 trace 批量核对时，运维可用只读 SQL 直查。
表结构以 [schema/95_agent_audit.sql](internal/data/pg_sqlc/sqlc/schema/95_agent_audit.sql)
与 [schema/97_agent_retrieval_log.sql](internal/data/pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql)
为准。以应用角色 `grape_u` 连接时 RLS 生效，须在事务内先设置租户；超级
用户直查会绕过 RLS，需自律带 `tenant_id` 谓词。

```sql
BEGIN;
SELECT set_config('app.tenant_id', :tenant_id, true);

-- 会话内工具调用（走 (tenant_id, session_id, called_at) 索引）
SELECT called_at, trace_id, tool_call_id, tool_name, status,
       duration_ms, arguments_summary, result_summary
FROM public.agent_tool_call_log
WHERE tenant_id = :tenant_id AND session_id = :session_id
ORDER BY called_at DESC, id DESC
LIMIT 100;

-- 同一 trace 的检索证据（hits 为 jsonb 数组）
SELECT created_at, source, status, query_text, requested_top_k,
       effective_top_k, filter_summary, hits, duration_ms
FROM public.agent_retrieval_log
WHERE tenant_id = :tenant_id AND trace_id = :trace_id
ORDER BY created_at DESC, id DESC;
COMMIT;
```

`:tenant_id` 等是 psql 变量（`-v tenant_id=…`）；以其它客户端执行时按
参数绑定替换，不要拼接未校验的外部输入。

## 相关文档

- [operations.md](operations.md)：保留期与归档、指标与告警、多实例粘性、
  SSE 与优雅停机。
- [permission.md](permission.md)：租户边界、RLS 与数据范围、错误语义。
- [internal/agent/README.md](internal/agent/README.md)：审计挂点、ack
  决定审计、事件窗口与压缩对照的实现细节。
- [internal/biz/README.md](internal/biz/README.md)：审计与检索留痕用例、
  脱敏注册表。
- [internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)：
  审计与留痕表的 SQL 对象索引。
- [internal/ops/README.md](internal/ops/README.md)：`/metrics` 指标出口。
- [internal/telemetry/README.md](internal/telemetry/README.md)：trace 生成、
  日志桥接与 span 导出。
- [web/README.md](web/README.md)：复现视图的页面行为与降级分支。
