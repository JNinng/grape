# 权限体系

多租户 RBAC 数据权限的落点规范：每类约束由哪一层强制执行、数据范围如何推导、读与写的判定路径、错误语义，以及新增资源时的实现清单。

SQL 对象签名与索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)，部署顺序见 [deploy_db.md](deploy_db.md)。

## 约束

权限分三类，各归一层：

| 约束 | 回答的问题 | 执行者 | 失效姿态 |
|---|---|---|---|
| 租户边界 | 数据属于哪个租户 | PostgreSQL RLS（21 张租户表，ENABLE + FORCE） | 未设 `app.tenant_id` → 默认全拒 |
| 行级数据范围 | 用户能看见、操作哪些行 | 读：`@scope`（列表）与 `fn_check_visible`（单行）；写：`fn_can_*` | 缺 Scope → 生成代码报错；判定 false → 拒绝 |
| 功能权限码 | 用户能否调用某个操作 | 入口层（service / middleware）调 `fn_has_permission` | 未接入；接入后判定 false → 拒绝 |

- 租户边界由数据库兜底，应用层不重复检查。RLS 只承担租户这个绝对边界，不承担行级数据范围。
- 行级数据范围依赖目标行数据与用户权限关系，写侧判定必须在库内、与写入同一事务完成。
- 功能权限码与行无关，只依赖操作本身，在入口层判定，不进入仓储方法。
- 操作者与 Scope 只从服务端认证上下文注入（`IdentityTx.Auth()`）；请求体中的 `tenantId`、`owner*`、`scope` 一律不可信。

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

锁行 → 库内判定 → 写入，三步在同一 `IdentityTx` 内，判定不通过整体回滚：

```go
// internal/data/pg_sqlc/user.go 的 Update 形态
row, err := s.Q().GetSysUserForUpdate(ctx, id) // 1. FOR UPDATE 锁行，按当前归属取值
if err != nil {
    return err
}
if err := requireOperable(ctx, s, auth.UserID, &row); err != nil { // 2. fn_can_operate_row
    return err
}
updated, err := s.Q().UpdateSysUser(ctx, params) // 3. 写入，同一事务
```

约束：

- 判定使用行的当前归属，必须锁行取值，防止并发修改归属后按旧值判定；
- 判定与写入同事务，false 即回滚，不存在「判了没写」或「写了没判」；
- 可见性谓词只在判定函数里写一份，不内联进 `UPDATE` / `DELETE` 的 `WHERE`；
- 不在 biz / Go 里重算 `data_scope`，可见范围推导只在库内进行。

业务授权用专用函数：

| 操作 | 判定函数 |
|---|---|
| 修改 / 删除 | `fn_can_operate_row` |
| 修改归属 | `fn_can_transfer_owner`（旧归属可操作 + 新归属可达） |
| 分配角色 | `fn_can_assign_role` / `fn_can_assign_roles` |
| 其他业务授权 | 在 `operate.sql` 新增专用 `fn_can_*`，配命名查询并同步索引 |

## 读形态

| 场景 | 路径 |
|---|---|
| 列表 / 计数 | 动态查询协议；生成代码在 WHERE 头部注入 `@scope` 三维守卫，缺 Scope fail-closed；List 与 Count 共用守卫，`total` 与 `items` 权限视图一致 |
| 单行 / 详情 | `GetXxxByID` + `CheckVisible`（掩码 7）；不可见等同不存在 → NotFound |
| 外部批量过滤 | `fn_filter_visible_rows`；只读，拒绝表现为空集，不是错误 |

- 软删过滤由表注释 `@filter` 在解析期注入；固定命名查询在 SQL 里自写 `deleted_at IS NULL`。
- 不手写绕过 `@scope` 的聚合查询；专用列表走专用命名查询，守卫仍取自 `fn_accessible_*`。
- 单行读没有生成代码兜底，判定需在仓储手写，照 `userRepo.GetByID` 实现。

## 错误语义

| 情况 | 返回 | 理由 |
|---|---|---|
| 行不存在 / 数据范围不可见 | NotFound | 不泄露存在性；`requireOperable` 判定 false 返回 `pgx.ErrNoRows`，由仓储 `mapErr` 统一映射 |
| 看得见，但业务授权不允许（分配角色、转归属到不可达对象） | Forbidden（`ErrUserAccessDenied`） | 存在性不敏感，明确拒绝 |
| 只读判定的 false | 不是错误 | 调用方按布尔值处理 |

权限失败不用「0 行」隐式表达：0 行既可能是没权限，也可能是并发删除，仓储层无法区分，调用方也无从给出正确状态码。

## 功能权限码

功能权限码与数据范围正交：只看角色 → 菜单 → 权限码这条链，不管能看见哪些行。

落点：入口层（service 或 auth 中间件）按 `tr.Operation()` 映射权限码，调 `fn_has_permission` 判定，不通过返回 Forbidden。

- 与行无关，不进入仓储方法；
- 一次判定、可缓存；失效用 `sys_perm_version`（租户级 + 用户级版本）；
- 不与 `@scope` 合并成一层。

现状：`fn_has_permission` 与 `HasPermission` 命名查询已生成，Go 侧尚无调用点；接入前，已注册 RPC 只受令牌校验与行级判定约束。

## 新增资源 / 操作速查

1. **建表**：`tenant_id` + 审计列 + 归属三元组；表注释声明 `@scope`、`@filter: deleted_at is_null`、`@pk`、`@page`；`owner_*` 建索引；`rls.sql` 补策略并 ENABLE + FORCE。
2. **列表 / 计数**：复用 `query.v1.Query` 动态协议，Scope 由 `IdentityTx` 注入。
3. **详情**：`GetXxxByID` + `CheckVisible`（掩码 7），不可见返回 NotFound。
4. **修改 / 删除 / 改密**：锁行 + `fn_can_operate_row` + 写入；每个资源放一个 `requireOperable` 同款 helper。
5. **修改归属**：锁行 + `fn_can_transfer_owner`，新归属每个非空字段都要在可见范围内。
6. **批量写**：单事务内逐行锁 + 判，或新增批量判定函数；外部批量读过滤用 `fn_filter_visible_rows`。
7. **无归属三元组的表**：不声明 `@scope`，行级无范围；写侧靠功能权限码或数据库授权（`sys_menu` / `sys_config` 已对运行角色 REVOKE 写权限，由管理端写入）。
8. **文档化例外**：登录点查（`TenantTx`）、令牌校验/刷新（`TokenTx`）、介质维护（`SECURITY DEFINER` 清理）是仅有的绕过路径，不得扩展到其他读写。
9. **测试**：每个资源覆盖全租户看全集、范围角色看子集、范围外读 NotFound、范围外写不改行、转归属不可达 Forbidden、缺 Scope fail-closed、未设 `app.tenant_id` 全拒。
10. **约定**：有权限的领域方法默认按权限处理（Scope / RLS 强制生效），无需额外标注；确需无权限或超高权限行为时，方法签名带 `super` 字样显式区分。

## 索引

| 内容 | 位置 |
|---|---|
| 租户 RLS、`fn_accessible_*` 三件套 | [rls.sql](internal/data/pg_sqlc/sqlc/rls.sql) |
| 写前判断函数 `fn_can_*` | [operate.sql](internal/data/pg_sqlc/sqlc/operate.sql) |
| 只读判定、功能权限码、范围版本 | [expose.sql](internal/data/pg_sqlc/sqlc/expose.sql) |
| 判定函数的命名查询 | [queries/operate.sql](internal/data/pg_sqlc/sqlc/queries/operate.sql)、[queries/perm.sql](internal/data/pg_sqlc/sqlc/queries/perm.sql) |
| `@scope` 声明 | [schema/10_basic.sql](internal/data/pg_sqlc/sqlc/schema/10_basic.sql)、[schema/30_user.sql](internal/data/pg_sqlc/sqlc/schema/30_user.sql) |
| 会话门面、Scope 注入口 | [internal/data/pg_sqlc/session.go](internal/data/pg_sqlc/session.go) |
| 写路径范式（锁行 + 判定） | [internal/data/pg_sqlc/user.go](internal/data/pg_sqlc/user.go) |
| 生成的三维守卫 | [internal/data/pg_sqlc/gen/sys_user_dynamic_gen.go](internal/data/pg_sqlc/gen/sys_user_dynamic_gen.go) |
