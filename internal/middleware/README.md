# middleware

入口中间件：HTTP / gRPC 服务在 handler 之前执行的横切能力，当前包含认证（[auth.go](auth.go)）、链路标识（[trace.go](trace.go)）与跨域共享（[cors.go](cors.go)）。认证中间件持有免认证白名单与 fail-closed 口径，是请求身份成立前的唯一放行点；链路中间件生成或继承链路标识并回传。本层只处理身份与链路事实，不承载业务数据流，也不参与 DTO 转换。

## 入口侧流向

```
client 请求（HTTP / gRPC）
  │
  ▼
server 装配的中间件链（recovery → trace → auth）
  ├─ trace：上游 traceparent 合法且对端可信则继承 trace-id，否则剥头新建根；
  │         把 SpanContext 注入 ctx，回写 x-trace-id / x-request-id
  └─ auth ：免认证操作名直接放行；其余端点校验 Authorization: Bearer 令牌，
            失败返回 401，成功把 biz.Identity 注入 ctx
  │
  ▼
handler / service（生成代码路由；业务数据流从 service 开始）
```

- 只读传输层操作名与请求头，向 ctx 放入身份与链路事实，不改写请求 / 响应体；
- 认证失败即终止，不进入 handler；链路回传头在调用 handler 前写入，错误响应同样携带；
- `srv.Handle` 直挂的运维端点不经过本链，见 [internal/README.md](../README.md) 的「手写端点归属」。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [auth.go](auth.go) | 认证中间件。按 `tr.Operation()` 查 `publicOperations` 白名单（登录、刷新、登出）；其余端点要求 `Authorization: Bearer <token>`，缺头、格式非法与令牌无效统一返回 `biz.ErrSessionInvalid`（401，不向调用方区分原因），校验通过把 `biz.Identity` 注入 ctx；取不到传输上下文直接拒绝（fail-closed）。 |
| [trace.go](trace.go) | 链路中间件。仅当 `traceparent` 为合法 W3C 格式且对端 IP 落在 `server.trace.trusted_networks` 内才继承 trace-id，否则剥头并新建根；注入 otel SpanContext 供日志自动携带 trace_id / span_id，回写 `x-trace-id`，`x-request-id` 上游有则原样回传、无则以 trace-id 兜底；配置含非法 CIDR 时构造函数报错、拒绝启动。`TraceMiddleware` 为命名类型（底层 `middleware.Middleware`），与认证中间件同型不同参时供 Wire 区分。 |
| [cors.go](cors.go) | CORS 中间件（HTTP 专属，gRPC 不装配）。`NewCORS` 按 `server.http.cors` 解析：关闭返回 nil（server 跳过装配，不输出任何 CORS 头），开启时白名单必填、origin 形态非法即拒绝启动。链内恒回 `Access-Control-Expose-Headers: x-trace-id, x-request-id`（TODO E1：跨域时链路 ID 必须可读）；Origin 命中白名单回显 Allow-Origin 并带 `Vary: Origin`，未命中不带 Allow-Origin（浏览器侧自拒）；预检（OPTIONS + `Access-Control-Request-Method`）短路 204，方法/请求头按预检请求回显，Max-Age 按配置可选。`MethodNotAllowed` 兜底生成代码路由的预检（方法不匹配在 gorilla/mux 层即 405、进不了链）。 |
| [provider.go](provider.go) | Wire `ProviderSet`：导出 `NewAuthMiddleware` / `NewTraceMiddleware`（CORS 由 server 从 conf 直接构造，不经 Wire——同一 conf 双处消费，见 [../server/http.go](../server/http.go)）。 |

## 接入要点

- 新中间件：在 [provider.go](provider.go) 增加构造函数并注册进 `ProviderSet`，再到 [server](../server/) 的 HTTP / gRPC 装配处按序加入链；同型多实例须用命名类型承载，否则 Wire 无法按类型装配。
- 免认证条目只加有明确依据的端点：登录与刷新（身份成立前调用，凭据分别为用户名口令与刷新令牌）、登出（凭据是访问令牌本身，且重复登出幂等——严格活跃会话校验会把已撤销令牌拦在 401，故由 service 自取令牌处理）；新增条目须说明凭据形态与校验方式，默认拒绝。
- 依赖边界：只允许 `api/...` 操作名、`biz`、`conf` 与 `pkg/utils`；不导入 `service`、`data`，不在本层实现业务规则。
- 需要免认证的业务回调（验签替代令牌）仍归 `service`，只把路由模板加入白名单，不得靠漏调入链实现。

## 测试

现有覆盖：认证中间件、链路标识生成/继承/回传、CORS 构造校验（关闭/空清单/非法 origin）、暴露头与回显、预检短路（不触 handler）、405 兜底（预检应答/其余回落）。
