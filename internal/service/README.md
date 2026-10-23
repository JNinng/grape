# service

传输层适配器：每个资源一个文件，在边界完成 DTO ↔ DO 转换，把 query.v1 动态查询协议转为 `dynq.Query`，随后委托 [biz](../biz/) 用例；本层不实现业务规则，入参校验在 biz 落地。

## 分层角色

- 第一类业务入口，与 [task](../task/) 平级；上游是 `api/` 中声明的 HTTP / gRPC 契约，下游是 [biz](../biz/)。
- 只导入 `api/...` 与 `biz`；手写端点可额外导入 `transport/http`。完整 import 方向见 [internal/README.md](../README.md) 的「分层与数据流向」。
- 服务由 [server](../server/) 按生成代码路由挂载；本层不构建传输层、不装配中间件链。
- 受保护端点的身份事实只从 context 取（`authOf`），缺失按未认证 fail-closed；令牌校验由中间件完成，不在本层重复实现。

## 数据流向

```
   client
     │  proto 请求 / 响应（DTO）
     ▼
┌─────────┐
│ service │  DTO ↔ DO；query.v1.Query → dynq.Query
└─────────┘
     │  DO + dynq.Query
     ▼
┌─────────┐
│   biz   │  用例与入参校验；声明仓储接口
└─────────┘
     │  DO + dynq.Query
     ▼   repo IF：biz 声明，data 实现（依赖反转）
```

边界上的两次转换：

- DTO ↔ DO：请求字段映射为 `biz` 的 DO 或入参结构（如 `biz.LoginInput`、`biz.OwnerTriple`），返回值再渲染为 proto；空串经 `optionalString` 归一为未设置，可空时间戳经 `tsPBPtr` 渲染。
- 查询协议：`queryv1.Query` 经 `convertQuery` 转为 `dynq.Query`（fields / where / sort / pagination）；枚举过滤值按 `enumAliases` 从线上枚举名归一化为存储取值；`page` 与 `after` 互斥，冲突报 `errPageAfterConflict`。转换失败经 `invalidQueryError` 包装为资源自身的 INVALID_ARGUMENT，响应页码由 `pageOf` 回填。

## 关键文件导览

| 文件 | 职责 |
| ---- | ---- |
| [provider.go](provider.go) | `ProviderSet` 聚合 `NewUserService` / `NewSessionService`，由 cmd 的 Wire 统一装配。 |
| [query.go](query.go) | 跨资源查询转换：`convertQuery` 把 `queryv1.Query` 转 `dynq.Query`；`normalizeEnumValue` 归一化枚举过滤值（仅 `eq` / `neq` / `in` / `not_in` 且字段已登记别名时生效）；`invalidQueryError` 包装转换错误；`pageOf` 计算分页信封页码。 |
| [session.go](session.go) | `SessionService`：`Login` 把请求映射为 `biz.LoginInput` 调用 `AuthUsecase`，`Refresh` 调 `SessionUsecase`；`toLoginReply` 渲染响应（Bearer 令牌、过期时间、用户摘要）。 |
| [user.go](user.go) | `UserService`：用户 CRUD、`ListUsers`（动态查询）、`SetUserPassword`、`DeleteUser`、`AssignRoles`、`TransferUserOwner`；`authOf` 读取身份、缺失按未认证处理；`toProtoUser` / `optionalString` / `normalizeUserStatus` 等转换辅助。 |

## 接入要点

新增资源 service：

1. 嵌入 `api/<domain>/<version>` 生成的 `Unimplemented<Resource>ServiceServer`，构造函数接收 `biz` 用例并加入 `service.ProviderSet`；
2. 在方法内完成 DTO → DO 与 DO → DTO 映射；List 方法用 `convertQuery` 转换查询（存在枚举字段时传入 `enumAliases`），转换错误用 `invalidQueryError` 映射为 INVALID_ARGUMENT；
3. 受保护端点先用 `authOf` 取身份，再委托用例；
4. 在 `server` 的 HTTP / gRPC 注册处挂载服务；改依赖后 `make generate` 刷新 wire 装配。

手写端点（非 proto 生成）：归属与中间件链见 [internal/README.md](../README.md) 的「手写端点归属」；实现放 `service/<资源>_http.go`，用 `srv.Route` 注册并在 handler 内调用 `ctx.Middleware`，且必须提供「无令牌 → 401」的测试守住认证链。

## 测试

现有覆盖：以内存假仓储替换 `biz` 仓储接口，隔离验证 DTO 转换与身份透传。
