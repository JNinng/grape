# sqlc — 数据权限体系 SQL 对象参考

grape 关系存储的 SQL 载体，移植自数据权限体系模板：schema 按业务域拆分、
RLS 租户隔离、读侧权限过滤、写前判断、权限查询判定、种子数据一应俱全。
本文件是这些文件里**函数和命名查询的索引**：每个对象的签名、用途和返回值
约定。

修改函数时同步更新三处：SQL 里的注释、`COMMENT ON FUNCTION`、本文件的
索引表；修改命名查询时同步查询注释（sqlc 带入 Go doc）与索引表。

## 文件总览

| # | 文件 | 作用描述 | sqlc 输入 | 内容 |
|---|---|---|---|---|
| 1 | schema/10_basic.sql | 定义 basic 业务表：租户列、审计列、归属三元组与 @scope 列注释 | 是 | 1 张表 |
| 2 | schema/20_tenant.sql | 定义 sys_tenant 租户表 | 是 | 1 张表 |
| 3 | schema/30_user.sql | 定义 sys_user 用户表、用户-角色/用户组映射与用户组表；sys_user 带 @scope 列注释 | 是 | 4 张表 |
| 4 | schema/40_rbac.sql | 定义角色、data_scope 范围明细、角色可分发部门、菜单与角色-菜单授权表 | 是 | 5 张表 |
| 5 | schema/50_dept.sql | 定义组织树与闭包表，以及闭包维护触发器函数 | 是 | 2 张表、1 个触发器函数 |
| 6 | schema/60_dict.sql | 定义字典类型与字典数据表 | 是 | 2 张表 |
| 7 | schema/70_audit.sql | 定义登录日志与操作日志表（append-only） | 是 | 2 张表 |
| 8 | schema/80_session.sql | 定义会话/令牌表（仅存哈希），以及过期会话清理函数 | 是 | 1 张表、1 个维护函数 |
| 9 | schema/90_config.sql | 定义全局配置、租户配置、功能开关与翻译表 | 是 | 4 张表 |
| 10 | rls.sql | 定义租户隔离 RLS 策略、fn_accessible_* 权限过滤函数、updated_at 触发器、会话令牌维度策略与运行角色授权 | 是（只读函数签名） | 23 条策略（含 sys_session 令牌维度 2 条）、4 个函数 |
| 11 | operate.sql | 定义 fn_can_* 写前判断函数 | 是（只读函数签名） | 4 个判断函数 |
| 12 | expose.sql | 定义只读权限查询判定函数、范围版本表与 bump 触发器 | 是（只读函数签名） | 3 个查询判断函数、1 张版本表、3 个触发器函数 |
| 13 | queries/ | 定义写侧判定、basic 操作、权限查询与业务表固定 CRUD 的命名查询 | 是（queries） | 5 个文件、28 条命名查询 |
| 14 | seed.sql | 填充种子数据（含多租户权限样例） | 否 | 租户/部门/角色/用户/用户组/字典/菜单/配置/开关/翻译/日志 + basic 归属矩阵 |

- schema 目录是表结构与注释 DSL（@dynamic/@scope/@filter/@page 等）的单一事实源；
  列级/表级指令的语法、类型映射与校验规则以上游参考为准：
  [dsl.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/dsl.md)（指令总表）、
  [scope.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/scope.md)（行级数据权限）、
  [schema.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/schema.md)（前端 Schema 契约）。
  `sqlc.yaml` 的 schema 列表 = schema/ 九个文件按序 + rls/operate/expose，
  queries 列表 = queries/ 五个文件（operate/basic/perm 与业务表固定 CRUD
  sys_user/sys_session）。加 rls/operate/expose 是为了让
  queries 里的查询能调用这些文件里的函数（GetScope 调三件套、判断类调
  fn_can_* / fn_check_visible）；sqlc 只看签名，函数体是否存在于库里由
  执行顺序保证。业务表的固定 CRUD 一资源一文件（queries/<资源>.sql），
  随纵切补入清单。
- 数据库部署按固定顺序执行 13 个文件：`schema/10_basic.sql` →
  `schema/20_tenant.sql` → `schema/30_user.sql` → `schema/40_rbac.sql` →
  `schema/50_dept.sql` → `schema/60_dict.sql` → `schema/70_audit.sql` →
  `schema/80_session.sql` → `schema/90_config.sql` → `rls.sql` →
  `operate.sql` → `expose.sql` → `seed.sql`。`seed.sql` 必须在 RLS 启用后
  执行，且以超级用户/表 owner 连接（超级用户绕过 RLS）。执行顺序的依赖
  细节与升级注意事项见仓库根 [deploy_db.md](../../../../deploy_db.md)。
- 再生成：在本目录执行 `sqlc generate -f sqlc.yaml`（或项目根
  `make sqlc`），产物写入 `../gen/`（禁止手改）。

## 三层权限模型（速查）

| 层 | 维度 | 执行者 | 失效姿态 |
|---|---|---|---|
| PostgreSQL RLS | 租户隔离（绝对边界） | 21 张租户表策略，`tenant_id = current_setting('app.tenant_id')`，ENABLE + FORCE；sys_session 另加 2 条令牌维度策略 | 漏设变量 → 默认全拒 |
| `@scope` 守卫 | 数据范围（data_scope 1-7，多角色并集） | 生成代码 WHERE 头部注入守卫谓词 | 缺 Scope → fail-closed 报错 |
| 判断函数（写侧） | 操作权限 | `operate.sql` 布尔判定 + 同事务先检后写 | 判定 false → 报错回滚 |

- **@scope 只挂在有归属字段的表上**：当前为 `basic` 与 `sys_user`
  （见 schema 文件表注释）；其余表租户内所有人可见。
- **软删过滤收敛为 @filter**：所有带 `deleted_at` 的业务表在表注释声明
  `@filter: deleted_at is_null`，动态查询（List/Count）在解析期附加该
  服务端固定不变量，协议不暴露 `deletedAt` 条件；固定命名查询的软删
  条件仍写在 SQL 里。回收站类需求走专用命名查询，不占动态协议。
- **权限过滤函数只收一个用户 ID**：该用户能看到什么由函数内部查角色
  算出，撤销角色下一次查询立刻生效。
- **运行连接三条纪律**：普通数据库角色（勿用超级用户，会绕过 RLS）；
  业务事务每事务 `set_config('app.tenant_id', ..., true)`（身份成立前的
  令牌事务与介质维护是文档化例外）；Scope 只从服务端认证上下文注入，
  永不受前端请求影响。

某类约束该落在哪一层、写路径为什么是「锁行 → 库内判定 → 写入」、新增
资源照哪条路径实现，见根目录 [permission.md](../../../../permission.md)。

## 函数索引

属性说明：S=STABLE（同一事务内重复调用结果一致）；默认 SECURITY
INVOKER（以调用者身份执行，受 RLS 约束）。除触发器函数与维护函数
（见「维护函数」一节）外，调用前会话必须已设置 `app.tenant_id`。

### 权限过滤函数（rls.sql，查询用）

列表查询自动把返回值拼进 WHERE（`owner_xx = ANY(fn(...))`）。
三个函数返回值约定相同：

| 返回值 | 含义 |
|---|---|
| NULL | 有全租户角色（data_scope=7），不过滤 |
| '{}' | 用户无效或没有有效角色，什么都看不到 |
| 数组 | 能看到的 ID 列表，多个角色取并集 |

| 函数 | 签名 | 属性 | 返回什么 |
|---|---|---|---|
| fn_accessible_user_ids | (用户ID) → int8[] | S | 能看到哪些用户 |
| fn_accessible_dept_ids | (用户ID) → int8[] | S | 能看到哪些部门（"仅本人"角色也算上自己所在部门，保证自己的数据不被过滤掉） |
| fn_accessible_group_ids | (用户ID) → int8[] | S | 能看到哪些用户组（只有"指定用户组"和"全租户"两类角色有结果） |

### 写前判断函数（operate.sql）

写数据前先调用，返回 true 才执行写入（同一个事务，false 就报错回滚）。

| 函数 | 签名 | 属性 | 判断什么 |
|---|---|---|---|
| fn_can_assign_role | (操作者, 角色, 目标用户) → boolean | S | 能否把角色分配给用户。角色配了分发部门时：目标部门须在配置内，且操作者部门命中配置、或能看见目标用户；没配时按 scope_dept_id 子树 + 能看见目标用户判断 |
| fn_can_assign_roles | (操作者, 角色数组, 目标用户) → (role_id, ok) 行集 | S | 上面那条的批量版：对数组里每个角色独立判断，逐个返回结论；全部通过才批量写入 |
| fn_can_operate_row | (用户, 归属人, 归属部门, 归属组) → boolean | S | 能否修改/删除一条数据。能看见就能操作：有全租户角色直接通过；否则任一归属命中可见范围即通过；三个归属全空的数据只有全租户能操作 |
| fn_can_transfer_owner | (用户, 旧归属×3, 新归属×3) → boolean | S | 能否修改数据的归属。两头都要过：这条数据本身有权操作，且新归属的人/部门/组在可见范围内（防止把数据转给自己看不见的人） |

### 查询判断函数（expose.sql，外部查询用）

只读判定，不与写事务绑定；与上面两个家族共用同一套 data_scope
推导（内外同源）。供权限查询服务层（宿主侧，待建）调用，HTTP/gRPC
由宿主自包。拒绝返回 false/空集，不是错误。

| 函数 | 签名 | 属性 | 判断什么 |
|---|---|---|---|
| fn_check_visible | (用户, 维度掩码, 归属人, 归属部门, 归属组) → boolean | S | 能否看见/操作一条数据（读写同权）。维度掩码：1 人、2 部门、4 组，按位组合 OR 并集；等价于 fn_can_operate_row（掩码 7）。未选中 1/2/4 中任何位时恒 false，其余位忽略 |
| fn_filter_visible_rows | (用户, 维度掩码, 归属人数组, 归属部门数组, 归属组数组) → 归属三元组行集 | S | 上面那条的批量版：三个数组按下标对齐，只返回可见子集（保持传入顺序）。数组元素可为 NULL（无该归属） |
| fn_has_permission | (用户, 权限码) → boolean | S | 功能权限轴：用户的有效角色是否关联了携带该权限码的有效菜单（角色→菜单→权限码，与数据范围正交） |

### 触发器函数

| 函数 | 所在文件 | 用途 |
|---|---|---|
| fn_maintain_dept_closure | schema/50_dept.sql | 部门增删改时自动维护闭包表：校验父部门、防环；软删当删除处理、恢复当新增处理；还有子部门时不允许删 |
| fn_touch_updated_at | rls.sql | UPDATE 时如果应用层没改 updated_at，就自动刷新它（批量挂在所有有 updated_at 列的表上） |
| fn_bump_perm_version | expose.sql | 范围版本自增：租户级行（user_id=0）或用户级行，不存在从 1 起建 |
| fn_touch_perm_version_mapping | expose.sql | sys_user_role_mapping 增删改 → bump 该用户版本（用户级失效的唯一来源） |
| fn_touch_perm_version_tenant | expose.sql | 角色/菜单/组织侧/用户组/用户行增删改 → bump 租户级行（挂在 sys_user、sys_role、sys_role_id_scope、sys_role_menu、sys_menu、sys_dept、sys_user_group、sys_user_group_mapping 上） |

挂载清单 = 判定函数的输入表清单，两处必须同步改。

### 维护函数（schema/80_session.sql，介质维护面）

由介质侧维护任务以池直连调用（不经租户/令牌事务门面），删除条件
（过期宽限、已撤销）收在函数内，调用方只传宽限期。

| 函数 | 签名 | 属性 | 作用描述 |
|---|---|---|---|
| fn_cleanup_expired_sessions | (宽限期秒数) → bigint | SECURITY DEFINER，`SET search_path = public` | 清理已撤销或过期超过宽限期的会话行，返回删除行数 |

属主/权限语义：sys_session 启用 ENABLE+FORCE 的租户 RLS，维护任务
没有租户身份，应用角色直删恒 0 行；本函数以属主（部署超级用户）身份
执行绕过 RLS，是全仓唯一的跨租户写路径。EXECUTE 沿用 PostgreSQL
默认授权 PUBLIC（与 rls.sql 其他函数一致，无显式 GRANT）：任何角色
都能调用，但只能以函数内固定的清理条件执行，不构成跨租户扩权面。

## 命名查询索引（queries/）

约定：归属类参数用 sqlc.narg 传入，NULL 表示没有该归属；查询注释会被
sqlc 带进生成的 Go 代码（godoc），修改时同步本索引。

### operate.sql（写侧判定）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| CanAssignRole | :one | 操作者/角色/目标 → bool | 判断操作者能否把指定角色授予目标用户 |
| CanAssignRoles | :many | 操作者/角色列表/目标 → (role_id, ok) 行 | 逐角色判断角色列表能否授予目标用户，每行返回 (role_id, ok) |
| CanTransferOwner | :one | 操作者 + 旧/新归属（可空）→ bool | 判断操作者能否把数据的归属从旧三元组改为新三元组（旧数据可操作且新归属可达） |
| CanOperateRow | :one | 操作者 + 归属三元组（可空）→ bool | 判断操作者能否修改/删除指定归属的数据（读写同权） |
| AssignUserRole | :exec | 用户/角色/操作者 | 写入一条用户-角色映射；租户取会话 app.tenant_id，重复分配跳过 |
| AssignUserRoles | :execrows | 用户/角色列表/操作者 → 新增条数 | 批量写入用户-角色映射，返回实际新增行数；已拥有的角色跳过 |
| HasUserRole | :one | 用户/角色 → bool | 判断用户是否拥有指定角色 |

### perm.sql（权限查询接口）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| CheckVisible | :one | 操作者/维度掩码/归属三元组（可空）→ bool | 按维度掩码判断归属是否可见/可操作（读写同权）；dims 1=归属人、2=归属部门、4=归属用户组，可按位组合；归属参数为 NULL 表示无该归属 |
| HasPermission | :one | 操作者/权限码 → bool | 判断用户是否持有指定功能权限码（角色→菜单→权限码，与数据范围正交） |
| GetScope | :one | 操作者 → 三个 int8[] 列（可 NULL） | 一次取回三个维度的可见集合；列 NULL 表示全租户不过滤，空数组表示无可见项，数组为可见集合并集 |
| GetUserRoles | :many | 用户 → SysRole 行 | 枚举用户的有效角色；用户无效（不存在/禁用/删除）时返回空集 |
| GetPermissionCodes | :many | 用户 → 权限码列表（去重排序） | 枚举用户的有效权限码（去重）；用户无效（不存在/禁用/删除）时返回空集 |
| GetScopeVersion | :one | 用户 → (租户版本, 用户版本) | 读取租户级与用户级范围版本；行不存在时返回 0 |

### basic.sql（basic 业务表操作）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetBasicForUpdate | :one | id → Basic | 按 id 锁定并读取一行 basic（FOR UPDATE） |
| UpdateBasicOwner | :exec | 新归属（可空）+ 操作者 + id | 更新 basic 行的归属；归属参数为 NULL 表示清空该归属，updated_at 由触发器刷新 |

### sys_user.sql（用户固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysUserByID | :one | id → SysUser | 按 id 读取未软删的用户（不做数据范围过滤） |
| GetSysUserForUpdate | :one | id → SysUser | 按 id 锁定并读取未软删的用户（FOR UPDATE；不做数据范围过滤） |
| InsertSysUser | :one | 用户字段 + 操作者 → SysUser | 插入用户记录；租户取会话 app.tenant_id |
| UpdateSysUser | :one | 可变字段 + 操作者 + id → SysUser | 更新可变字段（不含 password 与归属三元组）；updated_at 由触发器刷新 |
| SetSysUserPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 替换口令哈希 |
| SoftDeleteSysUser | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除用户 |
| UpdateSysUserOwner | :exec | 新归属三元组 + 操作者 + id | 更新用户归属三元组；归属参数为 NULL 表示清空该归属 |
| GetUserCredentialByName | :one | 用户名 → 凭据行（含口令哈希） | 按用户名读取口令哈希与状态（仅未软删行） |

### sys_session.sql（会话固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| InsertSysSession | :one | 身份 + 令牌哈希/有效期 → SysSession | 插入会话记录；租户取会话 app.tenant_id，有效期按 TTL 从数据库时钟起算 |
| GetActiveSysSessionByTokenHash | :one | 访问令牌哈希 → SysSession | 按访问令牌哈希读取启用、未撤销、未过期的会话；无行即无效 |
| RevokeSysSession | :execrows | 操作者 + id → 行数 | 撤销指定会话；已撤销时更新 0 行 |
| RotateSysSession | :one | 当前刷新令牌哈希 + 新令牌哈希/有效期 → SysSession | 按当前刷新令牌哈希轮换令牌；仅命中启用、未撤销且未过期的会话，未命中时更新 0 行 |
| CleanupExpiredSysSessions | :one | 宽限期秒数 → 删除行数 | 删除已撤销或过期超过宽限期的会话，返回删除行数 |

批量过滤 fn_filter_visible_rows 没有对应命名查询：sqlc 解析不了
表函数的返回列，权限查询服务层直接以参数化 SQL 调用该函数。

注意：命名查询生成的是 `*Queries` 上的**方法**（`gen.New(db)` 实例化后
调用）；动态查询生成的是顶层函数（ParseXxx/ListXxx，db 作参数显式传）。
两种风格不要混淆。

## RLS 策略（rls.sql）

21 张租户表用同一条规则：`tenant_id = current_setting('app.tenant_id')`，
读和写都检查，表主也不豁免（FORCE）。会话没设置 app.tenant_id 时
什么都读不到、写不进。sys_tenant 额外允许命中自身 id（子租户挂在
父租户名下时还能读写自己的行）。sys_config 是全局配置、没有租户列，
不设 RLS；范围版本表 sys_perm_version 的策略在 expose.sql。

sys_session 另有两条令牌维度策略（`sys_session_token_read` /
`sys_session_token_rotate`），服务会话校验与刷新：这两个动作发生在
租户身份成立之前，令牌哈希（访问令牌或刷新令牌）是全局唯一的秘密
凭据，持有即授权命中那一行。哈希以事务级 GUC `app.session_token_hash` /
`app.session_refresh_token_hash` / `app.session_new_refresh_token_hash`
声明；未设时 `current_setting(..., true)` 为 NULL，比较恒为假，不会
额外放行任何行。校验/刷新事务不设 app.tenant_id，除命中行外所有
租户数据仍默认全拒。
