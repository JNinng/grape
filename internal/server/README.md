# server

HTTP / gRPC 服务器的构建与挂载层：读取 `conf`、装配中间件、注册生成代码
路由、注册 Agent 手写路由、直挂运维 Handler 与前端静态托管。本目录是唯一
挂载点，`service` / `biz` 不感知监听与路由；不出现 DTO 翻译或业务逻辑。
中间件链顺序、分层流向、手写端点归属与 `ctx.Middleware` 纪律见
[../README.md](../README.md)。

## 关键约束

- **StrictSlash 关闭**：gorilla/mux 默认会把 `/agui/` 反向 301 规整到
  `/agui`，与内层 `ServeMux` 互踢成重定向环，故显式
  `http.StrictSlash(false)`。关闭后无尾斜杠形态由内层以 307（保方法）规整
  到 `/agui/`；生成代码路由的尾斜杠变体由 301 收紧为 404。
- **SPA 兜底与保留前缀**：静态托管经 `srv.HandlePrefix("/")` 链外直挂，
  必须最后注册（语义即「其余路由全部未命中」）。`ReservedAPIPrefixes`
  给出 `/v1`、`/metrics` 与 agent 前缀，按边界匹配判定（等于前缀或以子路径
  开头），`/v1x` 之类相邻路径不受影响；前缀内未知路径与方法失配一律 404，
  绝不回页面壳。assets 带内容哈希可 immutable 长缓存，`index.html` 禁缓存。
- **注册顺序**：gorilla/mux 按注册顺序匹配，agent 会话列表与事件窗口的
  精确路径必须先于 agent 前缀通配注册，否则被通配吞并。
- **otel 信任模型**：`telemetry.enabled` 时 `tracing.Server()` 入链，位置在
  grape 链路中间件之内、认证之外——不可信 `traceparent` 已被链路中间件
  剥除，otel 的 Extract 以链路中间件注入的 SpanContext 为父，可信网络
  fail-closed 的信任模型不被绕过；认证之外使未认证请求（401）同样产生
  span。HTTP 与 gRPC 的 otel 位置一致，agent 手写路由经 `ctx.Middleware`
  入链同被覆盖。
- **CORS 兜底**：`http.cors.enabled` 时，生成代码路由的预检（OPTIONS 与
  注册方法不匹配）在 gorilla/mux 层即 405、进不了链，由
  `http.MethodNotAllowedHandler` 兜底应答；CORS 配置缺白名单等错误在装配期
  拒绝启动。机制归 [../middleware/README.md](../middleware/README.md)。

## 超时与 SSE

`server.http.timeout` 归零后传输层不限时，普通路由限时由 [timeout.go](timeout.go)
的路由级中间件按 `server.http.request_timeout` 恢复，同步调用嵌入端点的知识
库路由按 `embedding_request_timeout`（未配置回落前者）；agent 前缀（SSE 长流）
按操作名边界匹配跳过，防止 `/aguiX` 类相邻操作被误跳过。各配置项语义、
心跳与前置代理要求见 [../../operations.md](../../operations.md#sse-与超时治理)，
本目录只保留挂载实现。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [http.go](http.go) | `NewHTTPServer`：装配中间件链、按注册顺序挂载生成路由与手写路由、`srv.Handle` 直挂 `/metrics`、最后挂载 SPA；CORS 启用时接 405 兜底。 |
| [grpc.go](grpc.go) | `NewGRPCServer`：同构的 gRPC 装配与生成服务注册；不挂运维出口，无 SSE 不涉超时治理。 |
| [web.go](web.go) | `NewWebHandler` / `ReservedAPIPrefixes`：嵌入 dist 静态托管、保留前缀按边界 404、assets 长缓存与 `index.html` 禁缓存、非 GET/HEAD 405。 |
| [timeout.go](timeout.go) | `NewRouteTimeoutMiddleware`：按 `request_timeout` 恢复普通路由限时，向量化路由登记表按 `embedding_request_timeout`，agent 前缀跳过；请求超时非正时退化为直通。 |
| [provider.go](provider.go) | Wire `ProviderSet`：暴露 `NewGRPCServer` / `NewHTTPServer` / `NewRouteTimeoutMiddleware` / `NewWebHandler`。 |

## 测试

- **挂载与链外语义**：`/metrics` 不经业务中间件；SPA 刷新不 404、未知 API
  路径（含方法失配）不回页面壳、assets 缓存头、占位产物可服务。
- **认证与权限链序**：无令牌 401 先于权限判定，放行后到达真实 service；
  无权限 403 携带权限 reason（含知识检索端点，守卫码表漏配）。
- **Agent 手写路由**：无令牌 401、精确路径先于前缀通配、SSE 长流跨超时点
  不断流、`x-trace-id` 回传且分片阶段中途可见、echo 工具端到端。
- **观测与跨域**：OTel 经内存导出器断言 agent 路由产生 server span；CORS
  预检链内短路与生成路由 405 兜底、白名单外不带 Allow-Origin、关闭零头。
- **路由级超时**：普通路由超时、agent 操作跳过、相邻前缀不误跳过；向量化
  路由取 `embedding_request_timeout`、未配置回落、超过普通超时仍完成。

## 接入要点

- 新增生成服务：在 [http.go](http.go) 与 [grpc.go](grpc.go) 分别调用生成的
  注册函数并把 `*service.<X>Service` 加入入参，随后 `make generate` 刷新 Wire。
- 运维 / 探针 Handler 与业务手写端点都不在本目录实现：前者由 `ops` 构造后
  经 `srv.Handle` 直挂，后者归 `service/*_http.go` 并经 `srv.Route` +
  `ctx.Middleware` 入链，归属见 [../README.md](../README.md#手写端点归属)。
- Agent 手写路由由 `service` 侧提供注册方法（会话列表与事件窗口的装配面在
  [../agent/](../agent/)），[http.go](http.go) 只按上述顺序调用。
