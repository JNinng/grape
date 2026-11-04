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
| [dept.go](dept.go)                | 部门 DO `Dept`、分页信封 `DeptList` 与树节点 `DeptNode`；`DeptRepo`（CRUD、租户内全量列表、移动、软删）与 `DeptUsecase`：name/owner 必填与 status 校验、创建缺省启用、父级存在性校验（引用无效 INVALID_ARGUMENT）、移动的防环检测（沿新父级祖先链上溯）、树装配（孤儿行防御性按根挂载）；闭包表是派生数据不进领域模型。 |
| [usergroup.go](usergroup.go)      | 用户组 DO `UserGroup`、分页信封 `UserGroupList` 与成员条目 `UserGroupMember`（映射行联结用户展示字段的只读视图）；`UserGroupRepo`（CRUD、成员列表分页、批量增删成员）与 `UserGroupUsecase`：name 必填与 status/remark 列宽校验、创建缺省启用（code 仅创建路径必填，身份列创建后不变）、成员 ID 去重排序、成员分页归一（页码下限 1、页大小 20/200）；组删除为软删，成员映射行物理残留并由消费侧活跃过滤排除。 |
| [role.go](role.go)                | 角色 DO `Role`、分页信封 `RoleList`、数据范围配置视图 `RoleDataScope`（角色行标量 + 两张明细表活跃行集合）与明细条目 `RoleIDScopeEntry`；`RoleRepo`（CRUD、数据范围读取/整组替换、菜单授权读取/整组替换）与 `RoleUsecase`：name/code/remark 列宽与 status 校验、创建缺省启用并强制租户自定义类型与仅本人范围（code/role_type 身份列创建后不变，data_scope/scope_dept_id 归配置面）、data_scope 1-7 与明细/分发部门取值校验、明细按 (type, data_id) 与 ID 集合去重升序、空菜单集合合法（清空授权）；系统预置角色不可删除（仓储锁定行后拒绝），明细/映射引用对象的存在性校验收在仓储（引用未知整组拒绝）。 |
| [menu.go](menu.go)                | 菜单 DO `Menu`、树节点 `MenuNode`；`MenuRepo`（读：GetByID/ListAll；写为受控写面 `CreateSuper`/`UpdateSuper`/`MoveSuper`/`SoftDeleteSuper`——经部署面 SECURITY DEFINER 固定口径写函数写入，运行角色无直写权限）与 `MenuUsecase`：name/permission_code 列宽与可空文本列校验、创建缺省启用（permission_code/menu_type 身份/类型列创建后不变）、父级存在性校验（引用无效 INVALID_ARGUMENT）、移动的防环检测（沿新父级祖先链上溯）、树装配（孤儿行防御性按根挂载）、删除有未删除直接子菜单拒绝；父级/防环/软删口径同时由受控写函数在库内兜底。 |
| [auth.go](auth.go)                | 登录用例：`LoginInput` / `LoginResult`、`UserCredential` 与账号摘要 `UserSummary`；租户回落、防枚举（不存在与口令错同因）、口令校验通过后判禁用、透明重哈希、委托 `SessionUsecase` 签发会话。                          |
| [session.go](session.go)          | 会话 DO `Session` 与状态/TTL/元数据列宽常量；`SessionRepo` 的 Issue / Validate / Revoke / RevokeByAccessTokenHash（登出）/ Refresh 动作及入参出参；`SessionUsecase` 负责令牌生成、sha256 哈希、元数据截断；会话与登录相关类型化错误也定义于此。          |
| [password.go](password.go)        | 口令哈希单点：`HashPassword`（argon2id PHC）与 `VerifyPassword`（按行内算法标识分发 argon2id/argon2i、bcrypt、种子 scrypt），返回是否需重哈希；`ErrPasswordHashUnsupported` 表示库内哈希格式异常。                      |
| [identity.go](identity.go)        | 服务端身份事实 `Identity`（TenantID + UserID）与私有 ctx 键；`WithIdentity` 仅供认证中间件注入，`IdentityFromContext` 缺失时由调用方按未认证 fail-closed 处理。                                                           |
| [maintenance.go](maintenance.go)  | 设施无关契约：诊断面 `FacilityDiagnostics` 与 `MetricSample` / `MetricType`，维护面 `FacilityMaintenance` 与 `MaintenanceAction` / `MaintenanceReport`；ops 指标出口与 task 维护循环只认契约，不读原始实现。             |
| [vectorstore.go](vectorstore.go)   | 向量存储设施契约 `VectorStore`（框架无关形状，租户隔离的强制点）：`VectorDocument` / `VectorFilter` / `ScoredVectorDocument` 与 Add/Get/Update/Delete/Search/DeleteByFilter/Count/GetMetadata；身份不进契约参数，实现必须从认证 ctx 提取并强制租户过滤；嵌入空间纪律——文档与过滤显式携带空间键（Embedder）与维度（Dimension）成对字段，Search 必填并与注册表对账，Count/DeleteByFilter/GetMetadata 可空即跨空间；`ErrVectorStoreDisabled` / `ErrVectorDocNotFound` 类型化错误。 |
| [audit.go](audit.go)               | Agent 业务审计：工具调用/授权确认的记录入参与完整行 DO、`AuditRepo` 契约（Append/List/Get/组合批量）与 `AuditUsecase`（入参校验、身份与 trace_id 从认证 ctx 提取、参数/结果摘要按 `pkg/redact` 的 tool_param/retrieval 类别脱敏——类别未登记放行原值但保留 4096 rune 截断兜底）；`ErrAuditInvalidArgument` / `ErrAuditNotFound`。 |
| [retrieval_log.go](retrieval_log.go) | Agent 检索留痕：留痕记录与完整行 DO（命中 `RetrievalHit` 的 json 标签即落库 jsonb 元素键、`Embedder` 记操作空间）、`RetrievalLogRepo` 契约（Append/List，追加写无更新面）与 `RetrievalLogUsecase`（查询侧分页归一与来源取值校验；写入侧不经用例——留痕行由 data 层 VectorStore 留痕装饰器在操作现场构造直写，脱敏在装饰器落库前完成）；会话 ID / 查询文本的 ctx 事实载体（`WithSessionID` / `WithRetrievalQuery` 及读取函数，agent 层注入、装饰器消费，事实缺失如实留空）；`ErrRetrievalLogInvalidArgument`。 |
| [embedder.go](embedder.go)        | 文本向量化设施契约 `Embedder`（`Embed(ctx, embedder, text)` + `Dimension(embedder)`，多嵌入空间路由，框架无关最小面）与默认空间解析契约 `EmbedderResolver`（`ResolveDefaultEmbedder` 返回键+维度成对：请求显式 > 租户配置 > 全局兜底，实现在 data 层）；嵌入空间 ctx 事实载体 `WithEmbedderSpace` / `EmbedderSpaceFromContext`（框架 SearchQuery 不可扩展，适配器据此填充契约过滤）；`ErrEmbedderDisabled` / `ErrEmbedderUnknown` 类型化错误。 |
| [document.go](document.go)        | 摄取管线 `DocumentUsecase`：简版固定长度 + 重叠切分（rune 窗口，可替换点）→ 解析默认嵌入空间 → 逐块 embed（响应长度与声明维度对账）→ 经 VectorStore 契约写入（切片携带空间键与维度）；幂等取「同来源覆盖」（切片 ID 确定性派生 `#index`，摄取前按 source_id 锚点跨空间清旧行——换默认空间的重摄取即迁移路径）；切片元数据含来源标识与原文 rune 区间锚点；`ErrDocumentSourceTooLong` 类型化错误。 |
| [permission.go](permission.go)    | 功能权限码：管理面权限码常量（`<资源>:<操作>`，与部署面种子权限点一一对应）、`ErrPermissionDenied`（入口层判定不通过的唯一 403 语义）、`PermissionRepo` 契约（fn_has_permission 判定与 GetPermissionCodes 枚举）与 `PermissionUsecase`（入口判定中间件与权限码查询端点的业务面；每请求库内直查，应用内无缓存）。 |
| [grant.go](grant.go)              | Agent 工具授权：`ToolGrant` DO 与 `GrantRepo` 契约（许可评估原语 FindActive* 的 miss 以 (nil, nil) 表达正常控制流，管理原语按 id 定位）；`GrantUsecase` 编排会话级先于全局级的许可解析（Resolve/Remember，agent 适配层消费）与管理面（ListOwn/ListTenant/Revoke，手写端点消费，功能权限码在用例层判定）；功能权限码常量与类型化错误。 |
| [metrics.go](metrics.go)          | Agent 运行观测契约 `AgentRunObserver`（RunActive/RunRejected/ToolCallObserved）：agent 适配层推运行事实、ops 的 RuntimeMetrics 实现，依赖反转接缝与 FacilityDiagnostics 同型。 |
| [provider.go](provider.go)        | Wire `ProviderSet`：注册 `NewUserUsecase`、`NewDeptUsecase`、`NewUserGroupUsecase`、`NewRoleUsecase`、`NewMenuUsecase`、`NewSessionUsecase`、`NewAuthUsecase`、`NewAuditUsecase`、`NewDocumentUsecase`、`NewGrantUsecase`、`NewPermissionUsecase`、`NewRetrievalLogUsecase`、`NewRedactRegistry`（审计表/检索留痕与复现视图共用的脱敏注册表单点）。                                                                                                                                       |


## 关键约束

- 令牌与口令哈希只在 biz 单点生成/校验，仓储出入参只见哈希；登录点查对软删除/跨租户与口令错误同因返回（防枚举）。

## 新增资源接入要点

- 声明 DO 与 `<Resource>Repo` 接口：DO 为纯领域模型（无 proto/存储标签）；读/写方法收 `Identity` 作为租户边界与审计操作者（身份成立前的登录点查例外，以租户 ID 入参，见 `GetCredentialForLogin`，不使用 `super` 命名），List 收 `*dynq.Query`，接口由 data 实现而 biz 不导入 data。
- 基于接口实现 `<Resource>Usecase`：入参必填校验、缺省值与跨字段规则在此落地；失败返回 Kratos 类型化错误，reason 使用 `api/<domain>/v1` 中声明的枚举。
- 需要在 biz 内生成的敏感值（口令哈希、令牌及哈希）沿用现有单点函数；选项类型零值在用例内落为缺省值。
- 新增 `New<Resource>Usecase` 构造函数并加入 [provider.go](provider.go) 的 `ProviderSet`；装配变化后按仓库约定重跑 `make generate`。

## 测试

现有覆盖：口令多算法校验与重哈希、会话令牌边界、登录编排与防枚举、审计用例的脱敏消费（类别命中/未登记放行但截断）、写入校验与批量转发、授权用例（[grant_test.go](grant_test.go)：Resolve 会话级优先/回退/两级 miss、Remember 幂等与会话/全局作用域、ListTenant/Revoke 的权限码 403 与归属 404 边界）；摄取管线（[document_test.go](document_test.go)，替身 embedder + 契约替身）：切分边界（短文/恰界/重叠窗/零重叠/多字节/空白）、维度失配构造期拒绝、同来源覆盖幂等与删除先行、来源标识与锚点元数据、身份缺失 fail-closed、embed 失败不触库、中途 Add 失败上报；检索留痕查询用例（[retrieval_log_test.go](retrieval_log_test.go)，替身仓储）：分页缺省与上限、来源/会话/offset 校验 fail-closed、身份缺失 fail-closed；部门用例（[dept_test.go](dept_test.go)，mock 仓储）：创建校验与缺省启用、父级存在性与点查失败透传、移动防环（自父/未知父/移入自身子树拒、跨枝与移根放行）、更新/删除/树装配（嵌套顺序、孤儿按根、总数）；用户组用例（[usergroup_test.go](usergroup_test.go)，mock 仓储）：创建校验（name/code 列宽、remark 超长、status 取值、组长正 ID）与缺省启用、更新转发与缺 id 拒绝、成员增删的去重排序/空集与缺组拒绝、仓储错误透传、成员分页归一（页码下限、页大小上下限）；角色用例（[role_test.go](role_test.go)，mock 仓储）：创建校验（name/code/remark 列宽、status 取值）与缺省启用、服务端管理列强制（租户自定义类型/仅本人范围/层级限定为空）、更新与删除转发、数据范围配置的取值校验（data_scope 1-7、明细类型与正 ID、分发部门正 ID）与去重升序、菜单授权的去重排序与空集合清空、非法入参不触仓储、仓储错误透传；菜单用例（[menu_test.go](menu_test.go)，mock 仓储）：创建校验（name/permission_code/可空文本列宽、menu_type 1-3、status 取值）与缺省启用、父级存在性与点查失败透传（非 NotFound 原样上抛）、移动防环（自父/未知父/移入自身子树拒、跨枝与移根放行）、更新/删除/树转发与树装配（嵌套顺序、孤儿按根、总数）、非法入参不触仓储。

