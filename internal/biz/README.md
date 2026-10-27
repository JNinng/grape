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
| [session.go](session.go)          | 会话 DO `Session` 与状态/TTL/元数据列宽常量；`SessionRepo` 的 Issue / Validate / Revoke / RevokeByAccessTokenHash（登出）/ Refresh 动作及入参出参；`SessionUsecase` 负责令牌生成、sha256 哈希、元数据截断；会话与登录相关类型化错误也定义于此。          |
| [password.go](password.go)        | 口令哈希单点：`HashPassword`（argon2id PHC）与 `VerifyPassword`（按行内算法标识分发 argon2id/argon2i、bcrypt、种子 scrypt），返回是否需重哈希；`ErrPasswordHashUnsupported` 表示库内哈希格式异常。                      |
| [identity.go](identity.go)        | 服务端身份事实 `Identity`（TenantID + UserID）与私有 ctx 键；`WithIdentity` 仅供认证中间件注入，`IdentityFromContext` 缺失时由调用方按未认证 fail-closed 处理。                                                           |
| [maintenance.go](maintenance.go)  | 设施无关契约：诊断面 `FacilityDiagnostics` 与 `MetricSample` / `MetricType`，维护面 `FacilityMaintenance` 与 `MaintenanceAction` / `MaintenanceReport`；ops 指标出口与 task 维护循环只认契约，不读原始实现。             |
| [vectorstore.go](vectorstore.go)   | 向量存储设施契约 `VectorStore`（框架无关形状，租户隔离的强制点）：`VectorDocument` / `VectorFilter` / `ScoredVectorDocument` 与 Add/Get/Update/Delete/Search/DeleteByFilter/Count/GetMetadata；身份不进契约参数，实现必须从认证 ctx 提取并强制租户过滤；`ErrVectorStoreDisabled` / `ErrVectorDocNotFound` 类型化错误。 |
| [audit.go](audit.go)               | Agent 业务审计：工具调用/授权确认的记录入参与完整行 DO、`AuditRepo` 契约（Append/List/Get/组合批量）与 `AuditUsecase`（入参校验、身份与 trace_id 从认证 ctx 提取、参数/结果摘要按 `pkg/redact` 的 tool_param/retrieval 类别脱敏——类别未登记放行原值但保留 4096 rune 截断兜底）；`ErrAuditInvalidArgument` / `ErrAuditNotFound`。 |
| [retrieval_log.go](retrieval_log.go) | Agent 检索留痕：留痕记录与完整行 DO（命中 `RetrievalHit` 的 json 标签即落库 jsonb 元素键）、`RetrievalLogRepo` 契约（Append/List，追加写无更新面）与 `RetrievalLogUsecase`（查询侧分页归一与来源取值校验；写入侧不经用例——留痕行由 data 层 VectorStore 留痕装饰器在操作现场构造直写，脱敏在装饰器落库前完成）；会话 ID / 查询文本的 ctx 事实载体（`WithSessionID` / `WithRetrievalQuery` 及读取函数，agent 层注入、装饰器消费，事实缺失如实留空）；`ErrRetrievalLogInvalidArgument`。 |
| [embedder.go](embedder.go)        | 文本向量化设施契约 `Embedder`（Embed/Dimensions，框架无关最小面）：维度纪律——声明维度须与 conf.vector.dimension 对账（摄取管线装配期校验），实现须在每次响应复核；`ErrEmbedderDisabled` 类型化错误。                      |
| [document.go](document.go)        | 摄取管线 `DocumentUsecase`：简版固定长度 + 重叠切分（rune 窗口，可替换点）→ 逐块 embed → 经 VectorStore 契约写入；幂等取「同来源覆盖」（切片 ID 确定性派生 `#index`，摄取前按 source_id 锚点清旧行）；切片元数据含来源标识与原文 rune 区间锚点；装配期校验 embedder 维度与 conf 一致（不一致拒绝构造）。`ErrDocumentSourceTooLong` 类型化错误。 |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewSessionUsecase`、`NewAuthUsecase`、`NewAuditUsecase`、`NewDocumentUsecase`、`NewGrantUsecase`、`NewRetrievalLogUsecase`、`NewRedactRegistry`（审计表/检索留痕与复现视图共用的脱敏注册表单点）。                                                                                                                                       |


## 关键约束

- 令牌与口令哈希只在 biz 单点生成/校验，仓储出入参只见哈希；登录点查对软删除/跨租户与口令错误同因返回（防枚举）。

## 新增资源接入要点

- 声明 DO 与 `<Resource>Repo` 接口：DO 为纯领域模型（无 proto/存储标签）；读/写方法收 `Identity` 作为租户边界与审计操作者（身份成立前的登录点查例外，以租户 ID 入参，见 `GetCredentialForLogin`，不使用 `super` 命名），List 收 `*dynq.Query`，接口由 data 实现而 biz 不导入 data。
- 基于接口实现 `<Resource>Usecase`：入参必填校验、缺省值与跨字段规则在此落地；失败返回 Kratos 类型化错误，reason 使用 `api/<domain>/v1` 中声明的枚举。
- 需要在 biz 内生成的敏感值（口令哈希、令牌及哈希）沿用现有单点函数；选项类型零值在用例内落为缺省值。
- 新增 `New<Resource>Usecase` 构造函数并加入 [provider.go](provider.go) 的 `ProviderSet`；装配变化后按仓库约定重跑 `make generate`。

## 测试

现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举、审计用例的脱敏消费（类别命中/未登记放行但截断）、写入校验与批量转发。；摄取管线（[document_test.go](document_test.go)，替身 embedder + 契约替身）：切分边界（短文/恰界/重叠窗/零重叠/多字节/空白）、维度失配构造期拒绝、同来源覆盖幂等与删除先行、来源标识与锚点元数据、身份缺失 fail-closed、embed 失败不触库、中途 Add 失败上报；检索留痕查询用例（[retrieval_log_test.go](retrieval_log_test.go)，替身仓储）：分页缺省与上限、来源/会话/offset 校验 fail-closed、身份缺失 fail-closed。

