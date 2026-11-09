# sqlc — SQL 对象索引

关系存储的声明资产目录：`schema/` 十三个域文件（表结构与注释 DSL）、
`rls.sql` / `operate.sql` / `expose.sql`、`96_agent_grant.sql`、
`98_*.sql` 函数声明文件与 `queries/`。本文件是这些对象里函数与命名查询的
索引：签名、属性与返回值约定。

三层权限模型见 [permission.md](../../../../permission.md)，部署执行顺序见
[deploy_db.md](../../../../deploy_db.md)。修改函数时同步 SQL 注释、
`COMMENT ON FUNCTION` 与本索引；修改命名查询时同步查询注释（sqlc 带入 Go
doc）与本索引。再生成：本目录 `sqlc generate -f sqlc.yaml` 或项目根
`make sqlc`，产物写入 `../gen/`（禁止手改）。

## 文件总览

表内 `#` 为 sqlc.yaml 装载序，「—」为不入 catalog 的文件；部署执行序以
`deploy.go` 的 `DeployFiles` 与 [deploy_db.md](../../../../deploy_db.md)
为准。

| # | 文件 | 作用 |
| --- | --- | --- |
| 1 | schema/10_basic.sql | basic 业务表：租户列、审计列、归属三元组与 `@scope` 列注释 |
| 2 | schema/20_tenant.sql | sys_tenant 租户表 |
| 3 | schema/30_user.sql | sys_user、用户-角色/用户组映射与用户组表 |
| 4 | schema/40_rbac.sql | 角色、范围明细、可分发部门、菜单与角色-菜单映射 |
| 5 | schema/50_dept.sql | 组织树、闭包表与闭包维护触发器 |
| 6 | schema/60_dict.sql | 字典类型与字典数据表 |
| 7 | schema/70_audit.sql | 登录日志与操作日志（append-only） |
| 8 | schema/80_session.sql | 会话/令牌表（仅存哈希）与过期清理函数 |
| 9 | schema/90_config.sql | 全局/租户配置、功能开关与翻译 |
| 10 | schema/91_knowledge.sql | kb_library 知识库与 kb_document 条目（租户表，RLS 在 rls.sql） |
| 11 | schema/92_model.sql | sys_model 模型目录（tenant_id='' 平台行 / 租户行双态） |
| 12 | schema/95_agent_audit.sql | Agent 工具调用与授权确认审计（append-only，含租户 RLS） |
| 13 | rls.sql | 租户 RLS 策略、`fn_accessible_*` 三件套与 updated_at 触发器 |
| 14 | expose.sql | 只读判定函数、范围版本表与 bump 触发器 |
| 15 | operate.sql | `fn_can_*` 写前判定与角色判定族 |
| 16 | 96_agent_grant.sql | Agent 工具授权表、未撤销唯一约束与租户 RLS |
| 17 | schema/97_agent_retrieval_log.sql | Agent 检索留痕表（append-only）与租户 RLS |
| 18 | queries/ | 写侧判定、固定 CRUD、权限查询与 Agent 审计/授权/留痕/知识库/模型读写（20 个文件、179 条查询） |
| 19 | 98_menu_write.sql | sys_menu 四个 SECURITY DEFINER 受控写函数 |
| 20 | 98_config_write.sql | sys_config 三个 SECURITY DEFINER 受控写函数 |
| 21 | 98_model_write.sql | sys_model 平台行建/改/软删三个 SECURITY DEFINER 受控写函数 |
| 22 | 98_tenant_guard.sql | 租户删除前置受控计数函数 |
| — | role.sql | grape_u / test_u 角色、schema 使用权、DML 授权与 RLS 会话变量写入权 |
| — | [seed/](seed/) | 生产默认种子：根租户自举数据，12 个 SQL 文件，顺序由 [seed.go](seed/seed.go) 声明 |
| — | test_seed.sql | 集成测试种子（`t-test-*` 租户），仅由 `TestDeployFiles` 在生产清单后追加 |
| — | agent_session.sql | 框架 session/postgres 的六张 `agent_*` 表（不入 catalog） |
| — | agent_vector.sql | pgvector 扩展与 `agent_document` 多空间向量表（不入 catalog） |

## schema DSL 与装载

`schema/` 是表结构与注释 DSL 的单一事实源：列级 `@dynamic` / `@internal` /
`@updatable`，表级 `@scope` / `@filter` / `@pk` / `@default_sort`。UI 元数据
（label / desc / order / visibility / options / api）与分页策略在 `sqlc.yaml`
的 `codegen.options.ui` / `codegen.options.page` 声明，注释里的人类文本不参与
生成。语法、类型映射与校验规则以上游
[dsl.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/dsl.md)、
[scope.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/scope.md)、
[schema.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/schema.md)
为准。

`sqlc.yaml` 的 schema 列表 = `schema/` 十三个域文件（10–90 九个域 +
91_knowledge + 92_model + 95_agent_audit + 97_agent_retrieval_log）+ `rls.sql` /
`operate.sql` / `expose.sql` + `96_agent_grant.sql` + `98_menu_write.sql` +
`98_config_write.sql` + `98_model_write.sql` + `98_tenant_guard.sql`；queries
列表 = `queries/` 二十个文件。函数声明文件只提供 queries 调用所需签名
（受控写、删除前置计数、权限判定），函数体是否存在由部署顺序保证。

- **向量 SQL 不进 catalog**：`agent_document` 的存取 SQL 是手写参数化常量
  （[../vector/vector_repo.go](../vector/vector_repo.go)），pgvector 的类型与
  相似度算子超出 sqlc 映射面。表无 RLS，租户隔离由应用层强制；RLS 语句附在
  `agent_vector.sql` 供按需启用。
- **向量索引探针**：每个声明维度一个表达式部分索引，
  `(embedding::vector(N)) vector_cosine_ops WHERE embed_dim = N`；检索 SQL
  重复同一 cast 才命中。pgvector 维度超过 2000 时 vector ops 不可索引。
  HNSW 参数（`vector_cosine_ops`、m / ef_construction）固化在 DDL，不进 conf。
- **会话框架表无 RLS**：`agent_*` 会话表不进 `@scope`/RLS，租户隔离靠
  userKey（`tenant_id:user_id`）应用层编码，属文档化「设施面」例外。
- **软删过滤收敛为 `@filter`**：带 `deleted_at` 的业务表声明
  `@filter: deleted_at is_null`，动态读面与写面附加该不变量；固定命名查询
  自写条件；回收站类需求走专用命名查询。
- **可变列声明 `@updatable: set`**：动态更新只收声明列，身份/类型/归属/结构/
  审计列不进白名单；`updated_at` 由 `trg_touch_updated_at` 兜底刷新。

## 函数索引

属性说明：S=STABLE（同一事务内重复调用结果一致）；默认 SECURITY INVOKER
（以调用者身份执行，受 RLS 约束）。除触发器与维护函数外，调用前会话必须已
设置 `app.tenant_id`。

### 权限过滤函数（rls.sql，查询用）

列表查询自动把返回值拼进 WHERE（`owner_xx = ANY(fn(...))`）。三个函数返回
值约定相同：

| 返回值 | 含义 |
| --- | --- |
| NULL | 有全租户角色（data_scope=7），不过滤 |
| `'{}'` | 用户无效或没有有效角色，什么都看不到 |
| 数组 | 能看到的 ID 列表，多个角色取并集 |

| 函数 | 签名 | 属性 | 返回什么 |
| --- | --- | --- | --- |
| fn_accessible_user_ids | (用户ID) → int8[] | S | 能看到哪些用户 |
| fn_accessible_dept_ids | (用户ID) → int8[] | S | 能看到哪些部门（"仅本人"角色也算上自己所在部门） |
| fn_accessible_group_ids | (用户ID) → int8[] | S | 能看到哪些用户组（只有"指定用户组"和"全租户"角色有结果） |

### 写前判断函数（operate.sql）

写数据前先调用，返回 true 才执行写入（同一事务，false 报错回滚）。

| 函数 | 签名 | 属性 | 判断什么 |
| --- | --- | --- | --- |
| fn_role_visibility_core | (操作者, 角色) → boolean | S | 角色可见性 A AND G 核心（读写面共用），不校验角色行状态 |
| fn_can_see_role | (操作者, 角色) → boolean | S | 操作人能否看见角色（与 `fn_visible_role_ids` 同源），不按启用状态过滤 |
| fn_visible_role_ids | (用户ID) → int8[] | S | sys_role @scope 守卫：逐角色调 `fn_can_see_role`；NULL=全租户不过滤 |
| fn_can_assign_role | (操作者, 角色, 目标用户) → boolean | S | 能否分配：可见性 + 目标部门范围（映射 ∩ subtree）+ `sys:user:assign-role` |
| fn_can_assign_roles | (操作者, 角色数组, 目标用户) → (role_id, ok) | S | 批量版，逐角色返回结论 |
| fn_can_revoke_role | (操作者, 角色, 目标用户) → boolean | S | 能否撤销：沿用可见性与目标范围，放宽角色/目标状态校验 |
| fn_can_operate_row | (用户, 归属人, 归属部门, 归属组) → boolean | S | 能否修改/删除：能看见就能操作；三归属全空只有全租户可操作 |
| fn_can_set_owner | (用户, 归属人, 归属部门, 归属组) → boolean | S | 新归属三元组每个非空归属是否可挂（存在、启用、可见） |
| fn_can_transfer_owner | (用户, 旧归属×3, 新归属×3) → boolean | S | 改归属：旧归属可操作且新归属可挂 |

### 查询判断函数（expose.sql，只读判定）

只读判定不写库，可与写事务共用同一事务与快照，与写前函数共用同一套
data_scope 推导。`fn_check_visible` / `fn_has_permission` 经命名查询被取行
判定与功能权限码判定消费；`fn_filter_visible_rows` 无命名查询消费方
（sqlc 解析不了表函数返回列，按需以参数化 SQL 调用）。拒绝返回 false/空集，
不是错误。

| 函数 | 签名 | 属性 | 判断什么 |
| --- | --- | --- | --- |
| fn_check_visible | (用户, 维度掩码, 归属人, 归属部门, 归属组) → boolean | S | 能否看见/操作一条数据（读写同权）；掩码 1 人、2 部门、4 组按位 OR |
| fn_filter_visible_rows | (用户, 维度掩码, 归属人[], 归属部门[], 归属组[]) → 行集 | S | 批量版：数组按下标对齐，返回可见子集并保持顺序 |
| fn_has_permission | (用户, 权限码) → boolean | S | 功能权限轴：有效角色是否关联携带该码的有效菜单 |

### 受控写函数（98_menu_write.sql，sys_menu 管理端）

sys_menu 对运行角色 REVOKE 直写，全部写入经本组 SECURITY DEFINER 函数。
DEFINER 以属主身份绕过 sys_menu 的 ENABLE+FORCE 租户 RLS 与直写禁令，函数内
以 `current_setting('app.tenant_id')` 复位租户边界（未设置即拒绝）；调用方
仍每事务设 `app.tenant_id`。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC：任何
角色可调用，但只能以函数内固定口径在自身租户内写入，不构成跨租户扩权面。
写路径经 `trg_bump_perm_version_tenant` 自动 bump 租户级权限版本。

| 函数 | 签名 | 作用 |
| --- | --- | --- |
| fn_menu_create | (id, status, name, permission_code, parent_id, menu_type, icon, route_path, component, permission_expr, sort_order, remark, 操作者) → sys_menu | 新建；父菜单须同租户存在未删除，拒绝自身为父；permission_code 租户内唯一由部分唯一索引兜底 |
| fn_menu_update | (id, status, name, icon, route_path, component, permission_expr, sort_order, remark, 操作者) → sys_menu | 覆盖可变字段；permission_code/menu_type/parent_id 不经本路径变更；未命中返回 NULL |
| fn_menu_move | (id, parent_id, 操作者) → sys_menu | 移动（NULL 为根）；新父级须同租户存在未删除，祖先链命中自身即拒绝（防环）；未命中返回 NULL |
| fn_menu_delete | (id, 操作者) → bigint | 软删；存在未删除直接子菜单时拒绝；返回删除行数（0=未命中） |

### 受控写函数（98_config_write.sql，sys_config 管理端）

sys_config 是平台级全局表（无 tenant_id、无 RLS），对运行角色 REVOKE 直写，
写入经本组 SECURITY DEFINER 函数。函数内以
`current_setting('app.tenant_id', true)` 校验认证事务纪律（未设置即拒绝）。
EXECUTE 沿用默认授权 PUBLIC。config_key（未删除行间唯一）与 value_type 是
身份/值语义列，创建后不变；更新只覆盖 name/status/value/remark。

| 函数 | 签名 | 作用 |
| --- | --- | --- |
| fn_config_create | (id, status, name, config_key, value, value_type, remark, 操作者) → sys_config | 新建；`app.tenant_id` 未设置即拒绝；config_key 唯一由部分唯一索引兜底 |
| fn_config_update | (id, status, name, value, remark, 操作者) → sys_config | 覆盖 name/status/value/remark；未命中返回 NULL |
| fn_config_delete | (id, 操作者) → bigint | 软删；返回删除行数（0=未命中） |

### 受控写函数（98_model_write.sql，sys_model 平台行）

sys_model 单表双态：`tenant_id=''` 为平台行（所有租户可读，RLS 写策略只放行
本租户行），非空为租户行。平台行只能经受控 SECURITY DEFINER 函数写入：函数
内固定 `tenant_id=''` 并校验 `app.tenant_id` 已设置。`kind`/`model_key` 是
身份列，创建后不变；`api_key_cipher` 只写不读（动态投影不返回该列）。

| 函数 | 签名 | 作用 |
| --- | --- | --- |
| fn_model_platform_create | (id, status, kind, name, model_key, provider, base_url, model, dimension, timeout_ms, api_key_cipher, api_key_env, remark, 操作者) → sys_model | 新建平台行；`app.tenant_id` 未设置即拒绝 |
| fn_model_platform_update | (id, status, name, provider, base_url, model, dimension, timeout_ms, api_key_cipher, api_key_env, remark, 操作者) → sys_model | 覆盖可变字段；仅命中 `tenant_id=''` 未软删行，未命中 NULL |
| fn_model_platform_delete | (id, 操作者) → bigint | 软删平台行；返回删除行数（0=未命中） |

### 受控判定函数（98_tenant_guard.sql，sys_tenant 删除前置）

租户树 RLS 下祖父看不到孙行，删除前置的「无未删除直接子租户」需要全局
事实。本函数以 DEFINER 绕过租户 RLS，只返回计数、不返回行数据，不构成跨
租户读面；函数内校验 `app.tenant_id` 已设置且目标对调用方可见，消除结构
侧信道。`sys_tenant.parent_id` 未建索引，计数按全表扫描。

| 函数 | 签名 | 作用 |
| --- | --- | --- |
| fn_tenant_active_child_count | (p_id varchar) → bigint | 返回指定租户名下未删除直接子租户数；供删除前置校验（>0 拒绝软删） |

### 触发器函数

| 函数 | 所在文件 | 用途 |
| --- | --- | --- |
| fn_maintain_dept_closure | schema/50_dept.sql | 维护部门闭包表：父级/防环校验；软删当删除、恢复当新增；有子部门不允许删 |
| fn_touch_updated_at | rls.sql | UPDATE 未显式改 updated_at 时自动刷新 |
| fn_bump_perm_version | expose.sql | 范围版本自增（租户级 user_id=0 或用户级） |
| fn_touch_perm_version_mapping | expose.sql | sys_user_role_mapping 增删改 → bump 用户级版本 |
| fn_touch_perm_version_tenant | expose.sql | 角色/菜单/组织/用户组/用户行增删改 → bump 租户级版本 |

挂载清单 = 判定函数的输入表清单，两处必须同步改。

### 维护函数（schema/80_session.sql）

由介质维护任务以池直连调用（不经租户/令牌事务门面），删除条件收在函数内，
调用方只传宽限期。sys_session 启用 ENABLE+FORCE RLS，应用角色直删恒 0 行；
本函数以属主身份绕过 RLS，是全仓唯一的跨租户写路径，EXECUTE 沿用默认授权
PUBLIC。

| 函数 | 签名 | 作用 |
| --- | --- | --- |
| fn_cleanup_expired_sessions | (宽限期秒数) → bigint | 清理已撤销或过期超过宽限期的会话，返回删除行数 |

## 命名查询索引（queries/）

约定：归属类参数用 `sqlc.narg` 传入，NULL 表示没有该归属；查询注释会被
sqlc 带进生成的 Go 代码（godoc），修改时同步本索引。

### operate.sql（写侧判定）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| CanSeeRole | :one | 操作者/角色 → bool | 能否看见指定角色（A AND G） |
| CanAssignRole | :one | 操作者/角色/目标 → bool | 能否把角色授予目标（可见性 + 目标范围 + 分配码） |
| CanAssignRoles | :many | 操作者/角色列表/目标 → (role_id, ok) | 逐角色判断能否授予 |
| CanRevokeRole | :one | 操作者/角色/目标 → bool | 能否撤销映射（状态放宽：禁用/软删也可撤） |
| CanRevokeRoles | :many | 操作者/角色列表/目标 → (role_id, ok) | 逐角色判断能否撤销 |
| CanTransferOwner | :one | 操作者 + 旧/新归属（可空）→ bool | 能否改归属（旧可操作且新归属可达） |
| CanOperateRow | :one | 操作者 + 归属三元组（可空）→ bool | 能否修改/删除（读写同权） |
| CanSetOwner | :one | 操作者 + 归属三元组（可空）→ bool | 能否挂新归属（存在、启用、可见） |
| AssignUserRole | :exec | id/用户/角色/操作者 | 写一条映射；应用层发号，活跃冲突跳过 |
| AssignUserRoles | :execrows | ids/用户/角色列表/操作者 → 新增条数 | 批量写映射，已拥有跳过 |
| HasUserRole | :one | 用户/角色 → bool | 是否有活跃映射（软删行排除） |

### assign_options.sql（分配选择器与角色整组替换）

关键字统一 `strpos(lower(col), lower(keyword)) > 0`，空串不过滤；分页
`limit+1` / `offset`，`total` 不计算。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| ListAssignableRoleOptions | :many | 操作者/目标/keyword/limit/offset → (id, name) | 可分配且未拥有的活跃角色（`fn_can_assign_role` 过滤） |
| ListUserRoleIDs | :many | 用户 → role_id[] | 全部活跃映射（含停用/软删角色残留），供 locked 差集 |
| ListActiveRoleOptionsByIDs | :many | role_id[] → (id, name) | 活跃角色标签与 desired 存在性校验 |
| DeleteUserRoles | :execrows | 用户/role_id[]/操作者 → 行数 | 批量软删撤销，只撤 `fn_can_revoke_role` 允许的角色 |
| CountRolesGrantingPermission | :one | role_id[]/权限码 → 角色数 | 携带该权限码的有效角色数（自锁保护） |
| ListOwnerUserOptions | :many | 操作者/keyword/limit/offset → (id, label) | 归属用户候选（`fn_accessible_user_ids` 三态过滤） |
| ListOwnerDeptOptions | :many | 操作者/keyword/limit/offset → (id, label) | 归属部门候选（`fn_accessible_dept_ids` 三态过滤） |
| ListOwnerGroupOptions | :many | 操作者/keyword/limit/offset → (id, label) | 归属用户组候选（`fn_accessible_group_ids` 三态过滤） |

`SetUserRoles` 的权威顺序：`LockSysUserByID` → `ListUserRoleIDs` →
`ListActiveRoleOptionsByIDs` → `CanAssignRoles` 分区 managed/locked →
拒绝非法 → 版本比对 → 自锁计数 → `DeleteUserRoles`（行数必须相等）→
`AssignUserRoles`。不变量与错误语义见
[permission.md](../../../../permission.md)。

### perm.sql（权限查询接口）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| CheckVisible | :one | 操作者/维度掩码/归属三元组（可空）→ bool | 按掩码判断归属是否可见/可操作 |
| HasPermission | :one | 操作者/权限码 → bool | 是否持有功能权限码（角色→菜单→权限码） |
| GetScope | :one | 操作者 → 三个 int8[]（可 NULL） | 一次取回三维可见集合（NULL=全租户） |
| GetUserRoles | :many | 用户 → SysRole 行 | 枚举有效角色（用户无效返回空集） |
| GetPermissionCodes | :many | 用户 → 权限码列表 | 去重排序的有效权限码 |
| GetScopeVersion | :one | 用户 → (租户版本, 用户版本) | 行不存在返回 0 |

### basic.sql（basic 业务表操作）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetBasicForUpdate | :one | id → Basic | 按 id 锁定读取（FOR UPDATE） |
| UpdateBasicOwner | :exec | 新归属（可空）+ 操作者 + id | 更新归属，NULL 清空 |

### sys_user.sql（用户固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysUserByID | :one | id → SysUser | 按 id 读未软删用户（不做范围过滤；分配与内部回读用） |
| GetSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 读数据范围内可见用户 |
| LockSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 锁定范围内可操作用户（FOR UPDATE） |
| LockSysUserByID | :one | id → SysUser | 按 id 锁定（不判可见性）：分配按 ID 串行化并发 |
| InsertSysUser | :one | 字段 + 操作者 → SysUser | 插入；租户取 `app.tenant_id` |
| UpdateSysUser | :one | 可变字段 + 操作者 + id → SysUser | 更新可变字段（不含 password 与归属） |
| SetSysUserPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 替换口令哈希 |
| SoftDeleteSysUser | :execrows | 操作者 + id → 行数 | 软删用户 |
| UpdateSysUserOwner | :exec | 新归属 + 操作者 + id | 更新归属三元组 |
| GetUserCredentialByName | :one | 用户名 → 凭据行 | 登录专用：读口令哈希与状态（仅未软删） |
| GetMyProfileByID | :one | id → SysUser | 个人中心：读本人行（不判行级归属） |
| UpdateMyProfile | :one | 资料字段 + 操作者 + id → SysUser | 个人中心：覆盖本人可编辑资料 |
| GetMyPasswordHash | :one | id → 口令哈希 | 个人中心：读本人口令哈希 |
| SetMyPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 个人中心：替换本人口令哈希 |

### sys_dept.sql（部门固定 CRUD）

闭包表由触发器在写事务内维护，本文件不写闭包查询；触发器的父级/防环/
有子部门拒绝是并发窗口的兜底防线。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysDeptByID | :one | id → SysDept | 读未软删部门（表无 @scope） |
| LockSysDeptByID | :one | id → SysDept | 锁定部门（FOR UPDATE） |
| ListSysDeptAll | :many | → 行集 | 租户内全部未软删部门（树装配与环检测） |
| CountSysDeptActiveChildren | :one | id → 行数 | 未软删直接子部门数（删除前置） |
| InsertSysDept | :one | 字段 + 操作者 → SysDept | 插入；闭包路径由触发器建立 |
| UpdateSysDept | :one | 可变字段 + 操作者 + id → SysDept | 覆盖可变字段（PUT 语义） |
| MoveSysDept | :one | 新父级（可空）+ 操作者 + id → SysDept | 移动部门（NULL 为根） |
| SoftDeleteSysDept | :execrows | 操作者 + id → 行数 | 软删；有未软删子部门时触发器拒绝 |

### sys_user_group.sql（用户组固定 CRUD 与成员映射）

用户组是租户内资源（表无 @scope）；成员映射随宿主维护，移除走物理删除；
组软删后映射物理残留，消费侧以活跃联结过滤。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysUserGroupByID | :one | id → SysUserGroup | 读未软删用户组 |
| LockSysUserGroupByID | :one | id → SysUserGroup | 锁定用户组（FOR UPDATE） |
| InsertSysUserGroup | :one | 字段 + 操作者 → SysUserGroup | 插入；code/name 唯一冲突映射 AlreadyExists |
| UpdateSysUserGroup | :one | name/status/remark + 操作者 + id → SysUserGroup | 更新可变字段（不含 code 与归属） |
| SoftDeleteSysUserGroup | :execrows | 操作者 + id → 行数 | 软删用户组（成员映射物理残留） |
| ListSysUserGroupMembers | :many | group_id/limit/offset → 行集 | 分页成员（活跃联结过滤已删用户） |
| CountSysUserGroupMembers | :one | group_id → 行数 | 与 List 同条件计数 |
| CountSysUserGroupMemberCandidates | :one | 用户 ID[] → 行数 | 候选成员存在性（命中数不等即整批拒绝） |
| AddSysUserGroupMembers | :execrows | group_id/用户 ID[]/操作者 → 新增条数 | 批量写映射，重复跳过（幂等） |
| RemoveSysUserGroupMembers | :execrows | group_id/用户 ID[] → 移除条数 | 批量物理删除（非成员 0 行，幂等） |

### sys_dict.sql（字典两级固定 CRUD）

字典是租户内公共资源（表无 @scope）；数据随宿主类型维护——创建先锁宿主
类型行，类型删除先统计未删除数据；唯一约束限定未删除行间，软删后可复用。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysDictTypeByID | :one | id → SysDictType | 读未软删字典类型 |
| LockSysDictTypeByID | :one | id → SysDictType | 锁定类型（FOR UPDATE） |
| InsertSysDictType | :one | 字段 + 操作者 → SysDictType | 插入；code 唯一冲突映射 AlreadyExists |
| UpdateSysDictType | :one | name/status/remark + 操作者 + id → SysDictType | 更新可变字段（不含 code） |
| CountSysDictDataByType | :one | dict_type_id → 行数 | 类型下未删除数据数（删除前置） |
| SoftDeleteSysDictType | :execrows | 操作者 + id → 行数 | 软删类型（活跃数据校验由调用方先做） |
| GetSysDictDataByID | :one | id → SysDictData | 读未软删字典数据 |
| LockSysDictDataByID | :one | id → SysDictData | 锁定数据（FOR UPDATE） |
| InsertSysDictData | :one | 字段 + 操作者 → SysDictData | 插入；同类型 value 唯一冲突映射 AlreadyExists |
| UpdateSysDictData | :one | label/status/sort_order/remark + 操作者 + id → SysDictData | 更新可变字段（不含 value/value_type/宿主） |
| SoftDeleteSysDictData | :execrows | 操作者 + id → 行数 | 软删数据 |

### sys_role.sql（角色固定 CRUD 与授权映射）

角色是租户内资源（RLS + `@scope` `fn_visible_role_ids` + 详情
`fn_can_see_role`，共用 A AND G）；范围明细、分发部门与角色-菜单映射随宿主
维护，设置走差量同步；引用对象须存在且启用；软删后映射物理残留，消费侧
活跃过滤。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysRoleByID | :one | id → SysRole | 读未软删角色（写路径与内部回读） |
| GetSysRoleVisibleByID | :one | id/操作者 → SysRole | 读可见角色（`fn_can_see_role`，详情专用） |
| LockSysRoleByID | :one | id → SysRole | 锁定角色（FOR UPDATE） |
| InsertSysRole | :one | 字段 + 操作者 → SysRole | 插入；code/name 唯一冲突映射 AlreadyExists |
| UpdateSysRole | :one | name/status/remark + 操作者 + id → SysRole | 更新可变字段（不含 code/role_type/数据范围） |
| UpdateSysRoleDataScope | :one | data_scope/scope_dept_id + 操作者 + id → SysRole | 更新数据范围标量（NULL=全租户可分配） |
| SoftDeleteSysRole | :execrows | 操作者 + id → 行数 | 软删角色（映射物理残留） |
| ListSysRoleIDScopes | :many | role_id → (type, data_id) | 范围明细活跃行 |
| ListSysRoleAllowedDepts | :many | role_id → dept_id | 分发部门活跃行 |
| ListSysRoleMenus | :many | role_id → menu_id | 有效授权菜单（活跃过滤，与 `fn_has_permission` 同口径） |
| ListSysRoleMenuGrantsAll | :many | role_id → menu_id | 映射物理行（不过滤活跃性，供差量对账） |
| CountSysRoleScopeUserRefs | :one | 用户 ID[] → 行数 | 引用中启用未删除用户数（明细 type=1） |
| CountSysRoleScopeDeptRefs | :one | 部门 ID[] → 行数 | 引用中启用未删除部门数（type=2 与分发部门/scope_dept_id） |
| CountSysRoleScopeGroupRefs | :one | 用户组 ID[] → 行数 | 引用中启用未删除用户组数（type=3） |
| CountSysRoleMenuRefs | :one | 菜单 ID[] → 行数 | 引用中启用未删除菜单数 |
| DeleteSysRoleIDScope | :execrows | role_id/type/data_id → 行数 | 移除范围明细（物理删除） |
| AddSysRoleIDScope | :execrows | id/role_id/type/data_id + 操作者 → 行数 | 补建范围明细 |
| DeleteSysRoleAllowedDept | :execrows | role_id/dept_id → 行数 | 移除分发部门（物理删除） |
| AddSysRoleAllowedDept | :execrows | id/role_id/dept_id + 操作者 → 行数 | 补建分发部门 |
| DeleteSysRoleMenu | :execrows | role_id/menu_id → 行数 | 移除角色-菜单映射（物理删除） |
| AddSysRoleMenu | :execrows | role_id/menu_id + 操作者 → 行数 | 补建授权；主键冲突跳过（保留原授权人） |

### menu.sql（菜单读查询与受控写调用）

sys_menu 读面为租户内普通读（表无 @scope）；写面经 98_menu_write.sql 受控
函数，固定口径收在函数内。树查询走专用列表（全量装配，不分页）。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysMenuByID | :one | id → SysMenu | 读未软删菜单 |
| ListSysMenuAll | :many | → 行集 | 租户内全部未软删菜单（树装配与环检测） |
| CountSysMenuActiveChildren | :one | id → 行数 | 未软删直接子菜单数（删除前置） |
| InsertSysMenu | :one | 字段 + 操作者 → SysMenu | 调 `fn_menu_create`；permission_code 冲突映射 AlreadyExists |
| UpdateSysMenu | :one | 可变字段 + 操作者 + id → SysMenu | 调 `fn_menu_update`；未命中过滤成空集（NotFound） |
| MoveSysMenu | :one | 新父级（可空）+ 操作者 + id → SysMenu | 调 `fn_menu_move`；未命中同上 |
| SoftDeleteSysMenu | :one | 操作者 + id → 删除行数 | 调 `fn_menu_delete`；0 行即未命中 |

### sys_config.sql（配置族固定 CRUD）

配置族是「全局默认 + 租户覆盖」：sys_config 是平台级全局表，写面经受控
函数；其余三张租户表读写为普通 DML（`tenant_id` 为 `@internal`，动态协议
不接受 `tenantId`）。身份键唯一约束限定未删除行间；`value_type` 白名单与
status 校验收在 biz 用例。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysConfigByID | :one | id → SysConfig | 读未软删全局配置（无 RLS） |
| InsertSysConfig | :one | 字段 + 操作者 → SysConfig | 调 `fn_config_create`；config_key 冲突映射 AlreadyExists |
| UpdateSysConfig | :one | 可变字段 + 操作者 + id → SysConfig | 调 `fn_config_update`；未命中过滤成空集 |
| SoftDeleteSysConfig | :one | 操作者 + id → 删除行数 | 调 `fn_config_delete`；0 行即未命中 |
| GetSysTenantConfigByID | :one | id → SysTenantConfig | 读未软删租户配置 |
| InsertSysTenantConfig | :one | 字段 + 操作者 → SysTenantConfig | 插入；租户取会话，config_key 冲突映射 AlreadyExists |
| UpdateSysTenantConfig | :one | name/status/value/remark + 操作者 + id → SysTenantConfig | 更新可变字段 |
| SoftDeleteSysTenantConfig | :execrows | 操作者 + id → 行数 | 软删租户配置 |
| GetSysFeatureFlagByID | :one | id → SysFeatureFlag | 读未软删功能开关 |
| InsertSysFeatureFlag | :one | 字段 + 操作者 → SysFeatureFlag | 插入；flag_key 冲突映射 AlreadyExists |
| UpdateSysFeatureFlag | :one | name/status/description/is_enabled/remark + 操作者 + id → SysFeatureFlag | 更新可变字段 |
| SoftDeleteSysFeatureFlag | :execrows | 操作者 + id → 行数 | 软删功能开关 |
| GetSysTranslationByID | :one | id → SysTranslation | 读未软删翻译 |
| InsertSysTranslation | :one | 字段 + 操作者 → SysTranslation | 插入；身份四元组唯一冲突映射 AlreadyExists |
| UpdateSysTranslation | :one | status/value/remark + 操作者 + id → SysTranslation | 更新可变字段 |
| SoftDeleteSysTranslation | :execrows | 操作者 + id → 行数 | 软删翻译 |
| GetSysConfigValueByKey | :one | config_key → *string | 读启用的全局配置值（模型/嵌入选键；无行返回 pgx.ErrNoRows） |
| GetSysTenantConfigValueByKey | :one | config_key → *string | 读启用的租户配置值（租户边界由 RLS 承担） |

### kb_library.sql（知识库固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetKbLibraryByID | :one | id → KbLibrary | 读未软删知识库 |
| LockKbLibraryByID | :one | id → KbLibrary | 锁定知识库（FOR UPDATE） |
| InsertKbLibrary | :one | 字段 + 操作者 → KbLibrary | 插入（含切分区分串）；租户取会话，名称冲突映射 AlreadyExists |
| UpdateKbLibrary | :one | 可变字段 + 操作者 + id → KbLibrary | 更新可变字段（含切分区分串） |
| CountKbDocumentsByLibrary | :one | library_id → 行数 | 库下未删除条目数（删除前置） |
| SoftDeleteKbLibrary | :execrows | 操作者 + id → 行数 | 软删知识库 |

### kb_document.sql（知识条目固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetKbDocumentByID | :one | id → KbDocument | 读未软删条目 |
| LockKbDocumentByID | :one | id → KbDocument | 锁定条目（FOR UPDATE） |
| InsertKbDocument | :one | 字段 + 操作者 → KbDocument | 插入；租户取会话 |
| UpdateKbDocument | :one | 可变字段 + 操作者 + id → KbDocument | 更新可变字段（library_id/kind 不变） |
| UpdateKbDocumentStatus | :one | status/valid_from/valid_to + 操作者 + id → KbDocument | 只改状态与有效期（不重嵌） |
| ListKbDocumentsByLibrary | :many | library_id/limit/offset → 行集 | 库内条目固定分页（updated_at 倒序） |
| CountKbDocumentsByLibraryList | :one | library_id → 行数 | 与 List 同条件计数 |
| SoftDeleteKbDocument | :execrows | 操作者 + id → 行数 | 软删条目 |
| MarkKbDocumentIndexed | :one | indexed_embedder + 操作者 + id → KbDocument | 回写最近一次成功摄取时间与空间键 |

### sys_model.sql（模型目录固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysModelForTenantByKey | :one | model_key → SysModel | 读本租户行（含停用；由调用方 fail-closed） |
| GetSysModelPlatformByKey | :one | model_key → SysModel | 读平台行（含停用） |
| GetSysModelByID | :one | id → SysModel | 读本租户行或平台行 |
| LockSysModelTenantByID | :one | id → SysModel | 锁定本租户行（平台行不命中） |
| InsertSysModelTenant | :one | 字段 + 操作者 → SysModel | 插入租户行；租户取会话 |
| UpdateSysModelTenant | :one | 可变字段 + 操作者 + id → SysModel | 更新租户行（kind/model_key 不变） |
| SoftDeleteSysModelTenant | :execrows | 操作者 + id → 行数 | 软删租户行 |
| InsertSysModelPlatform | :one | 字段 + 操作者 → SysModel | 调 `fn_model_platform_create` |
| UpdateSysModelPlatform | :one | 可变字段 + 操作者 + id → SysModel | 调 `fn_model_platform_update`；未命中空集 |
| SoftDeleteSysModelPlatform | :one | 操作者 + id → 删除行数 | 调 `fn_model_platform_delete`；0 行即未命中 |
| ListSysModelAll | :many | 可空 kind → 行集 | 可见模型（本租户行 + 平台行，含停用） |
| ListSysModelOptions | :many | kind → 行集 | 可见且启用，每 model_key 租户行覆盖平台行 |

### sys_tenant.sql（租户固定 CRUD）

租户树模型：每行归属其父租户（`tenant_id`），RLS 允许命中
`tenant_id = app.tenant_id` 或 `id = app.tenant_id`，树外不可见。创建时
`parent_id` 只允许省略或等于调用方租户。表无 @scope；写路径是普通 DML，
API 的 `super` 命名只表达平台级实体管理，不表示绕过 RLS。删除前置计数经
`fn_tenant_active_child_count`。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysTenantByID | :one | id → SysTenant | 读未软删租户（树外等同不存在） |
| LockSysTenantByID | :one | id → SysTenant | 锁定租户（FOR UPDATE） |
| CountSysTenantActiveChildren | :one | id → 行数 | 经受控函数统计未删除直接子租户（>0 拒绝软删） |
| InsertSysTenant | :one | 字段 + 操作者 → SysTenant | 插入；`tenant_id` 取会话；code/name 冲突映射 AlreadyExists |
| UpdateSysTenant | :one | 可变字段 + 操作者 + id → SysTenant | 覆盖可变字段（不含 code 与身份/结构/归属列） |
| SetSysTenantStatus | :one | status/操作者 + id → SysTenant | 置位目标状态（停用 0 / 恢复 1，幂等） |
| SoftDeleteSysTenant | :execrows | 操作者 + id → 行数 | 软删；活跃子租户校验由调用方先做 |

### sys_audit_log.sql（审计日志追加与点查）

登录日志与操作日志均 append-only，只有 INSERT 与按 id 点查。登录日志由登录
用例在身份成立前经 `TenantTx` 追加（审计列显式给出）；操作日志由入口审计
中间件在身份成立后经 `IdentityTx` 追加。两表无 @scope，租户边界由 RLS 承担。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| GetSysLoginLogByID | :one | id → SysLoginLog | 按 id 点查登录日志 |
| InsertSysLoginLog | :exec | 用户/用户名/租户/来源/方式/结果/原因 → — | 追加登录日志；user_id/created_by 允许 0 |
| GetSysOperationLogByID | :one | id → SysOperationLog | 按 id 点查操作日志（含前后快照） |
| InsertSysOperationLog | :exec | 用户/用户名/租户/操作/目标/快照/请求事实/结果 → — | 追加操作日志；未采集项落 NULL |

### sys_session.sql（会话固定 CRUD）

管理面动态 List 由 schema 注释 DSL 声明（令牌哈希列 `@internal` 不可查，
REINDEX 目标即其唯一索引 `sys_session_token_hash_un`）；本文件只写认证生命
周期与介质维护查询。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| InsertSysSession | :one | 身份 + 令牌哈希/有效期 → SysSession | 插入；租户取会话，有效期按 TTL 从库时钟起算 |
| GetActiveSysSessionByTokenHash | :one | 访问令牌哈希 → SysSession | 读启用、未撤销、未过期会话；无行即无效 |
| RevokeSysSession | :execrows | 操作者 + id → 行数 | 撤销指定会话；已撤销更新 0 行 |
| RevokeSysSessionByTokenHash | :execrows | 访问令牌哈希 → 行数 | 登出：按令牌撤销；幂等。经 `sys_session_token_revoke` 策略走令牌维度事务 |
| RotateSysSession | :one | 当前刷新令牌哈希 + 新令牌哈希/有效期 → SysSession | 轮换刷新令牌；仅命中有效会话 |
| CleanupExpiredSysSessions | :one | 宽限期秒数 → 删除行数 | 删除已撤销或过期超宽限期的会话 |

### agent_audit.sql（Agent 业务审计读写）

两表 append-only，读写都要求会话已设 `app.tenant_id`。租户边界双重声明：
RLS 为主，读侧 WHERE 显式带同源租户谓词作应用层兜底（RLS 关闭时仍不越租户）。

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| InsertAgentToolCallLog | :one | 身份 + 工具调用字段 → AgentToolCallLog | 插入；`tenant_id+tool_call_id` 唯一，重复报冲突 |
| InsertAgentConfirmationLog | :one | 身份 + 确认字段 → AgentConfirmationLog | 插入（仅终态决定）；`tenant_id+confirmation_id` 唯一 |
| ListAgentToolCallLogs | :many | 可空 session_id + limit/offset → 行集 | 按会话分页（时间倒序，同秒 id tie-break） |
| CountAgentToolCallLogs | :one | 可空 session_id → 行数 | 与 List 同条件计数 |
| ListAgentConfirmationLogs | :many | 可空 session_id + limit/offset → 行集 | 按会话分页 |
| CountAgentConfirmationLogs | :one | 可空 session_id → 行数 | 与 List 同条件计数 |
| GetAgentToolCallLogByToolCallID | :one | tool_call_id → AgentToolCallLog | 按调用 ID 点查；无行即不存在或跨租户 |
| GetAgentConfirmationLogByConfirmationID | :one | confirmation_id → AgentConfirmationLog | 按确认 ID 点查；无行即不存在或跨租户 |

### agent_grant.sql（Agent 工具授权固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| InsertAgentToolGrant | :execrows | 授权字段（session_id 可空=全局级）→ 行数 | 写入；同键活跃许可经部分唯一索引幂等跳过（0 行=已存在） |
| GetActiveAgentToolGrantForSession | :one | 租户/用户/工具/会话 → AgentToolGrant | 会话级活跃许可点查（评估第一优先） |
| GetActiveAgentToolGrantGlobal | :one | 租户/用户/工具 → AgentToolGrant | 全局级活跃许可点查（兜底） |
| GetAgentToolGrantByID | :one | id/租户 → AgentToolGrant | 按租户内 id 取行（含已吊销，供归属判定） |
| ListActiveAgentToolGrantsByUser | :many | 租户/用户 → 行集 | 本人活跃授权列表 |
| ListActiveAgentToolGrantsByTenant | :many | 租户 → 行集 | 租户内全部活跃授权（管理视图，入口验 `agent:grant:query`） |
| RevokeAgentToolGrantSelf | :execrows | id/租户/归属人/吊销人 → 行数 | 属主吊销：id + 归属人双定位；非本人/已吊销 0 行 |
| RevokeAgentToolGrantManaged | :execrows | id/租户/吊销人 → 行数 | 管理吊销（入口验 `agent:grant:revoke`） |

两个 `GetActive*` 的「无行」是正常控制流：仓储映射为 `(nil, nil)`，会话级
先于全局级由 `biz.GrantUsecase.Resolve` 编排。`tenant_id` 在全部查询显式
过滤，作 RLS 之外的应用层兜底。

### agent_retrieval_log.sql（Agent 检索留痕读写）

| 查询 | 类型 | 参数 → 返回 | 作用 |
| --- | --- | --- | --- |
| InsertAgentRetrievalLog | :one | 留痕字段（可空 narg）→ AgentRetrievalLog | 写入；租户取会话，写入者与用户同值 |
| ListAgentRetrievalLogs | :many | 会话/来源（可空）/limit/offset → 行集 | 按会话/来源分页（时间倒序，供回放） |
| CountAgentRetrievalLogs | :one | 会话/来源（可空）→ int64 | 与 List 同条件计数 |

租户边界与审计同型：RLS 为主，读侧 WHERE 的同源租户谓词兜底；写入侧租户
一律取会话变量，调用方无法指定。

注意：命名查询生成的是 `*Queries` 上的方法（`gen.New(db)` 实例化后调用）；
动态查询生成的是顶层函数（`ParseXxx` / `ListXxx` / `UpdateXxx`，db 作参数
显式传）。两种风格不要混淆。

## RLS 策略

租户表策略统一为 `tenant_id = current_setting('app.tenant_id')`，读写都检查
且表主不豁免（FORCE）；会话未设置变量时什么都读写不到。策略随域文件走
（基线在 `rls.sql`、范围版本在 `expose.sql`、Agent 审计在
`schema/95_agent_audit.sql`、工具授权在 `96_agent_grant.sql`、检索留痕在
`schema/97_agent_retrieval_log.sql`）。`sys_tenant` 额外允许命中自身 `id`；
`sys_config` 无租户列，不设 RLS。

`kb_library` / `kb_document` 为普通租户表（ENABLE + FORCE，单一策略）。
`sys_model` 是双态表：读策略 `sys_model_read` 放行「平台行（tenant_id=''）+ 本
租户行」，写策略 `sys_model_write` 只放行本租户行；平台行只能经
`98_model_write.sql` 的 SECURITY DEFINER 函数（属主绕过 RLS）写入。

`sys_session` 另有三条令牌维度策略（`sys_session_token_read` /
`sys_session_token_rotate` / `sys_session_token_revoke`），服务会话校验、
刷新与登出：这三个动作发生在租户身份成立之前，令牌哈希（访问或刷新令牌）
是全局唯一的秘密凭据，持有即授权对命中那一行的读取、轮换或撤销。哈希以
事务级 GUC `app.session_token_hash` / `app.session_refresh_token_hash` /
`app.session_new_refresh_token_hash` 声明；未设时 `current_setting(..., true)`
为 NULL，比较恒为假，不额外放行。校验/登出/刷新事务不设 `app.tenant_id`，
除命中行外所有租户数据仍默认全拒。`token_revoke` 的 WITH CHECK 与 USING
同为 token_hash 匹配，固定撤销不得改动该列（防止借撤销路径偷换会话凭据）。
