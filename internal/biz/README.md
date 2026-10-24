# biz

领域层，只认 DO。定义 DO、用例、仓储接口（依赖反转的接缝）、类型化错误与选项类型（如 `SessionOptions`），动态查询以 `*dynq.Query` 作为协议；领域规则（必填校验、缺省值等）只在这里落地。

## 分层角色

- 位置：上游是 service / task，下游是 data；biz 声明仓储接口，data 实现，依赖方向恒为 service/task → biz → data。
- biz 对 `api/...` 的唯一依赖是错误 reason 枚举（`sessionv1.ErrorReason_*`、`v1.ErrorReason_*`），不感知 DTO 请求/响应，也不感知 PO 与存储标签。
- 需要动态查询的 List 方法以 `*dynq.Query` 作为查询协议随仓储接口跨界；biz 只透传，不解析该协议。

## 数据流向

```
┌──────────────┐
│ service/task │  service：DTO ↔ DO；query.v1 → dynq.Query（task 直接构造）
└──────────────┘
       │  DO + dynq.Query
       ▼
┌──────────┐
│   biz    │  用例编排与领域校验；声明 Repo 接口与类型化错误
└──────────┘
       │  DO + dynq.Query
       ▼  Repo IF：biz 声明，data 实现（依赖反转）
┌──────────┐
│   data   │  DO ↔ PO；解析 dynq.Query；驱动错误映射
└──────────┘
       │  PO
       ▼
    storage
```

错误沿原路返回：biz 抛出 Kratos 类型化错误（reason 引用 `api/...` 枚举），data 在仓储内把驱动错误映射为这些错误，service / task 不再自行翻译。

## 关键文件导览

| 文件                              | 职责                                                                                                                                                                                                                   |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [user.go](user.go)                | 用户 DO `User`、分页信封 `UserList`、归属三元组 `OwnerTriple`；`UserRepo`（CRUD、角色分配、归属转移、登录凭据点查）与 `UserUsecase`；name/nickname 必填与 status 校验、创建缺省值、角色 ID 去重排序。                     |
| [auth.go](auth.go)                | 登录用例：`LoginInput` / `LoginResult`、`UserCredential` 与账号摘要 `UserSummary`；租户回落、防枚举（不存在与口令错同因）、口令校验通过后判禁用、透明重哈希、委托 `SessionUsecase` 签发会话。                          |
| [session.go](session.go)          | 会话 DO `Session` 与状态/TTL/元数据列宽常量；`SessionRepo` 的 Issue / Validate / Revoke / Refresh 四动作及入参出参；`SessionUsecase` 负责令牌生成、sha256 哈希、元数据截断；会话与登录相关类型化错误也定义于此。          |
| [password.go](password.go)        | 口令哈希单点：`HashPassword`（argon2id PHC）与 `VerifyPassword`（按行内算法标识分发 argon2id/argon2i、bcrypt、种子 scrypt），返回是否需重哈希；`ErrPasswordHashUnsupported` 表示库内哈希格式异常。                      |
| [identity.go](identity.go)        | 服务端身份事实 `Identity`（TenantID + UserID）与私有 ctx 键；`WithIdentity` 仅供认证中间件注入，`IdentityFromContext` 缺失时由调用方按未认证 fail-closed 处理。                                                           |
| [maintenance.go](maintenance.go)  | 设施无关契约：诊断面 `FacilityDiagnostics` 与 `MetricSample` / `MetricType`，维护面 `FacilityMaintenance` 与 `MaintenanceAction` / `MaintenanceReport`；ops 指标出口与 task 维护循环只认契约，不读原始实现。             |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewSessionUsecase`、`NewAuthUsecase`。                                                                                                                                       |

## 关键约束

- 令牌与口令哈希只在 biz 单点生成/校验，仓储出入参只见哈希；登录点查对软删除/跨租户与口令错误同因返回（防枚举）。

## 新增资源接入要点

- 声明 DO 与 `<Resource>Repo` 接口：DO 为纯领域模型（无 proto/存储标签）；读/写方法收 `Identity` 作为租户边界与审计操作者（身份成立前的登录点查例外，以租户 ID 入参，见 `GetCredentialForLogin`，不使用 `super` 命名），List 收 `*dynq.Query`，接口由 data 实现而 biz 不导入 data。
- 基于接口实现 `<Resource>Usecase`：入参必填校验、缺省值与跨字段规则在此落地；失败返回 Kratos 类型化错误，reason 使用 `api/<domain>/v1` 中声明的枚举。
- 需要在 biz 内生成的敏感值（口令哈希、令牌及哈希）沿用现有单点函数；选项类型零值在用例内落为缺省值。
- 新增 `New<Resource>Usecase` 构造函数并加入 [provider.go](provider.go) 的 `ProviderSet`；装配变化后按仓库约定重跑 `make generate`。

## 测试

现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举。
