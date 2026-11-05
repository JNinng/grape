# 权限体系

多租户 RBAC 数据权限的落点规范：每类约束由哪一层强制执行、数据范围如何推导、读与写的判定路径、错误语义，以及新增资源时的实现清单。

SQL 对象签名与索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)，部署顺序见 [deploy_db.md](deploy_db.md)。

## 约束

权限分三类，各归一层：

| 约束 | 回答的问题 | 执行者 | 失效姿态 |
|---|---|---|---|
| 租户边界 | 数据属于哪个租户 | PostgreSQL RLS（26 张租户表，ENABLE + FORCE：21 张基线在 rls.sql、范围版本表在 expose.sql、Agent 工具授权表在 96_agent_grant.sql、Agent 审计两表在 schema/95_agent_audit.sql、检索留痕表在 schema/97_agent_retrieval_log.sql——策略随域文件走） | 未设 `app.tenant_id` → 默认全拒 |
| 行级数据范围 | 用户能看见、操作哪些行 | 读：`@scope`（列表）与 `fn_check_visible`（单行）；写：`fn_can_*` | 缺 Scope → 生成代码报错；判定 false → 拒绝 |
| 功能权限码 | 用户能否调用某个操作 | 入口层判定中间件按操作名查码表调 `fn_has_permission` | 判定 false → 拒绝（403，`ErrPermissionDenied`） |

- 租户边界由数据库兜底，应用层不重复检查。RLS 只承担租户这个绝对边界，不承担行级数据范围。
- 行级数据范围依赖目标行数据与用户权限关系，写侧判定必须在库内、与写入同一事务完成。
- 功能权限码与行无关，只依赖操作本身，在入口层判定，不进入仓储方法。
- 操作者与 Scope 只从服务端认证上下文注入（`IdentityTx.Auth()`）；请求体中的 `tenantId`、`owner*`、`scope` 一律不可信。
- Agent 设施面（agent schema，如向量表 `agent.agent_document`）不走 RLS 与 @scope：租户边界由应用层适配器强制——所有读写从认证 ctx 提取身份、每条 SQL 带租户过滤、缺身份 fail-closed，身份同时写入行与 metadata，调用方伪造不生效；RLS 语句以注释附在建表脚本（`agent_vector.sql`）供按需启用，属第二道防线、不作隔离前提，集成测试以无 RLS 场景断言隔离仍成立。跨租户管理操作（在线重建索引、租户级全量清理）属 super 管理口径：向量层仅留占位与本条说明，不暴露普通入口，落地时按第 10 条补审计与受限端点。

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
| 新增 `CreateUser` | 无既有行，不适用 `fn_can_operate_row`：`InsertSysUser` 在同一 `IdentityTx` 写入，租户取 `app.tenant_id`，`created_by` / `updated_by` 取 `auth.UserID`。请求可携带归属三元组，按约束应校验新归属可达（口径同 `fn_can_transfer_owner` 的新归属部分）；`CreateUser` 尚未接入该判定 |
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

落点：入口层判定中间件（[middleware/permission.go](internal/middleware/permission.go)，`PermissionMiddleware`）按 `tr.Operation()` 查操作码表（用户/部门/用户组/角色/菜单/字典/配置族（全局/租户/功能开关/翻译）/租户管理面与审计日志/会话治理面的全部端点），命中的端点在 handler 之前单独判 `fn_has_permission`，不通过返回 `ErrPermissionDenied`（403）；`GetXxxPermitted` / `LockXxxPermitted` 的可空权限码透传形态保留（随行判定拒绝表现为 NotFound），仓储缺省传 NULL，需要「行级与功能权限一并校验」的入口再把码透传到仓储参数。

已落地的入口：判定中间件管理的面——用户/部门/用户组/角色/菜单/字典/配置族（全局/租户/功能开关/翻译）/租户管理面与审计日志/会话治理面（`operationPermissionCodes` 码表，码值即 `biz` 权限码常量，与部署面种子权限点一一对应；管理读端点并入 `:query` 码、结构移动并入 `:update` 码、授权类写动作单设码；字典两级共用一组操作码、配置族按全局/租户/功能开关/翻译分设操作码、租户管理按 `sys:tenant:query|create|update|delete` 分设（停用/恢复并入 `:update`）、审计日志 List/点查并入各自页面码、会话治理查询/撤销单设码）；权限码查询端点 `GetMyPermissions`（`/v1/permissions/mine`，返回当前用户全部权限码供前端导航过滤）不设码。全局配置读取本身即全局口径（sys_config 无 tenant_id、无 RLS），读端点不设 super 例外；功能开关与翻译是租户表（RLS + 普通 DML），读写端点均不设 super。租户管理面（sys_tenant）方法名带 `super` 表达平台级租户实体管理（管理对象是一整个租户实体，见第 10 条），租户树边界仍由 RLS 承担，不设绕过例外。Agent 工具授权管理（手写端点，无 proto 操作名）——`agent:grant:query`（全租户授权列表）、`agent:grant:revoke`（吊销他人授权），种子菜单 252/253；本人授权列表与本人吊销不设码（认证身份即凭据），判定收在 `biz.GrantUsecase`（入口的直接受托方），仓储方法不携带权限参数。

- 与行无关，可缓存；失效用 `sys_perm_version`（租户级 + 用户级版本）。
- 平台信任边界：`sys:config:global:*` 写码只应由平台根租户持有，且代码在全局配置用例的写路径强制校验调用方租户为根租户（`parent_id IS NULL`）——子租户即使自造或持有写码也返回 403（`ErrConfigPlatformRequired` / `CONFIG_PLATFORM_REQUIRED`）。读端点不设此守卫。
- 权限码可空：传值则在取行时一并校验，NULL 只校验数据范围；空串按无此权限处理。
- 仓储缺省传 NULL；入口层强制功能权限时把权限码透传到仓储参数。
- 取行查询内的权限码拒绝表现为 NotFound。
- 不与 `@scope` 合并成一层。

码表外的端点（认证生命周期、agent 业务面与权限码查询端点）只受令牌校验与行级判定约束。

## 新增资源 / 操作速查

1. **建表**：`tenant_id`（列注释 `[@internal]`，不参与动态 schema/投影/过滤/排序；`sys_tenant.tenant_id` 为租户树结构例外，保留 `[@dynamic: exact @hideable]`）+ 审计列 + 归属三元组；表注释声明 `@scope`、`@filter: deleted_at is_null`、`@pk`、`@page`；`owner_*` 建索引；`rls.sql` 补策略并 ENABLE + FORCE。
2. **列表 / 计数**：复用 `query.v1.Query` 动态协议，Scope 由 `IdentityTx` 注入。
3. **详情**：`GetXxxPermitted`（`fn_check_visible` 掩码 7，权限码可空），不可见返回 NotFound。
4. **修改 / 删除 / 改密**：`LockXxxPermitted`（`fn_can_operate_row` + `FOR UPDATE`，权限码可空）→ 写入；业务授权另判。
5. **修改归属**：`LockXxxPermitted`（旧归属可操作）+ `fn_can_transfer_owner`（新归属可达）→ 写入。
6. **批量写**：单事务内逐行锁 + 判，或新增批量判定函数；外部批量读过滤用 `fn_filter_visible_rows`。
7. **无归属三元组的表**：不声明 `@scope`，行级无范围；写侧靠功能权限码或数据库授权（`sys_menu` / `sys_config` 已对运行角色 REVOKE 写权限，由管理端写入）。`sys_menu` 的管理端写入经部署面 SECURITY DEFINER 固定口径写函数（`fn_menu_create/update/move/delete`，见 [98_menu_write.sql](internal/data/pg_sqlc/sqlc/98_menu_write.sql)）：DEFINER 机械绕过该表 RLS 与直写禁令，函数内以 `app.tenant_id` 复位租户边界（未设置即拒绝），调用方事务纪律不变——API 写方法名按 `super` 约定命名（`CreateMenuSuper` 等），读方法（树查询/点查）不带。`sys_config` 同属受控写面（`fn_config_create/update/delete`，见 [98_config_write.sql](internal/data/pg_sqlc/sqlc/98_config_write.sql)）：表本身无 tenant_id 与 RLS，函数内仍校验认证事务纪律（`app.tenant_id` 未设置即拒绝）；API 写方法名带 `super`（`CreateConfigSuper` 等），读方法（全局配置读取本身即全局口径）不带。
8. **文档化例外**：登录点查（`TenantTx`）、令牌校验/登出/刷新（`TokenTx`）、介质维护（`SECURITY DEFINER` 清理）、受控写面（`fn_menu_*` 在函数内复位租户边界；`fn_config_*` 校验认证事务纪律——`app.tenant_id` 必须已设置，sys_config 无租户列，不做租户复位）与租户删除前置的受控计数面（`fn_tenant_active_child_count`，见 [98_tenant_guard.sql](internal/data/pg_sqlc/sqlc/98_tenant_guard.sql)）是仅有的绕过路径，不得扩展到其他读写。受控计数面的口径：租户树 RLS 下祖父看不到孙行（孙行 tenant_id = 子租户），删除前置的「无未删除直接子租户」完整性判定需要全局事实；函数以 DEFINER 执行绕过 sys_tenant 租户 RLS，但只返回计数、不返回任何行数据，不构成跨租户数据读面，也不提供跨租户写路径；函数内校验 `app.tenant_id` 已设置（fail-closed）且目标对调用方可见（`id` 或 `tenant_id` 命中调用方租户，否则拒绝），消除跨租户结构侧信道。
9. **测试**：每个资源覆盖全租户看全集、范围角色看子集、范围外读 NotFound、范围外写不改行、转归属不可达 Forbidden、缺 Scope fail-closed、未设 `app.tenant_id` 全拒。
10. **约定**：有权限的领域方法默认按权限处理（Scope / RLS 强制生效），无需额外标注。只有“身份已成立、接口有意绕过其租户/数据范围约束”的方法才在名称带 `super`（如管理端跨租户查询），并配受限端点与跨范围测试。操作审计写入面尚未落地（`sys_operation_log` 当前无写入方），super 管理写当前以 `updated_by` 等操作者列留痕；操作审计写入列为已知缺口，不因 super 命名放宽。
    - 显式例外，不使用 `super`：身份成立前的凭据/令牌查询与令牌维度撤销（`GetCredentialForLogin`、`SessionRepo.Validate` / `RevokeByAccessTokenHash`（登出）/ `Refresh`）、设施维护面（`FacilityMaintenance.Maintain`）、已授权事务内的 data 层内部回读（`GetSysUserByID`）。
    - `super` 的第二种用法（平台级实体管理，不是绕过例外）：`sys_tenant` 管理面方法名带 `super`（`CreateTenantSuper` / `GetTenantSuper` / `ListTenantsSuper` / `UpdateTenantSuper` / `DeleteTenantSuper` / `DisableTenantSuper` / `RestoreTenantSuper`）表达「被管理的是一整个租户实体」，而非调用方自己租户内的业务数据。租户树边界仍由 RLS 承担（`sys_tenant_tenant_isolation` 允许命中 `tenant_id = app.tenant_id` 或 `id = app.tenant_id`，树外租户行等同不存在）：租户树由各租户创建自己的直接子租户构成，创建时 `parent_id` 只允许省略或等于调用方租户（结构父级与行归属一致），每个租户只能把自己的直接子租户挂在自身下。默认种子只向平台根租户（`t-super-01` 的超级管理员角色 101）授予 `sys:tenant:*`；子租户如需自助管理其直接子租户，需另行在其租户内铺设菜单/角色（种子不铺）。本面没有新增 RLS 绕过机制或 SECURITY DEFINER 写函数（删除前置的只读计数例外见第 8 条）、写路径是普通 DML；因此不属「绕过约束」的 super 例外，也不因命名放宽审计与测试口径。区分要点：绕过类 super 的方法在实现上刻意不经过租户/范围约束，本类 super 的方法只是对象粒度是租户实体、约束照常生效。
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
