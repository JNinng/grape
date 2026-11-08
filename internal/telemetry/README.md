# telemetry

观测装配层：OTel TracerProvider 的生命周期（开关、OTLP 导出、采样、
Shutdown）与 trpc-agent-go 框架观测面桥接（日志门面 + Tracer 全局变量）
只在这里落子。不承载业务数据流，只依赖 `conf`、otel SDK 与框架观测门面
（仅桥接赋值）。

## 开关语义

`NewSetup(conf.Telemetry, logger)` 产出 `*Setup` 依赖锚点 + wire cleanup：

- **enabled=false（默认）**：SDK 完全 noop——不构造 exporter、不发起任何
  连接、不改写 otel 全局与框架 Tracer 变量，全链路 span 保持 no-op。
- **enabled=true**：校验配置（endpoint 必填、`sampler_ratio` 落在 `[0,1]`）
  → OTLP gRPC exporter → SDK TracerProvider（资源属性 `service.name` 空回落
  `grape`、`service.version` 空省略）→ `otel.SetTracerProvider` 注入全局 →
  框架 Tracer 桥接。进程退出经 cleanup `Shutdown`（5s 上限）冲刷缓冲，超时
  即放弃剩余 span、不拖住停机。

注意：框架**日志**桥接与开关无关，`NewSetup` 一开始就把 trpc-agent-go 日志
门面接管到 grape logger（ctx 可得的调用点自动携带 `trace_id`）。因此
「enabled=false 完全 noop」只指 SDK 与 Tracer 全局，不含日志门面。

采样器是裸 `TraceIDRatioBased`（非 ParentBased），决策独立于父 span 的
sampled 标志；`sampler_ratio=0` 视为未设置（全采），零导出需求直接
`enabled=false`。`x-trace-id` 响应头与日志 `trace_id` 字段来自 ctx 中的
SpanContext，与开关和采样无关、恒可用。

## 框架桥接

| 桥接面 | 形态 | trace_id |
| ------ | ---- | -------- |
| 框架无 ctx 日志（`log.Infof` 等 → `Default` / `ContextDefault`） | 适配器实现框架 `log.Logger` 接口，转发 grape logger | 不带——门面 API 不收 ctx，框架既成事实，不伪造 |
| 框架 ctx 日志（`log.InfofContext` 等，package 级函数变量） | 覆盖变量，转发 ctx 到 grape logger | 自动携带——logger 由 kratos `log.NewLogger(WithExtractor(tracing.TraceAttrs))` 构造，落盘时展开 SpanContext |
| 框架 `telemetry/trace.Tracer` 全局变量 | 桥到 `otel.GetTracerProvider()`，scope 名保持 `trpc.agent.go` | 框架 LLM / 工具 / agent span 随全局 provider 真实导出 |
| ack 网关日志（`ack.WithLogger`） | 已桥 grape logger（见 [../agent/](../agent/)） | 不带——ack 日志 API 无 ctx 参数 |

桥接局限（如实声明）：框架 `log.SetLevel` 治理其自带 zap logger，桥接后不再
影响任何输出（级别归 grape 日志配置）；无 ctx 调用点无链路字段；
`Tracef` / `TracefContext` 受框架 `IsTraceEnabled` 门控并落 grape debug 级别；
`Fatal` 保持 zap 语义（记 error 后退出）。

## Kratos 侧 span

`tracing.Server()` 由 [../server/](../server/) 在 `telemetry.enabled` 时插入
中间件链，位置在 grape 链路中间件之内、认证之外，HTTP 与 gRPC 同链；agent
手写路由经 `ctx.Middleware` 入链同被覆盖。信任模型（不可信 `traceparent`
已被链路中间件剥除）见
[../server/README.md](../server/README.md#关键约束)。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [telemetry.go](telemetry.go) | `NewSetup` / `*Setup`：校验、exporter、资源属性、采样、全局注入与 Shutdown cleanup；`NewSetupWithExporter` 是测试接缝。 |
| [bridge.go](bridge.go) | 框架观测面桥接：日志门面（无 ctx 适配器 + ctx 函数变量覆盖）与 Tracer 全局变量。 |
| [provider.go](provider.go) | Wire `ProviderSet`：`NewSetup`。 |

## 测试

- **开关语义**：disabled 不构造 exporter、不改写全局；enabled 注入全局
  provider 并经 cleanup 回收。
- **配置校验**：enabled 时 endpoint 必填、采样比例越界拒绝。
- **导出**：内存导出器断言 grape 侧与框架桥接侧 span 均产生。
- **日志桥接**：ctx 调用贯通 `trace_id`、无 ctx 调用不带、`Tracef` 受门控、
  各级别方法覆盖；用例保存并恢复框架全局变量。
