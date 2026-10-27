# 权限体系

多租户 RBAC 数据权限的落点规范：每类约束由哪一层强制执行、数据范围如何推导、读与写的判定路径、错误语义，以及新增资源时的实现清单。

SQL 对象签名与索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)，部署顺序见 [deploy_db.md](deploy_db.md)。

## 约束

权限分三类，各归一层：

| 约束 | 回答的问题 | 执行者 | 失效姿态 |
|---|---|---|---|
| 租户边界 | 数据属于哪个租户 | PostgreSQL RLS（26 张租户表，ENABLE + FORCE：21 张基线在 rls.sql、范围版本表在 expose.sql、Agent 工具授权表在 96_agent_grant.sql、Agent 审计两表在 schema/95_agent_audit.sql、检索留痕表在 schema/97_agent_retrieval_log.sql——策略随域文件走） | 未设 `app.tenant_id` → 默认全拒 |
| 行级数据范围 | 用户能看见、操作哪些行 | 读：`@scope`（列表）与 `fn_check_visible`（单行）；写：`fn_can_*` | 缺 Scope → 生成代码报错；判定 false → 拒绝 |
| 功能权限码 | 用户能否调用某个操作 | 入口层（service / middleware）调 `fn_has_permission` | 未接入；接入后判定 false → 拒绝 |

- 租户边界由数据库兜底，应用层不重复检查。RLS 只承担租户这个绝对边界，不承担行级数据范围。
- 行级数据范围依赖目标行数据与用户权限关系，写侧判定必须在库内、与写入同一事务完成。
- 功能权限码与行无关，只依赖操作本身，在入口层判定，不进入仓储方法。
- 操作者与 Scope 只从服务端认证上下文注入（`IdentityTx.Auth()`）；请求体中的 `tenantId`、`owner*`、`scope` 一律不可信。
- Agent 设施面（agent schema，如向量表 `agent.agent_document`）不走 RLS 与 @scope：租户边界由应用层适配器强制——所有读写从认证 ctx 提取身份、每条 SQL 带租户过滤、缺身份 fail-closed（TODO 决策 6），身份同时写入行与 metadata，调用方伪造不生效；RLS 语句以注释附在建表脚本（`agent_vector.sql`）供按需启用，属第二道防线、不作隔离前提，集成测试以无 RLS 场景断言隔离仍成立。跨租户管理操作（在线重建索引、租户级全量清理）属 super 管理口径：向量层 D1 仅留占位与本条说明，不暴露普通入口，落地时按第 10 条补审计与受限端点。

## 数据范围推导（三个推导函数）

可见范围只由三个函数推导，入参只有用户 ID：

| 函数 | 维度 |
|---|---|
| `fn_accessible_user_ids` | 归属人 |
| `fn_accessible_dept_ids` | 归属部门 |
| `fn_accessible_group_ids` | 归属用户组 |

角色、`data_scope` 1–7、部门闭包、用户组成员全部在库内查，调用方不传权限参数；撤销角色下一次查询立即生效。

返回值三态：

| 返回值 | 含义 |
|---|---|
| `NULL` | 有全租户角色（`data_scope=7`），该维度不过滤 |
| `'{}'` | 用户无效（不存在/禁用/删除）或没有有效角色，什么都看不到 |
| 数组 | 可见 ID 并集 |

列表守卫、单行判定、写前判定共用同一口径：

- 三个维度 OR，任一非空归属命中对应可见集合即可见；
- 三个归属全空的数据只有全租户角色可见；
- 用户无效什么都看不到。

`@scope` 只挂在有归属三元组（`owner_user_id` / `owner_dept_id` / `owner_user_group_id`）的表上，当前为 `basic` 与 `sys_user`；其余表租户内全可见。修改推导口径时，列表守卫、单行判定、写前判定三处必须同改。

## 写形态

锁定、取行与行权限判定合并为一条语句：`LockXxxPermitted` 用
`fn_can_operate_row` 过滤并 `FOR UPDATE`，返回的行即已锁定且可操作；
业务逻辑与写入继续在同一 `IdentityTx` 内执行。

```sql
-- queries/sys_user.sql
-- name: LockSysUserPermitted :one
SELECT * FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_can_operate_row(sqlc.arg('operator'),
        owner_user_id, owner_dept_id, owner_user_group_id)
  AND (sqlc.narg('permission_code')::varchar IS NULL
    OR public.fn_has_permission(sqlc.arg('operator'), sqlc.narg('permission_code')::varchar))
FOR UPDATE;
```

`permission_code` 可空：传值则同时校验功能权限，传 NULL 只校验行级数据范围；空串按无此权限处理。

```go
// internal/data/pg_sqlc/user.go 的 Update 形态
if _, err := s.Q().LockSysUserPermitted(ctx, gen.LockSysUserPermittedParams{
    ID: u.ID, Operator: auth.UserID, PermissionCode: nil,
}); err != nil {
    return err // pgx.ErrNoRows → NotFound
}
updated, err := s.Q().UpdateSysUser(ctx, params) // 同一事务，行锁持有到提交
```

约束：

- 无行 = 不存在、不可见或权限码不匹配，统一 NotFound；不可见行不会被锁定。
- READ COMMITTED 下 `FOR UPDATE` 对并发更新后的行复核 `WHERE`，不会按旧归属放行。
- 判定与写入同事务，行锁持有到提交。
- 业务授权（新归属可达、角色分发）仍用专用函数单独判定，失败返回 Forbidden。
- 可见性谓词只在判定函数里写一份，`UPDATE` / `DELETE` 不重复谓词。
- 不在 biz / Go 里重算 `data_scope`。

[业务授权用专用函数](internal/data/pg_sqlc/sqlc/README.md)：

| 操作 | 判定函数 |
|---|---|
| 修改 / 删除 | `fn_can_operate_row` |
| 修改归属 | `fn_can_transfer_owner`（旧归属可操作 + 新归属可达） |
| 分配角色 | `fn_can_assign_role` / `fn_can_assign_roles` |
| 其他业务授权 | 在 `operate.sql` 新增专用 `fn_can_*`，配命名查询并同步索引 |

### 以 User 增删改为例

| 操作 | 判定与写入 |
|---|---|
| 新增 `CreateUser` | 无既有行，不适用 `fn_can_operate_row`：`InsertSysUser` 在同一 `IdentityTx` 写入，租户取 `app.tenant_id`，`created_by` / `updated_by` 取 `auth.UserID`。请求可携带归属三元组，按约束应校验新归属可达（口径同 `fn_can_transfer_owner` 的新归属部分）；`CreateUser` 尚未接入该判定，属待补约束 |
| 修改 `UpdateUser` | `LockSysUserPermitted` 锁定并判定 → `UpdateSysUser`；不可见返回 NotFound |
| 删除 `DeleteUser` | `LockSysUserPermitted` 锁定并判定 → `SoftDeleteSysUser` 置 `deleted_at` |
| 改密 `SetUserPassword` | `LockSysUserPermitted` 锁定并判定 → `SetSysUserPassword` |
| 改归属 `TransferUserOwner` | `LockSysUserPermitted`（旧归属）→ `fn_can_transfer_owner`（新归属可达）→ `UpdateSysUserOwner` |
| 分配角色 `AssignRoles` | `fn_can_assign_roles` 逐角色判定（不锁目标行）→ `AssignUserRoles`；任一 false 整体回滚 |

## 读形态

| 场景 | 路径 |
|---|---|
| 列表 / 计数 | 动态查询协议；生成代码在 WHERE 头部注入 `@scope` 三维守卫，缺 Scope fail-closed；List 与 Count 共用守卫，`total` 与 `items` 权限视图一致 |
| 单行 / 详情 | `GetXxxPermitted`：`fn_check_visible`（掩码 7）+ 可空权限码，一条查询完成取行与判定；不可见 → NotFound |
| 外部批量过滤 | `fn_filter_visible_rows`；只读，拒绝表现为空集，不是错误 |

```sql
-- name: GetSysUserPermitted :one
SELECT * FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_check_visible(sqlc.arg('operator'), 7::int2,
        owner_user_id, owner_dept_id, owner_user_group_id)
  AND (sqlc.narg('permission_code')::varchar IS NULL
    OR public.fn_has_permission(sqlc.arg('operator'), sqlc.narg('permission_code')::varchar));
```

- 软删过滤由表注释 `@filter` 在解析期注入；固定命名查询在 SQL 里自写 `deleted_at IS NULL`。
- 不手写绕过 `@scope` 的聚合查询；专用列表走专用命名查询，守卫仍取自 `fn_accessible_*`。
- 单行读用 `GetXxxPermitted` 一次完成取行与判定。

### 以 User 读为例

| 场景 | 路径 |
|---|---|
| 列表 | `ParseSysUserQuery` → `params.Scope = s.SysUserScope()` → `ListSysUser` / `CountSysUser`；守卫由 `@scope` 注入，缺 Scope 报错 |
| 详情 | `GetSysUserPermitted`（掩码 7；权限码可空）→ 不可见 NotFound，不泄露存在性 |
| 登录凭据 | `GetUserCredentialByName` 走 `TenantTx`：身份成立前只注入租户，不做数据范围判定；仅返回未软删行的口令哈希 |

## 错误语义

| 情况 | 返回 | 理由 |
|---|---|---|
| 行不存在 / 数据范围不可见 / 权限码不匹配 | NotFound | 不泄露存在性；`LockXxxPermitted` / `GetXxxPermitted` 未命中返回 `pgx.ErrNoRows`，由仓储 `mapErr` 统一映射 |
| 看得见，但业务授权不允许（分配角色、转归属到不可达对象） | Forbidden（`ErrUserAccessDenied`） | 存在性不敏感，明确拒绝 |
| 只读判定的 false | 不是错误 | 调用方按布尔值处理 |

取行查询里的权限码校验未命中同样落到 NotFound；需要 403 的功能权限拒绝，应在入口层单独判 `fn_has_permission`，取行查询传 NULL。权限失败不用「0 行」隐式表达：0 行既可能是没权限，也可能是并发删除，仓储层无法区分。

## 功能权限码

功能权限码与数据范围正交：只看角色 → 菜单 → 权限码这条链，不管能看见哪些行。

落点：入口层（service 或 auth 中间件）按 `tr.Operation()` 映射权限码，再透传给 `GetXxxPermitted` / `LockXxxPermitted`，在库内随行判定一起校验；需要 403 的入口在透传前单独判 `fn_has_permission`。

已落地的入口（C2）：Agent 工具授权管理（手写端点，无 proto 操作名，码在 `biz` 常量声明）——`agent:grant:query`（全租户授权列表）、`agent:grant:revoke`（吊销他人授权），种子菜单 252/253；本人授权列表与本人吊销不设码（认证身份即凭据）。判定收在 `biz.GrantUsecase`（入口的直接受托方），仓储方法不携带权限参数。

- 与行无关，可缓存；失效用 `sys_perm_version`（租户级 + 用户级版本）。
- 权限码可空：传值则在取行时一并校验，NULL 只校验数据范围；空串按无此权限处理。
- 仓储缺省传 NULL；入口层强制功能权限时把权限码透传到仓储参数。
- 取行查询内的权限码拒绝表现为 NotFound。
- 不与 `@scope` 合并成一层。

在入口层映射权限码之前，RPC 只受令牌校验与行级判定约束。

## 新增资源 / 操作速查

1. **建表**：`tenant_id` + 审计列 + 归属三元组；表注释声明 `@scope`、`@filter: deleted_at is_null`、`@pk`、`@page`；`owner_*` 建索引；`rls.sql` 补策略并 ENABLE + FORCE。
2. **列表 / 计数**：复用 `query.v1.Query` 动态协议，Scope 由 `IdentityTx` 注入。
3. **详情**：`GetXxxPermitted`（`fn_check_visible` 掩码 7，权限码可空），不可见返回 NotFound。
4. **修改 / 删除 / 改密**：`LockXxxPermitted`（`fn_can_operate_row` + `FOR UPDATE`，权限码可空）→ 写入；业务授权另判。
5. **修改归属**：`LockXxxPermitted`（旧归属可操作）+ `fn_can_transfer_owner`（新归属可达）→ 写入。
6. **批量写**：单事务内逐行锁 + 判，或新增批量判定函数；外部批量读过滤用 `fn_filter_visible_rows`。
7. **无归属三元组的表**：不声明 `@scope`，行级无范围；写侧靠功能权限码或数据库授权（`sys_menu` / `sys_config` 已对运行角色 REVOKE 写权限，由管理端写入）。
8. **文档化例外**：登录点查（`TenantTx`）、令牌校验/登出/刷新（`TokenTx`）、介质维护（`SECURITY DEFINER` 清理）是仅有的绕过路径，不得扩展到其他读写。
9. **测试**：每个资源覆盖全租户看全集、范围角色看子集、范围外读 NotFound、范围外写不改行、转归属不可达 Forbidden、缺 Scope fail-closed、未设 `app.tenant_id` 全拒。
10. **约定**：有权限的领域方法默认按权限处理（Scope / RLS 强制生效），无需额外标注。只有“身份已成立、接口有意绕过其租户/数据范围约束”的方法才在名称带 `super`（如管理端跨租户查询 `ListAllTenantsSuper`），并配审计日志、受限端点与跨范围测试。
    - 显式例外，不使用 `super`：身份成立前的凭据/令牌查询与令牌维度撤销（`GetCredentialForLogin`、`SessionRepo.Validate` / `RevokeByAccessTokenHash`（登出）/ `Refresh`）、设施维护面（`FacilityMaintenance.Maintain`）、已授权事务内的 data 层内部回读（`GetSysUserByID`）。
    - SQL 命名查询不参与该约定；内部回读靠使用边界注释约束，不在 biz 接口上暴露。

## 索引

| 内容 | 位置 |
|---|---|
| 运行角色授权、RLS 会话变量写入权（`GRANT SET ON PARAMETER`） | [role.sql](internal/data/pg_sqlc/sqlc/role.sql) |
| 租户 RLS、`fn_accessible_*` 三件套 | [rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) |
| 写前判断函数 `fn_can_*` | [operate.sql](internal/data/pg_sqlc/sqlc/operate.sql) |
| 只读判定、功能权限码、范围版本 | [expose.sql](internal/data/pg_sqlc/sqlc/expose.sql) |
| 判定函数的命名查询 | [queries/operate.sql](internal/data/pg_sqlc/sqlc/queries/operate.sql)、[queries/perm.sql](internal/data/pg_sqlc/sqlc/queries/perm.sql) |
| 取行与判定的合并查询（`GetSysUserPermitted` / `LockSysUserPermitted`） | [queries/sys_user.sql](internal/data/pg_sqlc/sqlc/queries/sys_user.sql) |
| `@scope` 声明 | [schema/10_basic.sql](internal/data/pg_sqlc/sqlc/schema/10_basic.sql)、[schema/30_user.sql](internal/data/pg_sqlc/sqlc/schema/30_user.sql) |
| 会话门面、Scope 注入口 | [internal/data/pg_sqlc/session.go](internal/data/pg_sqlc/session.go) |
| 写路径（`LockSysUserPermitted` + 写入） | [internal/data/pg_sqlc/user.go](internal/data/pg_sqlc/user.go) |
| 生成的三维守卫 | [internal/data/pg_sqlc/gen/sys_user_dynamic_gen.go](internal/data/pg_sqlc/gen/sys_user_dynamic_gen.go) |
