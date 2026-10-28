# telemetry

观测装配层：OTel TracerProvider 的生命周期（开关、OTLP 导出、采样、
Shutdown）与 trpc-agent-go 框架观测面桥接（日志门面 + Tracer 全局变量）
只在这里落子：OTel SDK 接入与框架日志桥接 trace_id。

## 装配与开关语义

`NewSetup(conf.Telemetry, logger)` 产出 `*Setup` 依赖锚点 + wire cleanup：

- **enabled=false（默认，完全 noop）**：不构造 exporter、不发起任何连接、
  不改写 otel 全局与框架 Tracer 变量；全链路 span 保持 no-op。
- **enabled=true**：构造 OTLP gRPC exporter → SDK TracerProvider（资源属性
  service.name/service.version 取 conf，采样按 trace-id 确定性比例）→
  `otel.SetTracerProvider` 注入全局 → 框架 Tracer 桥接。进程退出经
  cleanup `Shutdown`（5s 上限）冲刷缓冲。

采样（`sampler_ratio`）只影响 span 是否导出；采样器为裸
TraceIDRatioBased（非 ParentBased），决策独立于父 span 的 sampled
标志；`x-trace-id` 响应头与日志
`trace_id` 字段来自 ctx 中的 SpanContext（[internal/middleware](../middleware/)
链路中间件注入），与开关和采样无关、恒可用。`sampler_ratio=0` 视为未
设置（全采），零导出需求直接 `enabled=false`。

## 框架桥接（bridge.go）

| 桥接面 | 形态 | trace_id |
| ------ | ---- | -------- |
| `trpc-agent-go/log` 无 ctx 调用（`log.Infof` 等 → `Default`/`ContextDefault`） | 适配器实现框架 `log.Logger` 接口，转发 grape logger | **不带**——门面 API 不收 ctx，框架既成事实，不伪造 |
| `trpc-agent-go/log` ctx 形态（`log.InfofContext` 等，package 级函数变量） | 覆盖变量，转发 ctx 到 grape logger | 自动携带——grape logger 由 kratos `log.NewLogger(WithExtractor(tracing.TraceAttrs))` 构造，落盘时展开 ctx 中的 SpanContext |
| `trpc-agent-go/telemetry/trace.Tracer` 全局变量 | 桥到 `otel.GetTracerProvider()`（scope 名保持 `trpc.agent.go`） | 框架 LLM/工具/agent span 随全局 provider 真实导出 |
| ack 网关日志（`ack.WithLogger`） | 已桥 grape logger（见 [internal/agent](../agent/)） | 不带——ack 日志 API 无 ctx 参数 |

局限（如实声明）：框架 `log.SetLevel` 治理其自带 zap logger，桥接后不再
影响任何输出（级别归 grape 日志配置）；无 ctx 调用点无链路字段；Fatal
保持 zap 语义（记 error 后退出）。

## Kratos 侧 span

`tracing.Server()`（kratos contrib otel）由 [internal/server](../server/) 在
`server.telemetry.enabled` 时插入中间件链，位置在 grape 链路中间件之内、
认证之外：链路中间件先行剥除不可信 `traceparent`，otel 中间件此时从
（可能已剥空的）请求头 Extract，父事实以链路中间件注入的 SpanContext
为准——可信网络的 fail-closed 信任模型不被绕过。HTTP 与 gRPC 同链，
agent 前缀路由（AG-UI / ack）经 `ctx.Middleware` 入链，同被 span 覆盖。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [telemetry.go](telemetry.go) | `NewSetup` / `*Setup`：SDK 生命周期（校验、exporter、资源属性、采样、全局注入、Shutdown cleanup）；`newTracerProvider` 是测试接缝（导出器可注入内存实现）。 |
| [bridge.go](bridge.go) | 框架观测面桥接：日志门面（无 ctx 适配器 + ctx 函数变量覆盖）与 Tracer 全局变量。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewSetup`。 |

## 与其他层的关系

- 只依赖 `conf`、otel SDK 与框架观测门面（仅桥接赋值），不承载业务数据流；
- `server` 持 `*Setup` 决定 otel 中间件是否入链（时序锚点：保证全局注入
  先于 server 构造）；消费面（审计、复现）经 ctx SpanContext 取
  trace_id，不直接依赖本包。

## 测试

测试与被测代码同目录：开关语义（disabled 全 noop / enabled 全局注入）、
配置校验（endpoint 必填、比例越界拒绝）、内存导出器断言 span 产生（grape
侧 + 框架桥接侧）、桥接日志的 trace_id 贯通与无 ctx 局限、Tracef 开关、
级别面覆盖；框架全局变量在用例内保存现场、退出恢复。
