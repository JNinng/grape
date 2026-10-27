# sqlc — 数据权限体系 SQL 对象参考

grape 关系存储的 SQL 载体，移植自数据权限体系模板：schema 按业务域拆分、
RLS 租户隔离、读侧权限过滤、写前判断、权限查询判定、种子数据一应俱全。
本文件是这些文件里**函数和命名查询的索引**：每个对象的签名、用途和返回值
约定。

修改函数时同步更新三处：SQL 里的注释、`COMMENT ON FUNCTION`、本文件的
索引表；修改命名查询时同步查询注释（sqlc 带入 Go doc）与索引表。

## 文件总览

| # | 文件 | 作用描述 | sqlc 输入 | 内容 |

> 表内 # 为 sqlc.yaml 装载序，不是部署执行序：部署顺序以
> `internal/data/pg_sqlc/deploy.go` 的 DeployFiles 清单与仓库根
> deploy_db.md 为准（schema 域文件整体先于 role.sql）。
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
| 10 | schema/95_agent_audit.sql | 定义 Agent 业务审计表：工具调用审计与授权确认审计（append-only，无动态协议），含租户 RLS（策略落本域文件，理由见文件头注释） | 是 | 2 张表、6 个索引、2 条策略 |
| 11 | role.sql | 定义 grape_u 运行角色与 test_u 演示用户（口令占位符）、schema 使用权、全表/全序列 DML 授权、默认权限与 RLS 会话变量写入权 | 否 | 2 个角色、8 条授权/默认权限 |
| 12 | rls.sql | 定义租户隔离 RLS 策略、fn_accessible_* 权限过滤函数、updated_at 触发器与会话令牌维度策略 | 是（只读函数签名） | 24 条策略（含 sys_session 令牌维度 3 条）、4 个函数 |
| 13 | operate.sql | 定义 fn_can_* 写前判断函数 | 是（只读函数签名） | 4 个判断函数 |
| 14 | expose.sql | 定义只读权限查询判定函数、范围版本表与 bump 触发器 | 是（只读函数签名） | 3 个查询判断函数、1 张版本表、3 个触发器函数 |
| 15 | 96_agent_grant.sql | 定义 Agent 工具授权表：agent_tool_grant、未撤销唯一约束（部分唯一表达式索引）、租户 RLS 策略与运行角色授权（C2；策略随域文件走，口径同 expose.sql 的 sys_perm_version） | 是 | 1 张表、1 个部分唯一索引、1 个普通索引、1 条策略、1 条授权 |
| 16 | schema/97_agent_retrieval_log.sql | 定义 Agent 检索留痕表：agent_retrieval_log（append-only，无动态协议）、租户+时间/会话/来源三类索引与租户 RLS（D3；策略随域文件走，95 同口径；保留期与归档策略、管理面留痕触发点见文件头注释） | 是 | 1 张表、3 个索引、1 条策略 |
| 17 | queries/ | 定义写侧判定、basic 操作、权限查询、业务表固定 CRUD 与 Agent 审计/授权/检索留痕读写的命名查询 | 是（queries） | 8 个文件、49 条命名查询 |
| 18 | seed.sql | 填充种子数据（含多租户权限样例与 C2 授权管理权限码菜单） | 否 | 租户/部门/角色/用户/用户组/字典/菜单/配置/开关/翻译/日志 + basic 归属矩阵 |
| 19 | agent_session.sql | 定义 Agent 会话存储：schema agent 与 trpc-agent-go 框架 session/postgres 的六张 `agent_*` 表、索引及 grape_u 授权 | 否 | 1 个 schema、6 张表、12 个索引、3 条授权 |
| 20 | agent_vector.sql | 定义向量检索设施：pgvector 扩展、agent schema、agent_document 向量表、HNSW 索引与运行角色授权（超级用户面；租户隔离由应用层适配器强制，RLS 仅作可选加强；授权收窄到 agent_document） | 否 | 1 个扩展、1 个 schema、1 张表、2 个索引、2 条授权 |


- schema 目录是表结构与注释 DSL（@dynamic/@scope/@filter/@page 等）的单一事实源；
  列级/表级指令的语法、类型映射与校验规则以上游参考为准：
  [dsl.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/dsl.md)（指令总表）、
  [scope.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/scope.md)（行级数据权限）、
  [schema.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/main/schema.md)（前端 Schema 契约）。
  `sqlc.yaml` 的 schema 列表 = schema/ 十一个文件按序 + rls/operate/expose
  + 96_agent_grant.sql + 97_agent_retrieval_log.sql，queries 列表 =
  queries/ 八个文件（operate/basic/perm、业务表固定 CRUD
  sys_user/sys_session 与 Agent 审计 agent_audit、工具授权 agent_grant、
  检索留痕 agent_retrieval_log）。加 rls/operate/expose 是为了让 queries 里的查
  询能调用这些文件里的函数（GetScope 调三件套、判断类调 fn_can_* /
  fn_check_visible）；sqlc 只看签名，函数体是否存在于库里由执行顺序保证。
  业务表的固定 CRUD 一资源一文件（queries/<资源>.sql），

  随纵切补入清单。
- 数据库部署按固定顺序执行 19 个文件：`schema/10_basic.sql` →
  `schema/20_tenant.sql` → `schema/30_user.sql` → `schema/40_rbac.sql` →
  `schema/50_dept.sql` → `schema/60_dict.sql` → `schema/70_audit.sql` →
  `schema/80_session.sql` → `schema/90_config.sql` → `role.sql` →
  `rls.sql` → `operate.sql` → `expose.sql` → `seed.sql` →
  `agent_session.sql` 与 `agent_vector.sql` → `schema/95_agent_audit.sql`
  → `96_agent_grant.sql` → `schema/97_agent_retrieval_log.sql`。单一事实源是

  `internal/data/pg_sqlc/deploy.go` 的 `DeployFiles` 清单（dbdeploy 命令
  与集成测试共用）；`role.sql` 必须先于 `rls.sql`（operate/expose 的函数
  授权依赖角色存在），`seed.sql` 必须在 RLS 启用后执行，且以超级用户/
  表 owner 连接（超级用户绕过 RLS），agent 域两文件只依赖 `role.sql` 的
  角色存在（授权对象），与 public 业务域零耦合，schema 幂等建立、
  相互无顺序约束。`agent_session.sql` 不入 sqlc catalog：trpc-agent-go
  框架自管表（应用经 session.Service 接口读写，grape 侧无仓储/命名
  查询），结构须与框架建表模板逐列一致，框架升级时以新模板 diff 该文件。
  `schema/95_agent_audit.sql` 是 sqlc catalog 输入（命名查询见
  queries/agent_audit.sql），放清单尾部独立追加不改既有顺序，其
  grape_u 的 DML 授权由 role.sql 默认权限覆盖（同角色于 public 建表
  自动 GRANT，文件内不含授权语句）；RLS 策略落本域文件而非 rls.sql
  （新域独立成文件、不改多流共触的 rls.sql，策略形态与 rls.sql 完全
  一致）。执行顺序的依赖细节、幂等边界与一键部署见仓库根
  [deploy_db.md](../../../../deploy_db.md)。
- **会话框架表无 RLS（设施面例外）**：agent_* 会话表不进 @scope/RLS
  体系，租户隔离靠 userKey（tenant_id:user_id）应用层编码；不存租户
  业务数据，属文档化「设施面」例外（见 permission.md）。
- **向量 SQL 不进 sqlc catalog**：agent_document 的存取 SQL 以手写
  参数化常量落在 [../vector_repo.go](../vector_repo.go)，不经 sqlc 生成
  ——pgvector 的类型与相似度算子超出 sqlc 的类型映射支持面（gen 包
  无法建模 vector 列），且向量存取没有动态查询协议需求。该表也不进
  @scope/RLS 体系：租户隔离由应用层适配器强制（每条 SQL 带租户过滤、
  缺认证身份拒绝），RLS 语句以注释形式附在 agent_vector.sql
  供按需启用；跨租户管理操作（在线重建索引、全量清理）走显式 super
  管理口径，不设普通入口。
- **HNSW 建索引参数不进 conf（决策说明）**：度量（vector_cosine_ops）与
  m / ef_construction 属物理 DDL，固化于 [agent_vector.sql](agent_vector.sql)
  ——索引结构是部署面资产，改参数即改 DDL 重建索引，不是运行期可调项；
  conf.vector 只持检索面参数（enabled / dimension / table_name），
  ef_search 等检索期调参归 D2 检索配置另议（会话级 SET，不落表定义）。
- 再生成：在本目录执行 `sqlc generate -f sqlc.yaml`（或项目根
  `make sqlc`），产物写入 `../gen/`（禁止手改）。

## 三层权限模型（速查）

| 层 | 维度 | 执行者 | 失效姿态 |
|---|---|---|---|
| PostgreSQL RLS | 租户隔离（绝对边界） | 23 张租户表策略（21 张基线表策略在 rls.sql，agent 审计两表策略在 schema/95_agent_audit.sql），`tenant_id = current_setting('app.tenant_id')`，ENABLE + FORCE；sys_session 另加 3 条令牌维度策略 | 漏设变量 → 默认全拒 |
| `@scope` 守卫 | 数据范围（data_scope 1-7，多角色并集） | 生成代码 WHERE 头部注入守卫谓词 | 缺 Scope → fail-closed 报错 |
| 判断函数（写侧） | 操作权限 | `operate.sql` 布尔判定，经 `LockXxxPermitted` 在取行语句内过滤 + 同事务写入 | 行级 false → 无行（NotFound）；业务授权 false → Forbidden |

- **@scope 只挂在有归属字段的表上**：当前为 `basic` 与 `sys_user`
  （见 schema 文件表注释）；其余表租户内所有人可见。
- **软删过滤收敛为 @filter**：所有带 `deleted_at` 的业务表在表注释声明
  `@filter: deleted_at is_null`，动态读面（List/Count）与写面（Update）
  都附加该服务端固定不变量，协议不暴露 `deletedAt` 条件；写面可在调用
  点用 `WithoutFilter()` 显式绕过；固定命名查询的软删条件仍写在 SQL 里。
  回收站类需求走专用命名查询，不占动态协议。
- **可变业务列声明 `@updatable: set`**：动态更新面（`UpdateXxx`）的
  SET 只收声明列，定位统一走 `@pk` 自动方法；身份列（code /
  config_key / permission_code 等）、类型列、归属与结构列（parent_id /
  owner 三元组）、审计列不进白名单，跨行一致性与归属变更由专项命名查
  询保证；`updated_at` 由 `trg_touch_updated_at` 对任何 UPDATE 兜底刷
  新，动态更新与静态更新行为一致；`@scope` 不参与写面，写前仍按
  `LockXxxPermitted` 锁定并判定的既有纪律。
- **权限过滤函数只收一个用户 ID**：该用户能看到什么由函数内部查角色
  算出，撤销角色下一次查询立刻生效。
- **运行连接三条纪律**：普通数据库角色（勿用超级用户，会绕过 RLS）；
  业务事务每事务 `set_config('app.tenant_id', ..., true)`（身份成立前的
  令牌事务与介质维护是文档化例外）；Scope 只从服务端认证上下文注入，
  永不受前端请求影响。

某类约束该落在哪一层、读写形态为何把取行与判定合并、新增
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

### 查询判断函数（expose.sql，只读判定）

只读判定，不写库，可与写事务共用同一事务与快照；与上面两个家族共用同一套
data_scope 推导（内外同源）。供权限查询服务层（宿主侧，待建）调用，HTTP/gRPC
由宿主自包；`fn_has_permission` 同时被取行查询用于可空权限码校验。
拒绝返回 false/空集，不是错误。

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
| GetSysUserByID | :one | id → SysUser | 按 id 读取未软删的用户（不做数据范围过滤；仅限已授权事务内回读） |
| GetSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 按 id 读取数据范围内可见的用户；permission_code 非空时同时校验功能权限 |
| LockSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 按 id 锁定数据范围内可操作的用户（FOR UPDATE）；permission_code 非空时同时校验功能权限 |
| InsertSysUser | :one | 用户字段 + 操作者 → SysUser | 插入用户记录；租户取会话 app.tenant_id |
| UpdateSysUser | :one | 可变字段 + 操作者 + id → SysUser | 更新可变字段（不含 password 与归属三元组）；updated_at 由触发器刷新 |
| SetSysUserPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 替换口令哈希 |
| SoftDeleteSysUser | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除用户 |
| UpdateSysUserOwner | :exec | 新归属三元组 + 操作者 + id | 更新用户归属三元组；归属参数为 NULL 表示清空该归属 |
| GetUserCredentialByName | :one | 用户名 → 凭据行（含口令哈希） | 登录路径专用：按用户名读取口令哈希与状态（仅未软删行） |

### sys_session.sql（会话固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| InsertSysSession | :one | 身份 + 令牌哈希/有效期 → SysSession | 插入会话记录；租户取会话 app.tenant_id，有效期按 TTL 从数据库时钟起算 |
| GetActiveSysSessionByTokenHash | :one | 访问令牌哈希 → SysSession | 按访问令牌哈希读取启用、未撤销、未过期的会话；无行即无效 |
| RevokeSysSession | :execrows | 操作者 + id → 行数 | 撤销指定会话；已撤销时更新 0 行 |
| RevokeSysSessionByTokenHash | :execrows | 访问令牌哈希 → 行数 | 登出：按访问令牌哈希撤销所属会话，updated_by 记会话用户；已撤销、已被清理或令牌不匹配时更新 0 行（幂等）。经 `sys_session_token_revoke` 策略走令牌维度事务 |
| RotateSysSession | :one | 当前刷新令牌哈希 + 新令牌哈希/有效期 → SysSession | 按当前刷新令牌哈希轮换令牌；仅命中启用、未撤销且未过期的会话，未命中时更新 0 行 |
| CleanupExpiredSysSessions | :one | 宽限期秒数 → 删除行数 | 删除已撤销或过期超过宽限期的会话，返回删除行数 |

### agent_audit.sql（Agent 业务审计读写）

两表 append-only：只有写入与读取，没有更新/删除面；读写都要求会话已设
置 app.tenant_id（IdentityTx 门面注入）。租户边界双重声明：RLS 为主，
读侧 WHERE 显式带同源租户谓词作应用层兜底（RLS 被关闭时仍不越租户，
对齐 D1 验收口径）。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| InsertAgentToolCallLog | :one | 身份 + 工具调用字段（trace_id/session_id/摘要/耗时/重试可空）→ AgentToolCallLog | 插入一条工具调用审计；租户取会话 app.tenant_id，创建者与记录用户同值；tenant_id+tool_call_id 唯一，重复写入报唯一冲突 |
| InsertAgentConfirmationLog | :one | 身份 + 确认字段（决定/remember/路径/操作者）→ AgentConfirmationLog | 插入一条授权确认审计（仅终态决定）；租户取会话 app.tenant_id；tenant_id+confirmation_id 唯一 |
| ListAgentToolCallLogs | :many | 可空 session_id + limit/offset → 行集 | 按会话过滤分页查询工具调用审计（时间倒序，同秒 id tie-break）；session_id 为 NULL 不过滤 |
| CountAgentToolCallLogs | :one | 可空 session_id → 行数 | 与 List 同条件的计数，供分页信封 |
| ListAgentConfirmationLogs | :many | 可空 session_id + limit/offset → 行集 | 按会话过滤分页查询授权确认审计（时间倒序，同秒 id tie-break） |
| CountAgentConfirmationLogs | :one | 可空 session_id → 行数 | 与 List 同条件的计数，供分页信封 |
| GetAgentToolCallLogByToolCallID | :one | tool_call_id → AgentToolCallLog | 按工具调用 ID 点查；无行即不存在或跨租户（上层统一 NotFound） |
| GetAgentConfirmationLogByConfirmationID | :one | confirmation_id → AgentConfirmationLog | 按确认 ID 点查；无行即不存在或跨租户（上层统一 NotFound） |
### agent_grant.sql（Agent 工具授权固定 CRUD）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| InsertAgentToolGrant | :execrows | 授权字段（session_id 可空=全局级）→ 写入行数 | 写入一条授权；同键活跃许可已存在时经部分唯一索引幂等跳过（ON CONFLICT 不带目标覆盖表达式部分索引），返回 0 即已存在 |
| GetActiveAgentToolGrantForSession | :one | 租户/用户/工具/会话 → AgentToolGrant | 会话级活跃许可点查（评估第一优先）；无行即未命中 |
| GetActiveAgentToolGrantGlobal | :one | 租户/用户/工具 → AgentToolGrant | 全局级活跃许可点查（会话级未命中后的兜底）；无行即未命中 |
| GetAgentToolGrantByID | :one | id/租户 → AgentToolGrant | 按租户内 id 取授权行（含已吊销，供吊销前归属判定）；跨租户等同不存在 |
| ListActiveAgentToolGrantsByUser | :many | 租户/用户 → 行集 | 指定用户的活跃授权列表（本人视图），created_at DESC, id DESC |
| ListActiveAgentToolGrantsByTenant | :many | 租户 → 行集 | 租户内全部活跃授权列表（管理视图，入口侧验 agent:grant:query） |
| RevokeAgentToolGrantSelf | :execrows | id/租户/归属人/吊销人 → 行数 | 属主吊销：id + 归属人双定位，原子置 revoked_at；非本人/已吊销/不存在更新 0 行 |
| RevokeAgentToolGrantManaged | :execrows | id/租户/吊销人 → 行数 | 管理吊销：调用方已验 agent:grant:revoke；已吊销或不存在更新 0 行 |

评估原语（两个 GetActive*）的「无行」是正常控制流：仓储映射为
(nil, nil)，查询次序「会话级先于全局级」由用例层（biz.GrantUsecase.
Resolve）编排。tenant_id 在全部查询里显式过滤——RLS 之外的应用层
兜底（与向量层隔离口径一致，集成测试以无 RLS 场景断言）。

### agent_retrieval_log.sql（Agent 检索留痕读写）

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| InsertAgentRetrievalLog | :one | 留痕字段（可空列 narg）→ AgentRetrievalLog | 写入一条检索留痕；租户取会话 app.tenant_id，写入者与用户同值 |
| ListAgentRetrievalLogs | :many | 会话（可空）/来源（可空）/limit/offset → 行集 | 按会话/来源过滤分页查询（A3 按会话/时间回放），created_at DESC, id DESC |
| CountAgentRetrievalLogs | :one | 会话（可空）/来源（可空）→ int64 | 与 List 同条件计数，供分页信封 |

租户边界与审计同型双重声明：RLS（97 域文件的策略）为主，读侧 WHERE
的同源租户谓词（`current_setting('app.tenant_id', true)`）作应用层兜
底——RLS 被关闭时读取仍不越租户。写入侧租户一律取会话变量，调用方
无法指定或伪造归属。


批量过滤 fn_filter_visible_rows 没有对应命名查询：sqlc 解析不了
表函数的返回列，权限查询服务层直接以参数化 SQL 调用该函数。

注意：命名查询生成的是 `*Queries` 上的**方法**（`gen.New(db)` 实例化后
调用）；动态查询生成的是顶层函数（ParseXxx/ListXxx/UpdateXxx，db 作参数显式传）。
两种风格不要混淆。

## RLS 策略（rls.sql 与域文件）

21 张基线租户表在 rls.sql、范围版本表在 expose.sql、Agent 工具授权表在 96_agent_grant.sql、Agent 检索留痕表在 97_agent_retrieval_log.sql（策略随域文件走），都用同一条规则：`tenant_id = current_setting('app.tenant_id')`，

读和写都检查，表主也不豁免（FORCE）。会话没设置 app.tenant_id 时
什么都读不到、写不进。sys_tenant 额外允许命中自身 id（子租户挂在
父租户名下时还能读写自己的行）。sys_config 是全局配置、没有租户列，
不设 RLS；范围版本表 sys_perm_version 的策略在 expose.sql。Agent 业务
审计两表（agent_tool_call_log / agent_confirmation_log）沿用同一规则，
策略声明在各自的域文件 schema/95_agent_audit.sql（并行冲突纪律：新域
独立成文件，不改多流共触的 rls.sql）。

sys_session 另有三条令牌维度策略（`sys_session_token_read` /
`sys_session_token_rotate` / `sys_session_token_revoke`），服务会话
校验、刷新与登出：这三个动作发生在租户身份成立之前，令牌哈希（访问
令牌或刷新令牌）是全局唯一的秘密凭据，持有即授权对命中那一行的读取
（校验）、轮换（刷新）或撤销（登出）。哈希以事务级 GUC
`app.session_token_hash` / `app.session_refresh_token_hash` /
`app.session_new_refresh_token_hash` 声明；未设时
`current_setting(..., true)` 为 NULL，比较恒为假，不会额外放行任何行。
校验/登出/刷新事务不设 app.tenant_id，除命中行外所有租户数据仍默认
全拒。`token_revoke` 的 WITH CHECK 与 USING 同为 token_hash 匹配，
固定撤销不得改动该列（防止借撤销路径偷换会话凭据）。
