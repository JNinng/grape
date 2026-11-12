# Agent 运维复现：证据面、关联机制与操作口径

仅凭落库证据还原一次交互：谁在哪个租户、哪一轮请求、调用了什么工具、
参数与结果、是否经过人工确认、检索召回了哪些文档、耗时与链路如何。
本文是证据面、关联键与写入面语义的权威说明；页面行为见
[web/README.md](web/README.md)「运维复现视图」，保留期与运维执行口径见
[operations.md](operations.md)，租户与行级安全见
[permission.md](permission.md)。

## 证据面

| 证据面 | 事实粒度 | 介质 | 写入点 |
| --- | --- | --- | --- |
| 会话事件流 | 会话内消息与工具事件；每次模型调用的模型型号与 token 用量（首字/思考为本轮口径） | 框架六表 `agent.agent_session_*` | 框架 `session/postgres` |
| 工具调用审计 | 一次工具调用的终态 | `public.agent_tool_call_log` | 工具执行审计包装器（含恢复路径代执行） |
| 授权确认审计 | 一次授权终态（人工或系统决定，互斥） | `public.agent_confirmation_log` | ack 决定路由审计面 + 授权终态观察挂点 |
| 检索留痕 | 一次检索或摄取操作（编排检索行带阶段时间线） | `public.agent_retrieval_log` | `biz.VectorStore` 留痕装饰器与知识检索编排层 |
| 模型调用留痕 | 管线侧一次模型/类模型调用（文本生成、向量化、检索重排、外部解析） | `public.agent_model_call_log` | `biz.ModelCallObserver` 观察面（四个能力目录） |
| 运行指标 | 进程内聚合 | Prometheus `/metrics` | `biz.AgentRunObserver` 推送 |
| 链路与日志 | 一次入站请求的因果链 | 日志 `trace_id`/`span_id`、`x-trace-id`、OTel span | 链路中间件与 telemetry 桥接 |

前五面是可还原内容与决定的证据面；指标与日志提供时序、因果与聚合视角，
不能替代内容证据。agent 对话 LLM 的模型调用事实由会话事件流与
`/agui/session-usage` 承载，不写模型调用留痕表（避免双写）。

授权状态（`public.agent_tool_grant`）不是按次调用的证据面，但解释
`confirm` 层级「为什么没有弹出确认卡」时需要对照（`allow` 直接放行）：
未撤销行按 `(tenant_id, user_id, tool_name, session_id)` 匹配，`session_id`
为 NULL 即全局级、非空即会话级；吊销置 `revoked_at` 不删行。表保存许可行
本身，不是完整状态快照，精确时点重建需结合确认审计的 `remember` 与决定
时间。

## 关联键与粒度

| 键 | 含义 | 作用域 | 关联对象 |
| --- | --- | --- | --- |
| `session_id` | 框架会话 ID（前端称 threadId） | 用户（userKey） | 事件流、工具/确认审计、检索留痕 |
| `trace_id` | 一次入站请求的链路 ID | 请求 | 同一轮内的工具/确认/检索/模型调用行、日志、span |
| `tool_call_id` | 模型生成的工具调用 ID | 租户内唯一 | 工具审计、确认审计、事件流工具调用 |
| `confirmation_id` | ack 确认 ID（与 `tool_call_id` 同源） | 租户内唯一 | 确认审计、恢复路径代执行的工具行 |
| `event_id` | 会话事件 ID | 会话内 | 压缩占位符与原文事件 |
| `document_id` / `source_id` / `anchor` | 检索命中标识与原文锚点 | 向量表内 | `agent.agent_document` 原文反查 |
| 留痕行 `document_id` | 模型调用作用的知识条目 ID | `kb_document.id` | 摄取向量化/外部解析行 → 条目反查 |
| userKey | `tenant_id:user_id` 编码 | 会话归属 | 框架会话表、ack 归属守卫、审计归属 |

```
session_id（会话）
  └─ trace_id（一轮请求）──┬─ tool_call_id（一次工具调用）── confirmation_id
                           ├─ 工具调用审计行
                           ├─ 授权确认审计行
                           ├─ 检索留痕行 ── document_id（向量切片反查）
                           ├─ 模型调用留痕行 ── document_id（kb 条目反查）
                           └─ 日志 / span / x-trace-id
  └─ event_id（会话事件）── 压缩占位符对照
```

`trace_id` 按入站请求生成（仅可信网段的合法 W3C traceparent 头才继承），
不按会话统一：一轮对话是一次请求，同一轮内多次工具调用共享 trace，不同
轮次不同。会话级关联用 `session_id`；查询面与复现视图都以会话过滤、会话
内按 trace 分组。

确认交互跨两个请求，工具行与确认行的 trace 关系随路径不同：

- live：工具批准后在原 chat 运行内执行，工具行带原运行 trace；确认行由
  ack 决定请求写入，带 ack 请求 trace——两者 trace 不同。
- recovery：工具由 ack 请求内的网关代执行，工具行与确认行同带 ack 请求
  trace。

因此 `trace_id` 不是工具与确认的关联键；两者以 `confirmation_id` ==
`tool_call_id`（同源）与 `session_id` 关联。把一次工具调用与其确认归为
一组时用 `tool_call_id`/`confirmation_id`，不要把 trace 改成会话级或跨
请求复用。

## 写入机制

### 工具调用审计

- 装配期以审计包装器包住全量工具（含 echo），在调用终态（成功、失败、
  panic）记一行；运行内执行与恢复路径网关代执行是同一挂点。
- `tool_call_id` 取框架执行 ctx；恢复路径代执行时取确认 ID（同源）。缺失
  调用 ID 无法构造合法行，记警告并跳过。`session_id` 在 live 路径取框架
  Invocation，恢复路径由确认捕获器补入；`trace_id` 取 ctx 的 SpanContext。
- `duration_ms` 是本次调用墙钟耗时。重试在框架执行面内部收敛，终态只见
  最终一次；`retry_count` 恒记 0（框架不暴露）。
- 工具 panic 先落一条 `status=error` 行再原样 re-panic；审计不改变工具
  结果与错误。
- 写入走独立短超时预算（5s），失败只记日志、不中断运行；审计是旁路观察面。
- 表为 append-only，`(tenant_id, tool_call_id)` 唯一。

### 授权确认审计

决定词表：人工封闭值 `approved` / `denied`；系统决定两形态——无来源
`auto`（自动放行）/ `auto_deny`（自动拒绝），有来源为来源命名空间
`{name}_auto` / `{name}_deny`（如 `risk_deny`）。来源名小写字母开头、仅
`[a-z0-9_]`、≠ `auto`，decision 总长 ≤16（varchar(16) 无 CHECK，由 biz
模式校验把门）。待确认（pending）不入审计。

- 每次工具调用恰好一条授权行，两类写入点互斥：人工决定（ack `Decide`
  结算，live 与 recovery 路径）由决定路由审计面落行；评估管线内自动终结的
  调用（层级放行/拒绝、策略判定、许可命中、重复确认 ID 防御放行、取消/
  持久化失败结算）由授权终态观察挂点（ack `PermissionObserver`）转写落行
  ——许可命中不与人工决定双写。
- 路径语义与决定值一一配对：live 是原运行仍在本进程等待、决定直接唤醒；
  recovery 是原运行已不在本进程（重启或粘性失效），由 ack 网关代执行工具
  或合成拒绝后续跑；两者为人工决定专用。auto 是系统自动决定专用路径，
  交叉配对非法。
- 人工决定写入点：在 ack 决定路由的归属守卫内层拦截
  `POST {prefix}/confirmation`，请求体得决定事实，结算前预读待确认负载
  （工具名、工具调用 ID），200 响应体得生效路径。live 路径只写确认行
  （工具行由执行终态自然落库）；recovery 路径代执行工具行与确认行在同一
  审计事务内组合写入，无代执行时只写确认行；预读失败但已归集代执行行时
  回退单边写，不丢工具行。拒绝决定不执行工具，因此只有确认行、没有对应
  工具行。无结算（参数错误、归属不匹配、冲突）不审计；`remember` 仅人工
  批准可携带，拒绝归一为空。
- 系统自动决定写入点：`confirmation_id` 即 `tool_call_id`（自动终态不发
  确认 ID，以工具调用 ID 为对账锚点）、`path` 恒 auto、`remember` 恒空、
  `decided_by` 取评估 ctx 的运行属主身份（userKey 编码）；来源名不合规时
  该行 fail-closed 丢弃。
- 两路写入共用旁路纪律：独立短超时预算（5s），取不到事实（负载/身份/调用
  ID）只记日志放弃该行，失败不反噬决定与授权主流程。
- 表为 append-only，`(tenant_id, confirmation_id)` 唯一。自动行以工具调用
  ID 充当 confirmation_id，行唯一性以调用 ID 租户内唯一为前提；模型复用
  调用 ID 的重复授权行由唯一索引收编、写入失败按旁路纪律放弃，不伪造第二
  次授权行。

### 时间与用量事实

内容证据面的时间与用量事实各自独立，不可互相替代：

- 工具：审计行 `duration_ms` 是本次调用墙钟耗时，`called_at` 是调用终态
  落库时间（不是起始时间）。
- 检索：留痕行 `duration_ms` 与 `created_at`，前者是检索耗时，后者是
  终态时间。
- 对话流：消息与工具调用的**起始时间**来自快照 `rawEvent` 的来源索引
  （框架源元数据，毫秒时间戳，按 messageId / toolCallId 索引）——这是
  回答「文字与工具何时开始」的唯一证据，只到消息级与工具调用级，流式
  分块与思考过程不逐块记时。回复的生成耗时由下一条事件或该轮终态的
  `called_at` / 运行终态时间推算，属推理口径，不作落库事实。
- 以上事实相互对照即可拆出「用户在等什么」：用户消息起点 → 工具起点 →
  工具耗时（`duration_ms`）→ 回复起点。来源索引缺失（后端未开启源元数据
  或会话早于该口径）时起始时间为空，不补采、不伪造。
- 模型调用事实（模型型号、`prompt`/`completion`/`total` token、缓存与思考
  细分 token）取自会话事件 `response` 部分的 `model` 与 `usage`，按响应 ID
  （即 AG-UI 消息 ID）对账；一次调用一条，不重复落库。工具决策轮通常没有
  正文，其事实经「发起该调用的助手消息 ID」挂到工具卡片上。provider 未
  上报的字段留空，不按 0 呈现；缓存率是展示侧派生值（缓存输入 token ÷
  输入 token，两位小数），不落库、不参与对账。
- 首字时长与思考时长是**流程级**事实：框架只在一次运行的首个模型调用上
  测量，随后把同一个值复制进该运行后续调用的 `usage.timing_info`。因此
  同一 `invocationId` 的各条事实取值相同，复现视图只在每次运行的首条
  调用上标注「本轮首字」「本轮思考」，逐条呈现会把同一个数值误读成多次
  测量的巧合。两者都不按调用次数累加。

### 检索留痕

- 写入方有两处，共同保住「一次操作一行」：混合检索编排层（知识检索用例，
  console / tool 两条来源同覆盖）在完成处自写一行完整行——编排路径经 ctx
  标记让装饰器跳过逐路留痕；留痕装饰器承接其余检索（框架侧单路直调）与
  摄取管线：`Add`/`Update`/`DeleteByFilter` 记来源 `pipeline`；点读、点删、
  枚举统计不留痕。来源都取 ctx 声明——知识检索用例缺省声明 `console`，
  agent 检索工具链路在调用前显式声明 `tool`，装饰器未声明时回落 `tool`。
- 编排行（混合检索）的 `stages` 列承载阶段时间线：数组顺序即执行序（召回
  → 融合 → 重排 → 回包），后一阶段的输入序即前一阶段的输出序。元素为
  `{"stage","query_text","limit","model","candidates","units","error",
  "duration_ms"}`——召回阶段（vector / lexical）记该路变体查询文本与候选
  （上限 50，形态同 `hits` 元素），fusion 记 RRF 融合序，rerank 记重排模
  型键与重排后序（对照 fusion 候选即还原重排前后序），pack 记回包单元
  （mode / 聚合块 ID / heading_path / anchor / token 量级 / admitted，对照
  fusion 序与 units 可看出预算截断掉了谁）。分数按阶段自解释（向量相似度
  / 词法分 / RRF 分 / rerank 模型分），同阶段可比、跨阶段不可比。阶段失败
  同样留痕（候选空 + 错误摘要；词法路与重排失败按质量增强降级，行 status
  仍记操作终态）；单路直调行 stages 为 NULL（不伪造空阶段）。
- 查询文本、`requested_top_k`/`effective_top_k`、过滤摘要、最终命中
  （`document_id`/`score`/`anchor`）与耗时两处同口径：编排行取归一化后的
  top_k（最终命中上限，逐路召回宽度在 stages 各召回阶段的 `limit`）、过滤
  摘要记 `kb_ids` 可见库集合；装饰器行取存储契约收到的 Limit、过滤摘要记
  `ids/metadata/min_score`。摄取行记作用文档（单元素命中，锚点取切片元
  数据）与条件摘要。
- 查询文本与会话 ID 由检索执行现场放入 ctx 事实载体（工具链路在 agent
  层、前端检索在知识检索用例）；事实缺失如实留空（前端检索与摄取管线无
  会话事实，`session_id` 为 NULL；摄取管线 `query_text` 亦为 NULL）。
  `effective_top_k` 非正请求时取存储层默认 10。
- 命中与阶段候选上限 50 条，仅存标识与数值，不含正文；查询文本（主查询
  与变体）与过滤摘要在写入方落库前经 retrieval 类别脱敏（脱敏义务随写入
  方迁移：编排行在编排层脱敏，装饰器行在装饰器脱敏）。
- 无论成败都记一行（`status` 区分）；留痕写入独立于业务操作，失败只记日
  志；表为 append-only。装饰器实现与阈值见
  [internal/data/README.md](internal/data/README.md#检索留痕装饰器)。

### 模型调用留痕

- 观察面（`biz.ModelCallObserver`，由 `ModelCallLogUsecase` 实现）覆盖管线
  侧模型/类模型调用面：文本生成（openaicompleter，场景 `ingest_summary`
  文档摘要 / `ingest_prefix` 块前缀）、向量化（openaiembedder，场景
  `embed_document` 摄取 / `embed_query` 检索）、检索重排（openaireranker，
  场景 `rerank`）、外部文档解析（docparse，场景 `parse_document`，类模型
  OCR）。agent 对话 LLM 不在本面：会话事件流与 `/agui/session-usage`
  已承载（见「时间与用量事实」），避免双写。
- 一次调用一行，留痕点在「解析出端点事实之后、调用终态（含 error）」：
  模型/空间解析失败（无端点事实）与外部解析端点未配置（未发生调用）不留
  行；解析成功后的任何失败（协议错误、非 2xx、维度漂移）记
  `status=error` + 错误摘要（截断 512）。空产出记 output 为空。
- `model_key`/`endpoint` 按解析现场原样留痕，不做注册表对账——失败尝试的
  原始声明正是排查换模型/换端点事故的证据；`usage` token 取端点响应体
  （未上报为 NULL）；`document_id` 是作用知识条目 ID（摄取与解析链路经
  ctx 载体注入），检索查询向量化无文档事实恒 NULL。
- 输入/输出摘要经 `pkg/redact` 的 `model_call` 类别脱敏（4096 rune 截断，
  对齐审计摘要口径）；docparse 的原件字节不落留痕（input 记
  mime/filename/size/sha256 事实摘要，output 记 markdown 产物截断），
  embedder 的向量产物不落留痕（output 记维度说明）。
- 场景由调用链路经 ctx 载体声明（`biz.WithModelCallScene`），缺失时按实现
  声明的缺省回落（embed→`embed_document`、completer→`ingest_prefix`、
  reranker→`rerank`；docparse 无回落，缺失放弃该行），不伪造其他值。词表
  由应用层把门（表无 CHECK），词表扩展随调用面演进同步 biz 常量与列注释。
- 写入与检索留痕同一旁路纪律：身份缺失放弃该行（fail-closed 不伪造）、
  独立短超时预算（5s）、失败只记日志不反噬业务主流程；表为 append-only。
  实现与阈值见 [internal/data/README.md](internal/data/README.md#审计与留痕)。

### 会话事件流

- 框架六表（`agent_session_states`、`agent_session_events`、
  `agent_session_track_events`、`agent_session_summaries`、
  `agent_app_states`、`agent_user_states`）存会话元数据、事件与 track
  事件；无 RLS、无租户列，归属由 userKey 应用层编码，跨用户不可见。
- 事件流保留原文：提示词、模型输出、工具调用与结果、确认事件。请求侧
  上下文压缩只作用于请求投影副本，事件流不被改写；压缩占位符携带
  `event_id`，可经事件窗口取回原文。
- 保留期由 conf `agent.session_storage` 四项 TTL 控制，0 = 永不过期
  （默认）；track 事件 TTL 0 沿用会话 TTL。会话事件是敏感数据面，访问
  控制与保留期口径见 [operations.md](operations.md)。

### 链路与运行指标

- trace 是请求级事实；生成规则（可信网段继承、剥头、`x-trace-id` 回传）
  见 [internal/middleware/README.md](internal/middleware/README.md)，日志
  携带与 span 导出见
  [internal/telemetry/README.md](internal/telemetry/README.md)。
- 日志自动携带 ctx 的 `trace_id`/`span_id`；telemetry 开关只决定 span 是否
  导出与采样，不影响审计与日志中的 trace 事实。被采样掉的链路在 OTel 后端
  没有 span，但审计行仍有 `trace_id`。
- `/metrics` 四族指标（活跃 SSE 运行数、限流拒绝数、工具调用计数、工具耗时
  直方图）无用户/租户标签，口径见 [internal/ops/README.md](internal/ops/README.md)。

## 脱敏、截断与保留

- 脱敏注册表 `pkg/redact` 是日志、审计与复现视图共用的单一代码点：
  `password`/`token` 固定掩码，`tool_param` 按键名清单对 JSON 敏感值掩码，
  `retrieval` 按 512 rune 截断，`model_call` 按 4096 rune 截断。
- 工具审计的参数/结果摘要分别按 `tool_param` / `retrieval` 脱敏，落库前
  再做 4096 rune 兜底；检索留痕的查询文本（主查询与各召回阶段的变体文本）
  与过滤摘要同样处理，过滤摘要截断破坏 JSON 时降级为截断声明；模型调用
  留痕的输入/输出摘要按 `model_call` 类别脱敏（4096 rune）+ 兜底截断，
  错误摘要另截 512。
- 脱敏是类别化的确定性改写，不是内容级全文识别：未登记键名、非 JSON 参数
  与自由文本可能保留原文；会话事件流本身不做脱敏，属敏感数据面。
- 两张审计表、检索留痕表与模型调用留痕表 append-only，应用无更新/删除
  面；保留与归档由运维执行（默认全量保留），统一口径见 operations.md
  「保留期与归档统一口径」。
- 会话事件 TTL 与审计/留痕保留期相互独立：可能「审计行在、会话事件已
  过期」或反之；复现前先确认目标证据面是否仍在保留期内。

## 查询面与权限边界

下表路径按默认 `agent.agui.base_path=/agui`（前缀可配置）；复现页面
`/replay` 是受保护的前端路由，鉴权发生在 API 调用。

| 端点 | 能力 | 过滤与分页 |
| --- | --- | --- |
| `GET /agui/sessions` | 列出本人会话元数据 | offset/limit 默认 20、上限 100 |
| `POST /agui/history` | 会话消息快照回放（SSE） | 按 threadId；前端以快照回放，不依赖 follow |
| `GET /agui/event-window` | 锚点事件窗口 | threadId/eventId/before/after，前后各上限 50 |
| `GET /agui/session-usage` | 每次模型调用的模型型号与 token；首字/思考为本轮口径 | 按 threadId；按响应 ID 去重，随会话事件保留期 |
| `GET /v1/agent/audit/tool-calls` | 工具调用审计列表或点查 | session_id 或 tool_call_id；默认 20、上限 200 |
| `GET /v1/agent/audit/confirmations` | 授权确认审计列表或点查 | session_id 或 confirmation_id；默认 20、上限 200 |
| `GET /v1/agent/retrieval-logs` | 检索留痕列表 | session_id/source；默认 20、上限 200 |
| `GET /v1/agent/model-calls` | 模型调用留痕列表（复现页「模型调用」面板） | scenario/model_key/status/document_id/created_from/created_to；默认 20、上限 200 |
| `GET /metrics` | 运行指标 | 链外免认证、只读 |

- 业务与复现端点都在 Kratos 中间件链内，缺令牌 401；参数非法 400，审计
  点查未命中或跨租户 404（不区分存在性），存储故障 500 固定文案。
- 会话列表、事件窗口与历史快照按 userKey 归属过滤，跨用户不可见。
- 工具/确认审计、检索留痕与模型调用留痕的查询面边界是认证 + 租户：仓储
  经 IdentityTx 注入 `app.tenant_id`，RLS 与应用层租户谓词双重强制；它们
  **按租户不按用户**过滤。前端只提供本人会话入口，但同租户内已认证用户若
  持有会话 ID，可经 API 读取该会话的证据行（模型调用留痕无会话过滤项，
  同租户全量可见）。需要用户级最小权限时，应在网关或部署层收口，或扩展
  端点权限码与用户过滤，不要把当前边界当作按用户隔离。
- 响应是落库时已脱敏的形态，端点不二次脱敏、不回传原文；原文只在会话
  事件面（历史快照与事件窗口）可见。

## 标准复现流程

1. 收集线索：租户与用户、会话或 threadId、时间窗，以及
   `tool_call_id`/`confirmation_id`/`trace_id`（客户端可见 `x-trace-id`）。
2. 定位会话：用 `/agui/sessions` 或线索中的 session_id；打开 `/replay`
   选择会话。
3. 选交互：会话内按 trace 选择一轮；证据簇按出现频次排序，选中后过滤工具、
   确认、检索三面并高亮留存行；对话流按 `toolCallId` 桥接（审计与确认两面
   取并集）定位命中的工具项，命中项所在「用户消息为界」的整段交互保持
   高亮。live 路径的确认行与工具行 trace 不同，看到确认行但工具面为空时
   按 `confirmation_id`/`tool_call_id` 对账。
4. 读事实：工具行看状态、耗时、参数与结果摘要；确认行看决定、记住作用域
   与 live/recovery 路径；检索行看查询、top_k、过滤、命中、阶段时间线
   （召回 → 融合 → 重排 → 回包，含阶段失败摘要与被预算截断的回包单元）
   与耗时；回放项的模型调用事实看模型型号、token 用量、思考与首字时长。
5. 反查原文：压缩占位符经 `event-window` 按 `event_id` 取原文；检索命中经
   `document_id` 反查 `agent.agent_document`；工具参数与结果原文以会话事件
   为准。
6. 关联链路：用 `trace_id` 检索日志；telemetry 启用且该链路被采样时在
   OTel 后端看 span；用 `/metrics` 判断是否为全局或下游问题。
7. 判定与补证：证据缺失时先查保留期、写入失败日志与链路采样，不把
   「查不到」直接归因为「没发生」。

| 现象 | 首查证据 |
| --- | --- |
| 工具未执行或报错 | 工具审计的 status/result；服务端工具错误日志 |
| 参数或结果与预期不符 | 工具审计参数/结果摘要；会话事件原文（含压缩对照） |
| 回答引用不实或召回缺失 | 检索留痕的 query/top_k/filter/hits 与阶段时间线（哪一路召回空了、重排是否降级、回包截断了谁）；document 反查 |
| 确认未生效或疑似重复执行 | 确认审计的 decision/remember/path；对应工具行与 ack 路径；无确认卡时对照 `agent_tool_grant` 许可 |
| 响应慢 | 回放流的起始时间与段内用时、模型调用事实（模型型号/token/思考与首字时长）、工具/检索耗时、OTel span；`/metrics` 聚合 |
| 审计行缺失 | 写入失败日志、调用 ID 缺失警告、保留期与采集开关 |

## 已知边界与降级

- `retry_count` 恒 0：框架不暴露重试计数，前端不呈现，后端不补采。
- trace 是请求级：不同轮次不同 trace；ack 决定与恢复代执行带 ack 请求的
  trace。live 路径的确认行与工具行 trace 不同，复现视图按 trace 分组时
  确认组不含工具行，需按 `tool_call_id`/`confirmation_id` 对账。会话级
  关联始终用 `session_id`。
- 历史快照可能不可用：无 track 事件或事件已过保留期的会话，快照运行返回
  500，复现视图降级为空态提示，工具、确认、检索三面照常装载。快照以手动
  刷新兜底上游 track follow 的同毫秒事件缺陷，不依赖 follow 流。
- 压缩占位符只有携带 `event_id` 且事件窗口可读时才能对照原文；否则只标注
  「已压缩（不可反查）」。
- 审计与留痕写入是旁路：失败只记日志，行可能缺失；工具调用 ID 缺失时
  跳过工具行。排查缺失先看服务端日志。
- 待确认负载预读失败或未命中、且没有已归集的代执行行时，确认行放弃并记
  日志；已归集的工具行仍回退单边写入。ack 200 响应体不可解析时整个决定
  审计面放弃（recovery 已归集的工具行同样不落库），记日志；升级 ack 后需
  按 operations.md 的升级检查单核对响应形状。
- 审计与留痕查询按租户而非用户隔离，见「查询面与权限边界」。
- 多实例部署下确认决定与运行须同进程：粘性失效时 live 决定退化为 recovery
  （代执行或合成拒绝续跑），确认审计的 `path` 记录 recovery；这是既定降级
  语义，不是重复执行。
- 检索留痕的 pipeline 行没有会话与查询事实（分别为 NULL）；摄取证据用命中
  里的 `document_id` 反查。
- 模型调用留痕行没有会话事实（`session_id` 恒 NULL，管线调用无会话）；
  模型/空间解析失败与解析端点未配置的「调用」不留行（无端点事实）。
  摄取与解析证据用 `document_id` 反查 `kb_document`。
- 保留期两侧独立：审计或留痕可长于会话事件；会话事件过期后仍可能有审计行
  但无法回放原文，反之亦然。

## 只读直查（可选）

API 是首选；需要跨会话或按 trace 批量核对时，运维可用只读 SQL 直查。
表结构以
[schema/95_agent_audit.sql](internal/data/pg_sqlc/sqlc/schema/95_agent_audit.sql)、
[schema/97_agent_retrieval_log.sql](internal/data/pg_sqlc/sqlc/schema/97_agent_retrieval_log.sql)
与
[schema/99_model_call_log.sql](internal/data/pg_sqlc/sqlc/schema/99_model_call_log.sql)
为准。以 `grape_u` 连接时 RLS 生效，须在事务内先设租户；超级用户直查会
绕过 RLS，需自律带 `tenant_id` 谓词。

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

-- 同一 trace 的检索证据（hits / stages 为 jsonb；stages 是编排层的
-- 阶段时间线，单路直调行与摄取行为 NULL）
SELECT created_at, source, status, query_text, requested_top_k,
       effective_top_k, filter_summary, hits, stages, duration_ms
FROM public.agent_retrieval_log
WHERE tenant_id = :tenant_id AND trace_id = :trace_id
ORDER BY created_at DESC, id DESC;

-- 同一 trace 的模型调用证据（摄取/解析链路的外部调用事实）
SELECT created_at, scenario, status, model_key, endpoint, document_id,
       input_summary, output_summary, input_tokens, output_tokens,
       duration_ms, error
FROM public.agent_model_call_log
WHERE tenant_id = :tenant_id AND trace_id = :trace_id
ORDER BY created_at DESC, id DESC;
COMMIT;
```

`:tenant_id` 等是 psql 变量（`-v tenant_id=…`）；其它客户端按参数绑定
替换，不要拼接未校验的外部输入。

## 相关文档

- [operations.md](operations.md)：保留期与归档、指标与告警、多实例粘性、
  SSE 与优雅停机。
- [permission.md](permission.md)：租户边界、RLS 与数据范围、错误语义。
- [internal/agent/README.md](internal/agent/README.md)：审计挂点、ack 决定
  审计、事件窗口与压缩对照的适配层实现。
- [internal/biz/README.md](internal/biz/README.md)：审计与检索留痕用例、
  脱敏注册表。
- [internal/data/README.md](internal/data/README.md)：留痕装饰器与向量介质。
- [internal/data/pg_sqlc/sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)：
  审计与留痕表的 SQL 对象索引。
- [internal/ops/README.md](internal/ops/README.md)：`/metrics` 指标出口。
- [internal/telemetry/README.md](internal/telemetry/README.md)：trace 生成、
  日志桥接与 span 导出。
- [web/README.md](web/README.md)：复现视图页面行为与降级分支。
