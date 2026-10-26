# server

HTTP / gRPC 服务器的构建与挂载层：读取 `conf`、装配中间件、注册生成代码路由、注册 Agent 手写路由、直挂运维 Handler；不出现 DTO 翻译或业务逻辑。

## 挂载与流向

入参：`conf`（`http` / `grpc` 监听网络、地址与超时）、`service`（生成契约的服务实现与 Agent 手写端点）、`middleware`（链路与认证）、`ops`（`/metrics` 原始 Handler）。出参：`*http.Server` / `*grpc.Server`（Kratos `transport.Server`），由 `cmd/server` 交给 `kratos.App` 随应用启停。

```
client
  │  HTTP / gRPC
  ▼
listener（conf 的 network / addr / timeout；http.timeout 归零即传输层不限时）
  │
  ▼
中间件链：recovery → 路由级超时（agent 前缀跳过）→ trace → auth
  │
  ▼
生成代码 handler（api/... 的 Register 函数）＋ Agent 手写路由（/agui 前缀）
  │
  ▼
service ──► biz ／ agent（框架适配层）

/metrics（ops 原始 Handler，srv.Handle 直挂）
  └── 位于业务中间件链外，免认证
```

`server` 是唯一挂载点：中间件链、生成代码路由、Agent 手写路由与运维直挂都只在此拼装，`service` / `biz` 不感知监听与路由。

SSE 超时治理：Kratos 入站 filter 按 `server.http.timeout` 给每个请求的 ctx 施加截止时间，AG-UI 的 SSE 写循环监听该 ctx，全局超时会截断长流。当前形态是 `timeout: 0s`（传输层不限时）+ 路由级超时中间件按 `request_timeout` 恢复普通路由限时，agent 前缀（SSE 长流）跳过；AG-UI 运行时长由 `agent.agui.timeout` 兜底。另因 gorilla/mux 的 StrictSlash 会把 `/agui/` 反向规整成重定向环，HTTP 服务器关闭了 StrictSlash（生成路由的尾斜杠变体由 301 收紧为 404）。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [http.go](http.go) | `NewHTTPServer`：按 `conf.Server.Http` 设置 network / addr / timeout 并关闭 StrictSlash，装配 `recovery → 路由级超时 → trace → auth` 链，经 `srv.Handle` 直挂 `/metrics`，注册 user / session 的生成 HTTP 路由并调用 `AgentService.RegisterHTTP` 挂载 Agent 手写路由。 |
| [timeout.go](timeout.go) | `NewRouteTimeoutMiddleware`：路由级请求超时中间件，按 `request_timeout` 恢复普通路由限时，agent 前缀（SSE 长流）跳过；`server.http.timeout` 归零后的 SSE 治理配套。 |
| [grpc.go](grpc.go) | `NewGRPCServer`：按 `conf.Server.Grpc` 构造 gRPC 服务器，装配 `recovery → trace → auth` 链，注册 user / session 的生成 gRPC 服务；不挂运维出口，gRPC 无 SSE 不涉超时治理。 |
| [provider.go](provider.go) | Wire `ProviderSet`：暴露 `NewGRPCServer` / `NewHTTPServer` / `NewRouteTimeoutMiddleware`，由 `cmd/server` 统一装配。 |
| [metrics_route_test.go](metrics_route_test.go) | 守卫 `/metrics` 直挂路由的链外语义：原始 Handler 不经业务中间件。 |
| [agent_route_test.go](agent_route_test.go) | 守卫 Agent 手写路由的挂载语义：无令牌 401（fail-closed）、无尾斜杠 307 规整、echo 工具端到端、跨超时点 SSE 不断流、`x-trace-id` 回传。 |

## 接入要点

- 新增服务：在 [http.go](http.go) 与 [grpc.go](grpc.go) 中分别调用生成的 `Register<X>ServiceHTTPServer` / `Register<X>ServiceServer`，并把 `*service.<X>Service` 加入两个构造函数入参，随后 `make generate` 刷新 Wire。
- 中间件链只在本目录装配（当前 `recovery → 路由级超时 → trace → auth`）；新增中间件在此插入，不在 `service` 内另行包装。
- 运维 / 探针 Handler 由 `ops` 构造后经 `srv.Handle` 直挂：原生 `net/http.Handler`，位于业务链外、免认证；不要用它挂业务端点。
- 业务手写端点（上传下载、SSE、WebSocket 等）归 `service/<资源>_http.go`，须用 `srv.Route` 注册并在 handler 内调用 `ctx.Middleware` 入链；归属、注册方式与中间件链要求见 [../README.md](../README.md) 的「手写端点归属」，此处不复述。Agent 手写路由（AG-UI / ack）由 [../service/agent_http.go](../service/agent_http.go) 提供注册方法，[http.go](http.go) 只调用。

## 测试

现有覆盖：`/metrics` 直挂路由的链外语义（原始 Handler 不经业务中间件）；Agent 手写路由的入链语义（无令牌 401、SSE 长流跨超时点、trace-id 回传、echo 工具端到端）。
