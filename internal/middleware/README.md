# middleware

入口中间件：HTTP / gRPC 服务在 handler 之前执行的横切能力，当前包含认证（[auth.go](auth.go)）、操作审计（[audit.go](audit.go)）、功能权限码判定（[permission.go](permission.go)）、链路标识（[trace.go](trace.go)）与跨域共享（[cors.go](cors.go)）。认证中间件持有免认证白名单与 fail-closed 口径，是请求身份成立前的唯一放行点；操作审计中间件在身份成立后把管理写请求追加到 `sys_operation_log`；判定中间件持有操作名 → 权限码码表，是管理面端点的功能权限关口；链路中间件生成或继承链路标识并回传。本层只处理身份、链路与操作审计事实，不承载业务数据流，也不参与 DTO 转换。

## 入口侧流向

```
client 请求（HTTP / gRPC）
  │
  ▼
server 装配的中间件链（recovery → trace → auth → operation audit → permission）
  ├─ trace：上游 traceparent 合法且对端可信则继承 trace-id，否则剥头新建根；
  │         把 SpanContext 注入 ctx，回写 x-trace-id / x-request-id
  ├─ auth ：免认证操作名直接放行；其余端点校验 Authorization: Bearer 令牌，
  │         失败返回 401，成功把 biz.Identity 注入 ctx
  ├─ operation audit：按操作名识别管理写请求（跳过 List/Get/Query 与 agent
  │         业务面），以已注入身份追加 sys_operation_log（操作类型、目标表、
  │         目标 ID、响应/请求载荷快照、请求事实与结果码；权限拒绝 403
  │         同样入账，密码类固定掩码、其余敏感键无盐哈希），写入失败不影响响应
  └─ permission：按 tr.Operation() 查权限码码表；表内端点以已注入身份调
             fn_has_permission 判定，不通过返回 403（ErrPermissionDenied），
             码表外端点原样放行
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
| [audit.go](audit.go) | 操作审计中间件（命名类型 `OperationAuditMiddleware`）。在认证之后、权限之前入链：按 `tr.Operation()` 的 Service/Method 识别管理写请求（Create→CREATE、Delete/Revoke→DELETE、其余写→UPDATE），映射目标表（用户/部门/用户组/角色/菜单/字典两级/配置族/租户/会话），跳过 List/Get/Query 读方法与 agent 业务面；以认证身份追加 `sys_operation_log`（用户名经 `AuditUsernameResolver` 回读本人资料补齐，HTTP 取路径/方法/对端 IP/User-Agent，gRPC 取端点/GRPC/peer；request_id 优先上游 x-request-id，缺省回落 trace-id；结果码取 Kratos 错误码，成功 200）。快照侧：`target_id` 从响应/请求 proto 的标识字段提取（含内嵌实体），`new_value` 取响应实体（失败/空响应回落请求载荷）经 protojson 序列化；密码类键（`password`/`oldPassword`/`newPassword`/`passwordHash`/`passwd`）固定掩码 `***`，其余敏感键（token/hash/secret/privateKey，配置族另含 `value`）做无盐确定性哈希（`sha256:` 前缀，同值同摘要）；`old_value` 不在通用层采集，保持 NULL。写入 best-effort，失败只记日志；身份缺失（公开端点）直接放行。 |
| [trace.go](trace.go) | 链路中间件。仅当 `traceparent` 为合法 W3C 格式且对端 IP 落在 `server.trace.trusted_networks` 内才继承 trace-id，否则剥头并新建根；注入 otel SpanContext 供日志自动携带 trace_id / span_id，回写 `x-trace-id`，`x-request-id` 上游有则原样回传、无则以 trace-id 兜底；配置含非法 CIDR 时构造函数报错、拒绝启动。`TraceMiddleware` 为命名类型（底层 `middleware.Middleware`），与认证中间件同型不同参时供 Wire 区分。 |
| [cors.go](cors.go) | CORS 中间件（HTTP 专属，gRPC 不装配）。`NewCORS` 按 `server.http.cors` 解析：关闭返回 nil（server 跳过装配，不输出任何 CORS 头），开启时白名单必填、origin 形态非法即拒绝启动。链内恒回 `Access-Control-Expose-Headers: x-trace-id, x-request-id`（跨域时链路 ID 必须可读）；Origin 命中白名单回显 Allow-Origin 并带 `Vary: Origin`，未命中不带 Allow-Origin（浏览器侧自拒）；预检（OPTIONS + `Access-Control-Request-Method`）短路 204，方法/请求头按预检请求回显，Max-Age 按配置可选。`MethodNotAllowed` 兜底生成代码路由的预检（方法不匹配在 gorilla/mux 层即 405、进不了链）。 |
| [permission.go](permission.go) | 功能权限码判定中间件（命名类型 `PermissionMiddleware`，与认证/链路同型不同名供 Wire 区分）。按 `tr.Operation()` 查 `operationPermissionCodes` 码表（用户/部门/用户组/角色/菜单/字典/配置族（全局/租户/功能开关/翻译）/租户管理面与审计日志/会话治理面的全部端点，码值即 biz 权限码常量 = 种子权限点；用户分配选择器按分配动作设码——角色候选/当前角色/设置角色并入 `sys:user:assign-role`，归属候选并入 `sys:user:transfer-owner`）；表内端点以认证中间件注入的身份经 `fn_has_permission` 判定（每请求库内直查），不通过返回 `biz.ErrPermissionDenied`（403），表外端点原样放行（认证生命周期、agent 业务面与权限码查询端点不设码）。全局配置读取本身即全局口径（sys_config 无 tenant_id、无 RLS），其读端点并入 `sys:config:global:query`，不设 super 例外；租户管理面（`sys:tenant:*`）方法名带 Super 表达平台级租户实体管理，租户树边界仍由 RLS 承担，入口判定与其他管理面同型。fail-closed：取不到传输上下文或受控端点身份缺失按未认证拒绝。必须排在认证中间件之后（身份事实由前者注入）。 |
| [provider.go](provider.go) | Wire `ProviderSet`：导出 `NewAuthMiddleware` / `NewPermissionMiddleware` / `NewOperationAuditMiddleware` / `NewAuditUsernameResolver` / `NewTraceMiddleware`（CORS 由 server 从 conf 直接构造，不经 Wire——同一 conf 双处消费，见 [../server/http.go](../server/http.go)）。 |

## 接入要点

- 新中间件：在 [provider.go](provider.go) 增加构造函数并注册进 `ProviderSet`，再到 [server](../server/) 的 HTTP / gRPC 装配处按序加入链；同型多实例须用命名类型承载，否则 Wire 无法按类型装配。
- 免认证条目只加有明确依据的端点：登录与刷新（身份成立前调用，凭据分别为用户名口令与刷新令牌）、登出（凭据是访问令牌本身，且重复登出幂等——严格活跃会话校验会把已撤销令牌拦在 401，故由 service 自取令牌处理）；新增条目须说明凭据形态与校验方式，默认拒绝。
- 依赖边界：只允许 `api/...` 操作名、`biz`、`conf`、`pkg/utils` 与传输/错误/追踪基础库（`transport`、kratos errors、otel trace、grpc peer）；不导入 `service`、`data`，不在本层实现业务规则。
- 需要免认证的业务回调（验签替代令牌）仍归 `service`，只把路由模板加入白名单，不得靠漏调入链实现。

## 测试

现有覆盖：认证中间件、操作审计中间件（写操作/读操作/权限拒绝/请求事实与目标表映射）、权限判定中间件（码表外零调用/无传输 fail-closed/受控端点缺身份/无权限 403 与码透传/有权限放行/仓储错误传播/码表完整性——覆盖用户/部门/用户组/角色/菜单/字典/配置族（全局/租户/功能开关/翻译）/租户管理面与审计/会话治理面全部端点的码表一一对应，租户 7 端点精确映射 `sys:tenant:query|create|update|delete`）、链路标识生成/继承/回传、CORS 构造校验（关闭/空清单/非法 origin）、暴露头与回显、预检短路（不触 handler）、405 兜底（预检应答/其余回落）。
