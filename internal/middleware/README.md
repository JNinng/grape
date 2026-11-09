# middleware

入口中间件：HTTP / gRPC 服务在 handler 之前执行的横切能力，包含认证
（[auth.go](auth.go)）、操作审计（[audit.go](audit.go)）、功能权限码判定
（[permission.go](permission.go)）、链路标识（[trace.go](trace.go)）与跨域
共享（[cors.go](cors.go)）。本层只处理身份、链路与操作审计事实，不承载业务
数据流，也不参与 DTO 转换。链内顺序、装配位置与手写端点归属见
[../README.md](../README.md)。

## 关键约束

- **免认证白名单准入标准**：只收有明确依据的端点——登录与刷新（身份成立前
  调用，凭据分别为用户名口令与刷新令牌）、登出（凭据是访问令牌本身，且重复
  登出幂等；严格的活跃会话校验会把已撤销令牌拦在 401，故由 service 自取
  `Authorization` 头处理）。新增条目须说明凭据形态与校验方式，默认拒绝。
- **认证 fail-closed**：取不到传输上下文直接拒绝（无法判定端点是否公开）；
  缺头、格式非法与令牌无效统一返回 `biz.ErrSessionInvalid`（401，不区分
  原因），成功后把 `biz.Identity` 注入 ctx。
- **链路信任模型**：仅当 `traceparent` 为合法 W3C 格式且对端 IP 落在
  `server.trace.trusted_networks` 内才继承 trace-id，否则剥头新建根；本跳
  span-id 恒新生成。`x-trace-id` 恒回传（错误路径同样），`x-request-id`
  上游有则原样回传、无则以 trace-id 兜底。取不到传输上下文时原样放行（无头
  可回、无链路可标，与认证的 fail-closed 不同）；非法 CIDR 使构造函数报错、
  拒绝启动。
- **操作审计快照脱敏**：`target_id` 取响应或请求 proto 的标识字段（含内嵌
  实体），`new_value` 取响应实体、失败或空响应回落请求载荷，`old_value`
  不在通用层采集、保持 NULL。密码类键（`password` / `oldPassword` /
  `newPassword` / `passwordHash` / `passwd`）固定掩码 `***`；其余敏感键
  （token 及其 hash 形态、secret、privateKey，配置族另含 `value`）做无盐
  确定性哈希（`sha256:` 前缀，同值同摘要）。写入 best-effort，失败只记日志；
  写入 ctx 剥离请求的取消与截止（超时或断开的请求同样入账），另设 5s 独立限时。
- **审计位置**：认证之后、权限之前——身份已成立，权限拒绝（403）也入账；
  读方法（List / Get / Query）与 agent 业务面跳过，身份缺失（公开端点）
  直接放行。
- **CORS 仅 HTTP**：链内恒回 `Access-Control-Expose-Headers: x-trace-id,
  x-request-id`；Origin 命中白名单回显并带 `Vary: Origin`，未命中不带
  Allow-Origin；预检（OPTIONS + `Access-Control-Request-Method`）短路 204
  并按请求回显方法/头。关闭时返回 nil，server 跳过装配、不输出任何 CORS 头。

## 功能权限码

功能权限码与数据范围正交，码表命中端点由本层在 handler 之前经
`fn_has_permission` 判定，不通过返回 `biz.ErrPermissionDenied`（403）；
码表外端点原样放行。必须排在认证之后（身份由前者注入），fail-closed：无
传输上下文或受控端点缺身份一律拒绝。三层权限模型（租户边界 / 行级数据
范围 / 功能权限码）与码表口径见
[../../permission.md](../../permission.md#功能权限码)。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [auth.go](auth.go) | 认证中间件：查 `publicOperations` 白名单，其余要求 `Bearer` 令牌并经 `SessionUsecase` 校验，注入身份。 |
| [audit.go](audit.go) | 操作审计（`OperationAuditMiddleware`）：按操作名推导操作类型与目标表，追加 `sys_operation_log`。 |
| [trace.go](trace.go) | 链路中间件（`TraceMiddleware`）：生成或继承链路标识，注入 SpanContext 并回传头。 |
| [cors.go](cors.go) | CORS 构造与链内中间件、`MethodNotAllowed` 预检兜底。 |
| [permission.go](permission.go) | 功能权限码判定（`PermissionMiddleware`）：持有操作名 → 权限码码表。 |
| [provider.go](provider.go) | Wire `ProviderSet`：认证 / 权限 / 审计 / 用户名解析器 / 链路（CORS 由 server 从 conf 直接构造）。 |

## 测试

- **认证与权限**：公开操作跳过校验、无传输 fail-closed、缺失/非法令牌统一
  401、码表外零调用、无权限 403 与码透传、码表与种子权限点一一对应。
- **链路**：可信网段继承、不可信/非法/缺失 traceparent 新建根、无传输直通、
  错误响应也回传头、非法 CIDR 拒绝构造。
- **操作审计**：写请求入账与请求事实、读请求跳过、权限拒绝入账、请求已超时
  仍以独立限时写入、快照取标识与回落、密码掩码与其他敏感键哈希。
- **CORS**：开关与清单校验、暴露头与 Origin 回显、预检短路不触 handler、
  405 兜底。

## 接入要点

- 新中间件：在 [provider.go](provider.go) 注册并到 [../server/](../server/)
  按序入链；同型多实例须用命名类型承载（`TraceMiddleware` /
  `PermissionMiddleware` / `OperationAuditMiddleware` 先例），否则 Wire
  无法按类型装配。
- 依赖边界：只允许 `api/...` 操作名、`biz`、`conf`、`pkg/utils`、
  `pkg/redact` 与传输/错误/追踪基础库；不导入 `service`、`data`。
- 需要免认证的业务回调（验签替代令牌）仍归 `service`，只把路由模板加入
  白名单，不得靠漏调入链实现。
