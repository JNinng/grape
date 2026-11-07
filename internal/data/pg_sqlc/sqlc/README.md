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
| 3 | schema/30_user.sql | 定义 sys_user 用户表、用户-角色/用户组映射与用户组表；sys_user 带 @scope 列注释；sys_user_role_mapping 撤销走软删（deleted_by/deleted_at + 活跃映射部分唯一索引） | 是 | 4 张表 |
| 4 | schema/40_rbac.sql | 定义角色、data_scope 范围明细、角色可分发部门、菜单与角色-菜单授权表；sys_role 带 @scope 表注释（fn_visible_role_ids 角色可见性守卫） | 是 | 5 张表 |
| 5 | schema/50_dept.sql | 定义组织树与闭包表，以及闭包维护触发器函数 | 是 | 2 张表、1 个触发器函数 |
| 6 | schema/60_dict.sql | 定义字典类型与字典数据表 | 是 | 2 张表 |
| 7 | schema/70_audit.sql | 定义登录日志与操作日志表（append-only） | 是 | 2 张表 |
| 8 | schema/80_session.sql | 定义会话/令牌表（仅存哈希），以及过期会话清理函数 | 是 | 1 张表、1 个维护函数 |
| 9 | schema/90_config.sql | 定义全局配置、租户配置、功能开关与翻译表 | 是 | 4 张表 |
| 10 | schema/95_agent_audit.sql | 定义 Agent 业务审计表：工具调用审计与授权确认审计（append-only，无动态协议），含租户 RLS（策略落本域文件，理由见文件头注释） | 是 | 2 张表、6 个索引、2 条策略 |
| 11 | rls.sql | 定义租户隔离 RLS 策略、fn_accessible_* 权限过滤函数、updated_at 触发器与会话令牌维度策略 | 是（只读函数签名） | 24 条策略（含 sys_session 令牌维度 3 条）、4 个函数 |
| 12 | expose.sql | 定义只读权限查询判定函数、范围版本表与 bump 触发器 | 是（只读函数签名） | 3 个查询判断函数、1 张版本表、3 个触发器函数 |
| 13 | operate.sql | 定义 fn_can_* 写前判断函数与角色判定族（fn_role_visibility_core / fn_can_see_role / fn_visible_role_ids / fn_can_revoke_role / fn_can_set_owner） | 是（只读函数签名） | 9 个判断函数 |
| 14 | 96_agent_grant.sql | 定义 Agent 工具授权表：agent_tool_grant、未撤销唯一约束（部分唯一表达式索引）、租户 RLS 策略与运行角色授权（策略随域文件走，口径同 expose.sql 的 sys_perm_version） | 是 | 1 张表、1 个部分唯一索引、1 个普通索引、1 条策略、1 条授权 |
| 15 | schema/97_agent_retrieval_log.sql | 定义 Agent 检索留痕表：agent_retrieval_log（append-only，无动态协议）、租户+时间/会话/来源三类索引与租户 RLS（策略随域文件走，95 同口径；保留期与归档策略、管理面留痕触发点见文件头注释） | 是 | 1 张表、3 个索引、1 条策略 |
| 16 | queries/ | 定义写侧判定、basic 操作、权限查询、用户分配选择器/角色整组替换、业务表固定 CRUD（含用户组与成员映射、字典两级、配置族全局/租户/功能开关/翻译、租户树、审计日志点查、角色与数据范围/菜单授权映射、菜单读与受控写调用）与 Agent 审计/授权/检索留痕读写的命名查询 | 是（queries） | 17 个文件、144 条命名查询 |
| 17 | 98_menu_write.sql | 定义 sys_menu 受控写面：fn_menu_create/update/move/delete 四个 SECURITY DEFINER 固定口径写函数（建/改/移/软删），租户边界复位、父级校验、防环与软删口径收在函数内；role.sql 对 sys_menu 的直写 REVOKE 不动，EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（fn_cleanup_expired_sessions 先例） | 是（只读函数签名） | 4 个受控写函数 |
| 18 | 98_config_write.sql | 定义 sys_config 受控写面：fn_config_create/update/delete 三个 SECURITY DEFINER 固定口径写函数（建/改/软删），认证事务纪律校验（app.tenant_id 未设置即拒绝）与软删口径收在函数内；config_key/value_type 身份/值语义列不经更新路径；role.sql 对 sys_config 的直写 REVOKE 不动，EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（fn_menu_* 同款） | 是（只读函数签名） | 3 个受控写函数 |
| 19 | 98_tenant_guard.sql | 定义 sys_tenant 删除前置完整性判定（受控读取面）：fn_tenant_active_child_count 一个 SECURITY DEFINER 计数函数——租户树 RLS 下祖父看不到孙行，删除前置校验需要全局事实；函数只返回计数、不返回行数据，不构成跨租户数据读面，函数内校验 app.tenant_id 已设置（空串/未设置即拒绝，fail-closed）且目标对调用方可见（消除结构侧信道）；EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC | 是（只读函数签名） | 1 个受控判定函数 |
| — | role.sql | 定义 grape_u 运行角色与 test_u 演示用户（口令占位符）、schema 使用权、全表/全序列 DML 授权、默认权限与 RLS 会话变量写入权 | 否 | 2 个角色、8 条授权/默认权限 |
| — | seed.sql | 生产默认种子：平台树根租户 t-super-01 的自举数据（含授权管理权限码菜单 25x） | 否 | 租户/角色 101/用户 1（admin）/菜单/配置/开关/翻译 |
| — | test_seed.sql | 集成测试专用种子：t-test-01/t-test-02 测试租户及其演示数据（部门树、角色 102-110、用户 2-12、用户组、basic 归属矩阵、菜单副本 12xx/32xx（含用户分配/转归属权限点 3208/3209）、示例日志、租户配置与开关、字典演示——t-test-01 证件类型/客户来源/行业分类，t-test-02 无字典作对照；用户 12 superplain 属平台租户 t-super-01）；不入生产清单，仅由 deploy.TestDeployFiles（测试装载）在完整生产清单之后追加执行；仅用于全新建库，历史残留在文件头守卫显式报错 | 否 | 测试租户 2 个、部门/角色/用户/用户组/字典演示 3 类型 14 数据/菜单副本/映射/日志等 |
| — | 99_perm_seed.sql | 管理面功能权限码种子：sys_menu 操作权限点（246-249 用户/用户组/角色管理补充操作码、254-256 会话治理权限点、257 通用翻译页面、258-265 配置族全局/租户操作码、266-269 功能开关操作码、270-273 翻译操作码）与超级管理员（101）的角色-菜单映射 | 否 | 24 条菜单、24 条角色-菜单映射 |
| — | agent_session.sql | 定义 Agent 会话存储：schema agent 与 trpc-agent-go 框架 session/postgres 的六张 `agent_*` 表、索引及 grape_u 授权 | 否 | 1 个 schema、6 张表、12 个索引、3 条授权 |
| — | agent_vector.sql | 定义向量检索设施：pgvector 扩展、agent schema、agent_document 向量表（多嵌入空间：裸 vector 列 + embedder/embed_dim 逐行声明身份，CHECK 把维度列与实际向量长度绑定）、每维度表达式部分 HNSW 索引与运行角色授权（超级用户面；租户隔离由应用层适配器强制，RLS 仅作可选加强；授权收窄到 agent_document） | 否 | 1 个扩展、1 个 schema、1 张表、1 个 CHECK、2 个索引、2 条授权 |

> 表内 # 为 sqlc.yaml 装载序（「—」为不入 catalog 的文件），不是部署执行序：部署顺序以
> `internal/data/pg_sqlc/deploy/deploy.go` 的 DeployFiles 清单与仓库根
> deploy_db.md 为准。

- schema 目录是表结构与注释 DSL（@dynamic / @internal / @updatable 与表级
  @scope / @filter / @pk / @default_sort）的单一事实源；UI 元数据
  （label / desc / order / visibility / options / api）与分页策略在
  `sqlc.yaml` 的 `codegen.options.ui` / `codegen.options.page` 声明，
  注释里的人类文本不参与生成。
  列级/表级指令的语法、类型映射与校验规则以上游参考为准：
  [dsl.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/dsl.md)（指令总表）、
  [scope.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/scope.md)（行级数据权限）、
  [schema.md](https://github.com/JNinng/sqlc_gen_dynamic/blob/master/schema.md)（前端 Schema 契约）。
  `sqlc.yaml` 的 schema 列表 = schema/ 十一个域文件（10–90 九个域 +
  95_agent_audit + 97_agent_retrieval_log）+ rls/operate/expose +
  96_agent_grant.sql + 98_menu_write.sql + 98_config_write.sql +
  98_tenant_guard.sql，queries 列表 = queries/ 十七个文件
  （operate/basic/perm/assign_options、业务表固定 CRUD sys_user/sys_dept/sys_user_group/
  sys_dict/sys_audit_log/sys_role/sys_menu/sys_config/sys_tenant/sys_session 与 Agent
  审计 agent_audit、工具授权 agent_grant、检索留痕 agent_retrieval_log）。
  schema 列表里的函数声明文件提供 queries 调用所需的签名：
  98_menu_write.sql / 98_config_write.sql 对应 queries/menu.sql、
  queries/sys_config.sql 的受控写调用；98_tenant_guard.sql 对应
  queries/sys_tenant.sql 的删除前置计数；rls/operate/expose 对应
  queries 里的权限查询与判定调用（GetScope 调三件套、判断类调 fn_can_* /
  fn_check_visible）。sqlc 只看签名，函数体是否存在于库里由执行顺序保证。
  业务表的固定 CRUD 一资源一文件（queries/<资源>.sql），
  随纵切补入清单；例外是配置族四资源（sys_config / sys_tenant_config /
  sys_feature_flag / sys_translation）共用 queries/sys_config.sql。
- 数据库部署按固定顺序执行 23 个文件：`schema/10_basic.sql` →
  `schema/20_tenant.sql` → `schema/30_user.sql` → `schema/40_rbac.sql` →
  `schema/50_dept.sql` → `schema/60_dict.sql` → `schema/70_audit.sql` →
  `schema/80_session.sql` → `schema/90_config.sql` → `role.sql` →
  `rls.sql` → `operate.sql` → `expose.sql` → `seed.sql` →
  `agent_session.sql` 与 `agent_vector.sql` → `schema/95_agent_audit.sql`
  → `96_agent_grant.sql` → `schema/97_agent_retrieval_log.sql` →
  `98_menu_write.sql`（只依赖 40_rbac 的 sys_menu 存在，尾部独立追加）→
  `98_config_write.sql`（只依赖 90_config 的 sys_config 存在，尾部位
  于 98_menu_write 之后）→ `98_tenant_guard.sql`（租户删除前置计数函数，
  只依赖 20_tenant 的 sys_tenant 存在，位于 98_config_write 之后）→
  `99_perm_seed.sql`（管理面功能权限码种子，
  只依赖 seed.sql 的租户/角色/既有菜单树存在，尾部独立追加）。单一事实
  源是 `internal/data/pg_sqlc/deploy/deploy.go` 的 `DeployFiles` 清单
  （dbdeploy 命令使用）；集成测试经 `TestDeployFiles()` 在其后追加
  `test_seed.sql`（t-test-* 测试租户数据，依赖 seed.sql 的树根租户）。
  `role.sql` 必须先于 `rls.sql`
  （operate/expose 的函数授权依赖角色存在），`seed.sql` 必须在 RLS
  启用后执行，且以超级用户/表 owner 连接（超级用户绕过 RLS），agent
  域两文件只依赖 `role.sql` 的角色存在（授权对象），与 public 业务域
  零耦合，schema 幂等建立、相互无顺序约束。`agent_session.sql` 不入 sqlc catalog：trpc-agent-go
  框架自管表（应用经 session.Service 接口读写，grape 侧无仓储/命名
  查询），结构须与框架建表模板逐列一致，框架升级时以新模板 diff 该文件。
  `schema/95_agent_audit.sql` 是 sqlc catalog 输入（命名查询见
  queries/agent_audit.sql），放清单尾部独立追加不改既有顺序，其
  grape_u 的 DML 授权由 role.sql 默认权限覆盖（同角色于 public 建表
  自动 GRANT，文件内不含授权语句）；RLS 策略落本域文件而非 rls.sql
  （新域独立成文件、不改共享的 rls.sql，策略形态与 rls.sql 完全
  一致）。执行顺序的依赖细节、幂等边界与一键部署见仓库根
  [deploy_db.md](../../../../deploy_db.md)。
- **会话框架表无 RLS（设施面例外）**：agent_* 会话表不进 @scope/RLS
  体系，租户隔离靠 userKey（tenant_id:user_id）应用层编码；不存租户
  业务数据，属文档化「设施面」例外（见 permission.md）。
- **向量 SQL 不进 sqlc catalog**：agent_document 的存取 SQL 以手写
  参数化常量落在 [../vector/vector_repo.go](../vector/vector_repo.go)，不经 sqlc 生成
  ——pgvector 的类型与相似度算子超出 sqlc 的类型映射支持面（gen 包
  无法建模 vector 列），且向量存取没有动态查询协议需求。该表也不进
  @scope/RLS 体系：租户隔离由应用层适配器强制（每条 SQL 带租户过滤、
  缺认证身份拒绝），RLS 语句以注释形式附在 agent_vector.sql
  供按需启用；跨租户管理操作（在线重建索引、全量清理）走显式 super
  管理口径，不设普通入口。
- **HNSW 建索引参数不进 conf（决策说明）**：度量（vector_cosine_ops）与
  m / ef_construction 属物理 DDL，固化于 [agent_vector.sql](agent_vector.sql)
  ——索引结构是部署面资产，改参数即改 DDL 重建索引，不是运行期可调项；
  conf.vector 只持检索面参数（enabled / table_name / default_embedder /
  embedders 空间清单），ef_search 等检索期调参归检索配置另议（会话级
  SET，不落表定义）。索引以**每维度一个表达式部分索引**形态存在
  （`(embedding::vector(N)) vector_cosine_ops WHERE embed_dim = N`——裸
  vector 列只能表达式索引，查询侧重复同一 cast 才命中；同维度多空间
  共享一个索引）：新增嵌入维度 = conf.vector.embedders 加条目 + DDL 加
  对应索引，装配期探针逐维度对账，缺索引启动即失败。
- 再生成：在本目录执行 `sqlc generate -f sqlc.yaml`（或项目根
  `make sqlc`），产物写入 `../gen/`（禁止手改）。

## 三层权限模型（速查）

| 层 | 维度 | 执行者 | 失效姿态 |
|---|---|---|---|
| PostgreSQL RLS | 租户隔离（绝对边界） | 26 张租户表策略（21 张基线表在 rls.sql、范围版本表在 expose.sql，agent 审计两表在 schema/95_agent_audit.sql，agent_tool_grant 在 96_agent_grant.sql，agent_retrieval_log 在 schema/97_agent_retrieval_log.sql），`tenant_id = current_setting('app.tenant_id')`，ENABLE + FORCE；sys_session 另加 3 条令牌维度策略 | 漏设变量 → 默认全拒 |
| `@scope` 守卫 | 数据范围（data_scope 1-7，多角色并集） | 生成代码 WHERE 头部注入守卫谓词 | 缺 Scope → fail-closed 报错 |
| 判断函数（写侧） | 操作权限 | `operate.sql` 布尔判定，经 `LockXxxPermitted` 在取行语句内过滤 + 同事务写入 | 行级 false → 无行（NotFound）；业务授权 false → Forbidden |

- **@scope 挂两类表**：有归属字段的 `basic` 与 `sys_user`（守卫取
  `fn_accessible_*` 三件套），与 `sys_role`（守卫取 `fn_visible_role_ids`，
  与写侧 `fn_can_see_role` 共用同一 A AND G 角色可见性口径）（见 schema
  文件表注释）；其余表租户内所有人可见。
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
| fn_role_visibility_core | (操作者, 角色) → boolean | S | 内部核心：角色可见性 A AND G 判定（读/写面共用）。A=分发映射命中操作人可见部门集合（M 为空恒真，命中不替代 G）；G=组织可见范围（S 为 NULL 仅全租户操作人；S 非空时全租户操作人恒可见，受限操作人须 S∈O_d 且目标角色 data_scope 档位满足对应条件）。不校验角色行状态 |
| fn_can_see_role | (操作者, 角色) → boolean | S | 操作人能否看见某个角色（ListRoles/GetRole/GetRoleDataScope/ListRoleMenus 共用，与动态列表 fn_visible_role_ids 同源）。角色未软删且 A AND G 通过即可见，不按启用状态过滤 |
| fn_visible_role_ids | (用户ID) → int8[] | S | 能看到哪些角色（sys_role @scope 守卫；逐角色调用 fn_can_see_role 过滤，共用 A AND G；NULL=全租户操作人，不过滤；不按角色启用状态过滤） |
| fn_can_assign_role | (操作者, 角色, 目标用户) → boolean | S | 能否把角色分配给用户。前置：操作人/角色/目标有效、fn_can_see_role 可见、持有 sys:user:assign-role。目标部门须同时落入 sys_role_allowed_dept 映射与 scope_dept_id 子树（均配置时取交集）；无部门目标仅「无映射且无组织范围」可分配；不要求目标用户对操作人可见 |
| fn_can_assign_roles | (操作者, 角色数组, 目标用户) → (role_id, ok) 行集 | S | 上面那条的批量版：对数组里每个角色独立判断，逐个返回结论；全部通过才批量写入 |
| fn_can_revoke_role | (操作者, 角色, 目标用户) → boolean | S | 能否撤销一条用户-角色映射。沿用可见性（fn_role_visibility_core）与目标范围约束并持有 sys:user:assign-role；放宽角色/目标状态校验——禁用/软删角色、禁用/软删用户也可撤销 |
| fn_can_operate_row | (用户, 归属人, 归属部门, 归属组) → boolean | S | 能否修改/删除一条数据。能看见就能操作：有全租户角色直接通过；否则任一归属命中可见范围即通过；三个归属全空的数据只有全租户能操作 |
| fn_can_set_owner | (用户, 归属人, 归属部门, 归属组) → boolean | S | 给定归属三元组的每个非空归属是否可挂（存在、启用、未删除且在当前用户可见范围内；NULL=无该归属直接通过）。创建路径与 fn_can_transfer_owner 的新归属段共用，三维度同口径 |
| fn_can_transfer_owner | (用户, 旧归属×3, 新归属×3) → boolean | S | 能否修改数据的归属。两头都要过：这条数据本身有权操作（fn_can_operate_row），且新归属存在、启用且在可见范围内（fn_can_set_owner） |

### 查询判断函数（expose.sql，只读判定）

只读判定，不写库，可与写事务共用同一事务与快照；与上面两个家族共用同一套
data_scope 推导（内外同源）。`fn_check_visible` 与 `fn_has_permission` 经命名查询
被取行判定与功能权限码判定消费（queries/sys_user.sql、queries/perm.sql）；
`fn_filter_visible_rows` 暂无命名查询消费方（sqlc 解析不了表函数返回列，按需以
参数化 SQL 调用）。拒绝返回 false/空集，不是错误。

| 函数 | 签名 | 属性 | 判断什么 |
|---|---|---|---|
| fn_check_visible | (用户, 维度掩码, 归属人, 归属部门, 归属组) → boolean | S | 能否看见/操作一条数据（读写同权）。维度掩码：1 人、2 部门、4 组，按位组合 OR 并集；等价于 fn_can_operate_row（掩码 7）。未选中 1/2/4 中任何位时恒 false，其余位忽略 |
| fn_filter_visible_rows | (用户, 维度掩码, 归属人数组, 归属部门数组, 归属组数组) → 归属三元组行集 | S | 上面那条的批量版：三个数组按下标对齐，只返回可见子集（保持传入顺序）。数组元素可为 NULL（无该归属） |
| fn_has_permission | (用户, 权限码) → boolean | S | 功能权限轴：用户的有效角色是否关联了携带该权限码的有效菜单（角色→菜单→权限码，与数据范围正交） |

### 受控写函数（98_menu_write.sql，sys_menu 管理端）

sys_menu 是功能入口表：role.sql 对运行角色 REVOKE 直写授权，全部写入经
本组 SECURITY DEFINER 固定口径写函数（API super 方法调用）。
DEFINER 以属主（部署超级用户）执行绕过 sys_menu 的 ENABLE+FORCE 租户
RLS 与直写禁令，函数内一律以 `current_setting('app.tenant_id')` 复位
租户边界（未设置即拒绝，fail-closed）——调用方事务纪律不变（每事务仍
设 app.tenant_id）。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（无显式
GRANT，fn_cleanup_expired_sessions 同款）：任何角色都能调用，但只能以
函数内固定口径在自身租户内写入，不构成跨租户扩权面。写路径经
trg_bump_perm_version_tenant 自动 bump 租户级权限版本。

| 函数 | 签名 | 属性 | 作用描述 |
|---|---|---|---|
| fn_menu_create | (id, status, name, permission_code, parent_id, menu_type, icon, route_path, component, permission_expr, sort_order, remark, 操作者) → sys_menu | SECURITY DEFINER，`SET search_path = public` | 新建菜单；父菜单须同租户存在且未删除（父级停用不拦，口径同 fn_maintain_dept_closure），拒绝自身为父；permission_code 租户内未删除菜单间唯一由部分唯一索引兜底 |
| fn_menu_update | (id, status, name, icon, route_path, component, permission_expr, sort_order, remark, 操作者) → sys_menu | 同上 | 覆盖可变字段；permission_code/menu_type/parent_id 身份/类型/结构列不经本路径变更；目标未命中返回 NULL（命名查询以 id IS NOT NULL 过滤成空集，映射 NotFound） |
| fn_menu_move | (id, parent_id, 操作者) → sys_menu | 同上 | 移动菜单（parent_id 为 NULL 表示移为根）；新父级须同租户存在且未删除，沿 parent_id 祖先链上溯命中被移动菜单即拒绝（防环，迭代上限防数据损坏死循环）；目标未命中返回 NULL |
| fn_menu_delete | (id, 操作者) → bigint | 同上 | 软删除菜单（deleted_at 置位）；存在未删除直接子菜单时拒绝（自叶向根逐层删）；角色-菜单映射行物理残留，fn_has_permission 活跃联结使其不再产生功能权限；返回删除行数（0=目标未命中） |

### 受控写函数（98_config_write.sql，sys_config 管理端）

sys_config 是平台级全局配置表（无 tenant_id、无 RLS）：role.sql 对运行
角色 REVOKE 直写授权，全部写入经本组 SECURITY DEFINER 固定口径写函数
（API super 方法调用）。DEFINER 以属主（部署超级用户）执行绕过
直写禁令；sys_config 本身无租户列，函数内仍以
`current_setting('app.tenant_id', true)` 校验认证事务纪律（未设置即拒绝，
fail-closed）——调用方事务纪律不变（每事务仍设 app.tenant_id）。EXECUTE
沿用 PostgreSQL 默认授权 PUBLIC（无显式 GRANT，fn_menu_* 同款）：任何
角色都能调用，但只能以函数内固定口径写入，不构成扩权面。
config_key（未删除行间唯一）与 value_type（消费方取值预处理声明）是
身份/值语义列，创建后不变；更新函数只覆盖 name/status/value/remark。

| 函数 | 签名 | 属性 | 作用描述 |
|---|---|---|---|
| fn_config_create | (id, status, name, config_key, value, value_type, remark, 操作者) → sys_config | SECURITY DEFINER，`SET search_path = public` | 新建全局配置；认证事务纪律未设置 app.tenant_id 即拒绝；config_key 未删除行间唯一由部分唯一索引兜底；created_by/updated_by 记操作者 |
| fn_config_update | (id, status, name, value, remark, 操作者) → sys_config | 同上 | 覆盖可变字段（name/status/value/remark）；config_key/value_type 身份/值语义列不经本路径变更；目标未命中返回 NULL（命名查询以 id IS NOT NULL 过滤成空集，映射 NotFound） |
| fn_config_delete | (id, 操作者) → bigint | 同上 | 软删除全局配置（deleted_at 置位）；返回删除行数（0=目标未命中，映射 NotFound） |

### 受控判定函数（98_tenant_guard.sql，sys_tenant 删除前置）

sys_tenant 的租户树策略只放行 `tenant_id = app.tenant_id` 或
`id = app.tenant_id` 的行：平台租户能看到挂在自身名下的直接子租户与
自身，但看不到子租户自己创建的孙租户（孙行 tenant_id = 子租户）。删除
租户要求「无未删除直接子租户」才能软删（自叶向根逐层删），只用 RLS
可见行做 count 会漏掉孙租户、产生孤儿行——该完整性判定需要全局事实。
本组 SECURITY DEFINER 函数以属主（部署超级用户）执行绕过租户 RLS，
只返回计数、不返回行数据，不构成跨租户数据读面，也不提供跨租户写
路径；函数内仍以 `current_setting('app.tenant_id', true)` 校验认证事务
纪律（未设置/空串即拒绝，fail-closed），并校验目标对调用方可见（`id`
或 `tenant_id` 命中调用方租户，否则拒绝）——防止把计数差异用作跨租户
结构侧信道。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（无显式 GRANT，
fn_menu_* 同款）。sys_tenant.parent_id 未设索引，计数按全表扫描
（见 98_tenant_guard.sql 文件头注释）。

| 函数 | 签名 | 属性 | 作用描述 |
|---|---|---|---|
| fn_tenant_active_child_count | (p_id varchar) → bigint | SECURITY DEFINER，`SET search_path = public` | 返回指定租户名下未删除直接子租户数（DEFINER 读全局事实，仅暴露计数）；app.tenant_id 未设置/空串即拒绝；目标须对调用方可见（id 或 tenant_id 命中），否则拒绝；供删除前置校验（计数大于 0 拒绝软删） |

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
| CanSeeRole | :one | 操作者/角色 → bool | 判断操作者能否看见指定角色（A AND G，角色读面共用判定） |
| CanAssignRole | :one | 操作者/角色/目标 → bool | 判断操作者能否把指定角色授予目标用户（可见性 + 目标范围 + 分配权限码） |
| CanAssignRoles | :many | 操作者/角色列表/目标 → (role_id, ok) 行 | 逐角色判断角色列表能否授予目标用户，每行返回 (role_id, ok) |
| CanRevokeRole | :one | 操作者/角色/目标 → bool | 判断操作者能否撤销目标用户的指定角色映射（状态放宽：禁用/软删也可撤） |
| CanRevokeRoles | :many | 操作者/角色列表/目标 → (role_id, ok) 行 | 逐角色判断角色列表能否从目标用户撤销，每行返回 (role_id, ok) |
| CanTransferOwner | :one | 操作者 + 旧/新归属（可空）→ bool | 判断操作者能否把数据的归属从旧三元组改为新三元组（旧数据可操作且新归属存在、启用、可达） |
| CanOperateRow | :one | 操作者 + 归属三元组（可空）→ bool | 判断操作者能否修改/删除指定归属的数据（读写同权） |
| CanSetOwner | :one | 操作者 + 归属三元组（可空）→ bool | 判断操作者能否把新归属三元组挂上（存在、启用且在可见范围内；NULL=无该归属） |
| AssignUserRole | :exec | id/用户/角色/操作者 | 写入一条用户-角色映射；id 为应用层发号主键，租户取会话 app.tenant_id，与活跃映射冲突（部分唯一索引）时跳过 |
| AssignUserRoles | :execrows | ids/用户/角色列表/操作者 → 新增条数 | 批量写入用户-角色映射；ids 与 role_ids 下标对齐（应用层发号），返回实际新增行数；已拥有的角色跳过 |
| HasUserRole | :one | 用户/角色 → bool | 判断用户是否拥有指定角色的活跃映射（已撤销的软删行排除） |

### assign_options.sql（用户分配选择器与角色整组替换）

用户页分配动作的候选与撤销面：可分配角色、当前角色集合、三个归属候选与
`SetUserRoles` 的撤销/自锁计数。角色分配复用 operate.sql 的
`fn_can_assign_role` / `fn_can_assign_roles` / `AssignUserRoles`，本文件只补
差集与查询。关键字统一 `strpos(lower(col), lower(keyword)) > 0`（避免 LIKE
通配符转义），空串不过滤；分页 `limit+1`/`offset`、`total` 不计算。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| ListAssignableRoleOptions | :many | 操作者/目标/keyword/limit/offset → (id, name) 行 | 目标用户可分配、尚未拥有的活跃角色（只按活跃映射排除，已撤销的软删历史行不排除）；`fn_can_assign_role` 过滤（可见性 + 目标范围 + 分配权限码），匹配 name/code，按 name/id 升序 |
| ListUserRoleIDs | :many | 用户 → role_id 列表 | 用户的全部活跃映射（含停用/软删角色残留；已撤销的软删行排除），升序；供 locked 差集计算 |
| ListActiveRoleOptionsByIDs | :many | role_id 数组 → (id, name) 行 | 活跃角色（status=1 且未软删）的标签与 desired 存在性校验，按 name/id 升序 |
| DeleteUserRoles | :execrows | 用户/role_id 数组/操作者 → 行数 | 批量撤销映射（软删留痕：deleted_at/deleted_by 记撤销人）：只撤 `fn_can_revoke_role`（可见性 + 目标范围，放宽角色/目标状态）仍可撤销的角色；调用方要求行数等于请求数，否则回滚 |
| CountRolesGrantingPermission | :one | role_id 数组/权限码 → 角色数 | 统计携带指定权限码的有效角色数（角色/菜单活跃联结，与 fn_has_permission 同口径）；自锁保护 |
| ListOwnerUserOptions | :many | 操作者/keyword/limit/offset → (id, label) 行 | 归属用户候选：`fn_accessible_user_ids` 三态过滤，label=`COALESCE(NULLIF(nickname,''),name)`，匹配 name/nickname |
| ListOwnerDeptOptions | :many | 操作者/keyword/limit/offset → (id, label) 行 | 归属部门候选：`fn_accessible_dept_ids` 三态过滤，label=name，匹配 name |
| ListOwnerGroupOptions | :many | 操作者/keyword/limit/offset → (id, label) 行 | 归属用户组候选：`fn_accessible_group_ids` 三态过滤，label=name，匹配 name/code |

`SetUserRoles` 的权威顺序（同一 IdentityTx）：`LockSysUserByID` 按 ID 锁目标
（不判可见性）→ `ListUserRoleIDs` → `ListActiveRoleOptionsByIDs` →
`CanAssignRoles` 分区 managed/locked → 触及 locked / desired 失活 / 新增
不可分配即拒绝 → 版本比对（`sha256("<user_id>|<升序 manageable ID>")`，
biz helper）→ 自锁计数 → `DeleteUserRoles`（按 fn_can_revoke_role 软删，
行数必须相等）→ `AssignUserRoles`（应用层发号 id）。不变量与错误语义见
根目录 [permission.md](../../../../permission.md)。

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
| GetSysUserByID | :one | id → SysUser | 按 id 读取未软删的用户（不做数据范围过滤；分配读面按 ID 判目标存在与已授权事务内回读共用） |
| GetSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 按 id 读取数据范围内可见的用户；permission_code 非空时同时校验功能权限 |
| LockSysUserPermitted | :one | id/操作者/可空权限码 → SysUser | 按 id 锁定数据范围内可操作的用户（FOR UPDATE）；permission_code 非空时同时校验功能权限 |
| LockSysUserByID | :one | id → SysUser | 按 id 锁定未软删的用户（FOR UPDATE，不判可见性）：角色分配/撤销路径按新方案不要求目标可见，按 ID 锁目标串行化并发分配 |
| InsertSysUser | :one | 用户字段 + 操作者 → SysUser | 插入用户记录；租户取会话 app.tenant_id |
| UpdateSysUser | :one | 可变字段 + 操作者 + id → SysUser | 更新可变字段（不含 password 与归属三元组）；updated_at 由触发器刷新 |
| SetSysUserPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 替换口令哈希 |
| SoftDeleteSysUser | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除用户 |
| UpdateSysUserOwner | :exec | 新归属三元组 + 操作者 + id | 更新用户归属三元组；归属参数为 NULL 表示清空该归属 |
| GetUserCredentialByName | :one | 用户名 → 凭据行（含口令哈希） | 登录路径专用：按用户名读取口令哈希与状态（仅未软删行） |
| GetMyProfileByID | :one | id → SysUser | 个人中心专用：按认证身份读取本人行（目标恒取 auth.UserID，不判行级归属；身份成立后的内部回读例外） |
| UpdateMyProfile | :one | 昵称/可空邮箱/手机号/备注/语言 + 操作者 + id → SysUser | 个人中心专用：覆盖本人可编辑资料（目标恒取 auth.UserID，不判行级归属） |
| GetMyPasswordHash | :one | id → 口令哈希 | 个人中心专用：读取本人口令哈希（仅供用例内当前口令校验） |
| SetMyPassword | :execrows | 密码哈希 + 操作者 + id → 行数 | 个人中心专用：替换本人口令哈希 |

### sys_dept.sql（部门固定 CRUD）

闭包表 sys_dept_closure 是派生数据，全部路径由 trg_maintain_dept_closure
触发器在写事务内自动维护（INSERT/恢复建路径、软删/物理删清路径、改
parent_id 重写路径并校验父级与防环），本文件不写闭包表的任何查询；
触发器的拒绝（父级不存在/已删除/跨租户、移入自身子树、有未删除子部门）
是并发窗口的兜底防线。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysDeptByID | :one | id → SysDept | 按 id 读取未软删的部门（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| LockSysDeptByID | :one | id → SysDept | 按 id 锁定未软删的部门（FOR UPDATE，写路径先锁后写） |
| ListSysDeptAll | :many | → 行集 | 读取租户内全部未软删部门（组织树装配与环检测的专用列表；created_at ASC、id ASC 稳定排序） |
| CountSysDeptActiveChildren | :one | id → 行数 | 统计未软删的直接子部门数（删除前置校验，触发器兜底同口径） |
| InsertSysDept | :one | 部门字段 + 操作者 → SysDept | 插入部门记录；租户取会话 app.tenant_id，闭包路径由触发器建立 |
| UpdateSysDept | :one | name/status/parent_id/owner_user_id/owner_user_group_id（可空）+ 操作者 + id → SysDept | 覆盖可变字段（PUT 覆盖语义）；parent_id 为 NULL 移为根、owner_user_group_id 为 NULL 清空；闭包路径由触发器同事务校验并重写；updated_at 由触发器刷新 |
| MoveSysDept | :one | 新父级（可空）+ 操作者 + id → SysDept | 移动部门；NULL 表示移为根部门，闭包路径由触发器重写（父级/防环校验兜底） |
| SoftDeleteSysDept | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除部门；存在未软删直接子部门时由触发器拒绝 |

### sys_user_group.sql（用户组固定 CRUD 与成员映射）

用户组是租户内资源（表无 @scope，租户内全可见）；动态查询能力由
schema/30_user.sql 的列注释 DSL 声明（ParseSysUserGroupQuery 等顶层
函数为生成物）。成员映射 sys_user_group_mapping 不单独立资源，随宿主
用户组的管理面维护：映射行无可变业务字段，移除成员走物理删除；映射表
无外键级联，组软删后映射行物理残留，消费侧（fn_accessible_* 权限推导
与成员列表的活跃联结）都以组表/用户表 deleted_at IS NULL 过滤，残留行
不产生可见副作用。code 与 name 的部分唯一索引限定未删除组间，软删后
可复用。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysUserGroupByID | :one | id → SysUserGroup | 按 id 读取未软删的用户组（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| LockSysUserGroupByID | :one | id → SysUserGroup | 按 id 锁定未软删的用户组（FOR UPDATE，组与成员映射的写路径先锁后写） |
| InsertSysUserGroup | :one | 组字段 + 操作者 → SysUserGroup | 插入用户组记录；租户取会话 app.tenant_id；code/name 唯一冲突映射 AlreadyExists |
| UpdateSysUserGroup | :one | name/status/remark + 操作者 + id → SysUserGroup | 更新可变字段（不含 code 与 owner_user_id——身份与归属列不经本路径变更）；updated_at 由触发器刷新 |
| SoftDeleteSysUserGroup | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除用户组（成员映射行物理残留，消费侧活跃过滤排除） |
| ListSysUserGroupMembers | :many | group_id/limit/offset → 行集 | 分页返回组成员（联结用户表展示 name/nickname/status；按添加时间 ASC、user_id ASC 稳定排序；已软删用户被活跃联结过滤不出现在结果中） |
| CountSysUserGroupMembers | :one | group_id → 行数 | 与 List 同条件统计成员数，供分页信封 |
| CountSysUserGroupMemberCandidates | :one | 用户 ID 数组 → 行数 | 统计候选成员中租户内未软删用户数（添加成员前的存在性判定：命中数与请求数不等即存在未知/跨租户/已删除用户，整批拒绝不写入） |
| AddSysUserGroupMembers | :execrows | group_id/用户 ID 数组/操作者 → 新增条数 | 批量写入成员映射；租户取会话 app.tenant_id，重复添加经主键冲突跳过（幂等） |
| RemoveSysUserGroupMembers | :execrows | group_id/用户 ID 数组 → 移除条数 | 批量物理删除成员映射；移除非成员更新 0 行（幂等，不报错） |

### sys_dict.sql（字典两级固定 CRUD）

字典是租户内公共资源（sys_dict_type / sys_dict_data，表无 @scope，租户内
全可见）；动态查询能力由 schema/60_dict.sql 的列注释 DSL 声明
（ParseSysDictTypeQuery 等顶层函数为生成物），本文件只写固定命名查询。
两级同置一文件：数据随宿主类型的管理面维护——数据创建先锁宿主类型行
再插入（同事务内判定宿主存在且未删除，锁住类型行同时杜绝与类型软删的
并发窗口），类型删除先统计未删除数据、大于 0 即拒绝（先清空该类型下的
数据）；类型 code 与数据 value 的唯一约束限定未删除行间（部分唯一索引），
软删后可复用；类型软删后数据行物理残留，消费侧按需过滤宿主活跃性。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysDictTypeByID | :one | id → SysDictType | 按 id 读取未软删的字典类型（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| LockSysDictTypeByID | :one | id → SysDictType | 按 id 锁定未软删的字典类型（FOR UPDATE，类型写路径与数据创建路径先锁后写） |
| InsertSysDictType | :one | 类型字段 + 操作者 → SysDictType | 插入字典类型记录；租户取会话 app.tenant_id，code 唯一冲突映射 AlreadyExists |
| UpdateSysDictType | :one | name/status/remark + 操作者 + id → SysDictType | 更新可变字段（不含 code——身份列不经本路径变更）；updated_at 由触发器刷新 |
| CountSysDictDataByType | :one | dict_type_id → 行数 | 统计类型下未删除字典数据数（类型删除前置校验，大于 0 拒绝软删） |
| SoftDeleteSysDictType | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除字典类型；活跃数据校验由调用方在同一事务内先行完成 |
| GetSysDictDataByID | :one | id → SysDictData | 按 id 读取未软删的字典数据（租户边界由 RLS 承担） |
| LockSysDictDataByID | :one | id → SysDictData | 按 id 锁定未软删的字典数据（FOR UPDATE，数据更新/删除路径先锁后写） |
| InsertSysDictData | :one | 数据字段 + 操作者 → SysDictData | 插入字典数据记录；租户取会话 app.tenant_id，同类型下 value 唯一冲突映射 AlreadyExists（宿主存在性由调用方先锁类型行判定） |
| UpdateSysDictData | :one | label/status/sort_order/remark + 操作者 + id → SysDictData | 更新可变字段（不含 value/value_type 与 dict_type_id——值语义列与宿主归属列不经本路径变更）；updated_at 由触发器刷新 |
| SoftDeleteSysDictData | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除字典数据（宿主类型不受影响） |

### sys_role.sql（角色固定 CRUD 与数据范围/菜单授权映射）

角色是租户内资源（租户边界由 RLS 承担，行级可见性由表级 @scope 守卫
fn_visible_role_ids 与详情点查 fn_can_see_role 承担，共用同一 A AND G
口径）；动态查询能力由
schema/40_rbac.sql 的表/列注释 DSL 声明（ParseSysRoleQuery 等顶层函数为
生成物）。数据范围明细 sys_role_id_scope、分发部门
sys_role_allowed_dept 与角色-菜单映射 sys_role_menu 不单独立资源，随
宿主角色的管理面维护：设置走差量同步——事务内先锁角色行、读活跃行
集合、在应用层算差后逐行增删（Add* 记配置/授权人，既有命中行保留原
审计，Delete* 物理删除避免重复配置堆积软删残留）；明细/映射引用的对象
（用户/部门/用户组/菜单）必须存在且启用（Count*Refs 判定，口径同
fn_accessible_* / fn_has_permission 的活跃联结），整批拒绝不写入。角色
软删后明细/映射行物理残留，消费侧（fn_accessible_* /
fn_can_assign_role / fn_has_permission）都以角色表 status=1 且未删除
活跃过滤，残留行不产生可见副作用；code/name 唯一约束限定未删除行间，
软删后可复用。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysRoleByID | :one | id → SysRole | 按 id 读取未软删的角色（租户边界由 RLS 承担；写路径与已授权事务内回读用） |
| GetSysRoleVisibleByID | :one | id/操作者 → SysRole | 按 id 读取操作人可见的未软删角色（fn_can_see_role：A AND G 共用判定；不可见等同不存在；详情读面专用） |
| LockSysRoleByID | :one | id → SysRole | 按 id 锁定未软删的角色（FOR UPDATE，角色与其明细/映射的写路径先锁后写） |
| InsertSysRole | :one | 角色字段 + 操作者 → SysRole | 插入角色记录；租户取会话 app.tenant_id；code/name 唯一冲突映射 AlreadyExists |
| UpdateSysRole | :one | name/status/remark + 操作者 + id → SysRole | 更新可变字段（不含 code/role_type 与 data_scope/scope_dept_id——身份列与数据范围配置不经本路径变更）；updated_at 由触发器刷新 |
| UpdateSysRoleDataScope | :one | data_scope/scope_dept_id + 操作者 + id → SysRole | 更新数据范围配置标量（SetRoleDataScope 专项口径；scope_dept_id 为 NULL 表示全租户可分配） |
| SoftDeleteSysRole | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除角色（明细/映射行物理残留，消费侧活跃过滤排除） |
| ListSysRoleIDScopes | :many | role_id → (type, data_id) 行集 | 读取数据范围明细活跃行（按 type、data_id 升序） |
| ListSysRoleAllowedDepts | :many | role_id → dept_id 行集 | 读取分发部门活跃行（升序） |
| ListSysRoleMenus | :many | role_id → menu_id 行集 | 读取有效授权菜单 ID（联结菜单表 status=1 且未删除活跃过滤——停用/软删菜单的残留映射不出现在回显集合，与 fn_has_permission 同口径；升序） |
| ListSysRoleMenuGrantsAll | :many | role_id → menu_id 行集 | 读取角色-菜单映射物理行（不过滤菜单活跃性，供设置的差量对账——不在新集合内的行删除，含失活菜单残留授权；升序） |
| CountSysRoleScopeUserRefs | :one | 用户 ID 数组 → 行数 | 统计引用中租户内启用且未删除用户数（明细 type=1 存在性判定） |
| CountSysRoleScopeDeptRefs | :one | 部门 ID 数组 → 行数 | 统计引用中租户内启用且未删除部门数（明细 type=2、分发部门与 scope_dept_id 共用） |
| CountSysRoleScopeGroupRefs | :one | 用户组 ID 数组 → 行数 | 统计引用中租户内启用且未删除用户组数（明细 type=3 存在性判定） |
| CountSysRoleMenuRefs | :one | 菜单 ID 数组 → 行数 | 统计引用中租户内启用且未删除菜单数（角色-菜单授权存在性判定） |
| DeleteSysRoleIDScope | :execrows | role_id/type/data_id → 行数 | 移除一条数据范围明细（物理删除） |
| AddSysRoleIDScope | :execrows | id/role_id/type/data_id + 操作者 → 行数 | 补建一条数据范围明细；租户取会话 app.tenant_id |
| DeleteSysRoleAllowedDept | :execrows | role_id/dept_id → 行数 | 移除一条分发部门映射（物理删除） |
| AddSysRoleAllowedDept | :execrows | id/role_id/dept_id + 操作者 → 行数 | 补建一条分发部门映射；租户取会话 app.tenant_id |
| DeleteSysRoleMenu | :execrows | role_id/menu_id → 行数 | 移除一条角色-菜单映射（物理删除） |
| AddSysRoleMenu | :execrows | role_id/menu_id + 操作者 → 行数 | 补建一条角色-菜单授权；租户取会话 app.tenant_id，主键冲突跳过（保留原授权人） |

### menu.sql（菜单固定读查询与受控写调用）

sys_menu 是功能权限码链的宿主树：读面为租户内普通读（表无 @scope，租户
边界由 RLS 承担，行级无范围）；写面运行角色无直写权限（role.sql 对
sys_menu REVOKE 写授权），四个写查询即调用 98_menu_write.sql 受控写函数
的 SELECT——固定口径收在函数内（租户边界复位、父级校验、防环、软删），
事务纪律与普通写一致（调用方事务每事务设 app.tenant_id，函数内读取并
落值）。动态查询能力由 schema/40_rbac.sql 的列注释 DSL 声明
（ParseSysMenuQuery 等顶层函数为生成物），树查询走专用列表（全量装配，
不做分页）。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysMenuByID | :one | id → SysMenu | 按 id 读取未软删的菜单（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| ListSysMenuAll | :many | → 行集 | 读取租户内全部未软删菜单（菜单树装配与环检测的专用列表；sort_order ASC、id ASC 稳定排序） |
| CountSysMenuActiveChildren | :one | id → 行数 | 统计未软删的直接子菜单数（删除前置校验，受控写函数兜底同口径） |
| InsertSysMenu | :one | 菜单字段 + 操作者 → SysMenu | 新建菜单：调用 fn_menu_create（SECURITY DEFINER 固定口径）；租户由函数内会话 app.tenant_id 落值，permission_code 唯一冲突映射 AlreadyExists |
| UpdateSysMenu | :one | 可变字段 + 操作者 + id → SysMenu | 覆盖可变字段（不含 permission_code/menu_type/parent_id——身份/类型/结构列）：调用 fn_menu_update；目标未命中函数返回 NULL 复合行，查询以 id IS NOT NULL 过滤成空集（映射 NotFound） |
| MoveSysMenu | :one | 新父级（可空）+ 操作者 + id → SysMenu | 移动菜单：调用 fn_menu_move（父级/防环校验兜底，调用方已在用例层预检）；NULL 表示移为根；未命中同上过滤成空集 |
| SoftDeleteSysMenu | :one | 操作者 + id → 删除行数 | 软删除菜单：调用 fn_menu_delete（存在未软删直接子菜单时函数内拒绝）；返回行数 0 即目标未命中（映射 NotFound） |

### sys_config.sql（配置族固定 CRUD）

配置族是「全局默认 + 租户覆盖」两级：sys_config 是平台级全局表（无
tenant_id、无 RLS），读面为普通读、写面经 98_config_write.sql 的受控写
函数（运行角色无直写权限，本文件只写函数的 SELECT 调用）；
sys_tenant_config、sys_feature_flag、sys_translation 是租户表（RLS 承担
租户边界），读走固定点查、写为普通 DML（tenant_id 取
会话 app.tenant_id）。三张租户表的 `tenant_id` 为 `@internal` 列，动态
协议不接受 `tenantId` 过滤/投影，List 响应由服务端按认证身份回填。
动态查询能力由 schema/90_config.sql 的列注释 DSL
声明（ParseSysConfigQuery / ParseSysTenantConfigQuery /
ParseSysFeatureFlagQuery / ParseSysTranslationQuery 等顶层函数为生成
物），本文件只写固定命名查询。配置/功能开关的 config_key/flag_key 与翻译
的身份四元组唯一约束限定未删除行间（部分唯一索引），软删后可复用；
value_type 白名单与 status 取值校验收在 biz 用例。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysConfigByID | :one | id → SysConfig | 按 id 读取未软删的全局配置（sys_config 不设 RLS：全局表，读取本身即全局口径） |
| InsertSysConfig | :one | 配置字段 + 操作者 → SysConfig | 新建全局配置：调用 fn_config_create（SECURITY DEFINER 固定口径）；未删除行间 config_key 唯一冲突映射 AlreadyExists |
| UpdateSysConfig | :one | 可变字段 + 操作者 + id → SysConfig | 覆盖可变字段（name/status/value/remark）：调用 fn_config_update；目标未命中函数返回 NULL 复合行，查询以 id IS NOT NULL 过滤成空集（映射 NotFound） |
| SoftDeleteSysConfig | :one | 操作者 + id → 删除行数 | 软删除全局配置：调用 fn_config_delete；返回行数 0 即目标未命中（映射 NotFound） |
| GetSysTenantConfigByID | :one | id → SysTenantConfig | 按 id 读取未软删的租户配置（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| InsertSysTenantConfig | :one | 租户配置字段 + 操作者 → SysTenantConfig | 插入租户配置；租户取会话 app.tenant_id（不取请求值），租户内未删除配置间 config_key 唯一冲突映射 AlreadyExists |
| UpdateSysTenantConfig | :one | name/status/value/remark + 操作者 + id → SysTenantConfig | 更新可变字段（不含 config_key/value_type/tenant_id 身份/值语义/归属列）；updated_at 由触发器刷新 |
| SoftDeleteSysTenantConfig | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除租户配置 |
| GetSysFeatureFlagByID | :one | id → SysFeatureFlag | 按 id 读取未软删的功能开关（租户边界由 RLS 承担） |
| InsertSysFeatureFlag | :one | 开关字段 + 操作者 → SysFeatureFlag | 插入功能开关；租户取会话 app.tenant_id，租户内未删除开关间 flag_key 唯一冲突映射 AlreadyExists |
| UpdateSysFeatureFlag | :one | name/status/description/is_enabled/remark + 操作者 + id → SysFeatureFlag | 更新可变字段（不含 flag_key/tenant_id 身份/归属列）；updated_at 由触发器刷新 |
| SoftDeleteSysFeatureFlag | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除功能开关 |
| GetSysTranslationByID | :one | id → SysTranslation | 按 id 读取未软删的翻译（租户边界由 RLS 承担） |
| InsertSysTranslation | :one | 翻译字段 + 操作者 → SysTranslation | 插入翻译；租户取会话 app.tenant_id，身份四元组唯一冲突映射 AlreadyExists |
| UpdateSysTranslation | :one | status/value/remark + 操作者 + id → SysTranslation | 更新可变字段（不含身份四元组与 tenant_id）；updated_at 由触发器刷新 |
| SoftDeleteSysTranslation | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除翻译 |

### sys_tenant.sql（租户固定 CRUD）

租户树模型：sys_tenant 的每一行归属其父租户（tenant_id 列），RLS 策略
`sys_tenant_tenant_isolation` 允许命中 `tenant_id = app.tenant_id` 或
`id = app.tenant_id` 的行——每个租户可读写自己名下的直接子租户与自身，
树外行天然不可见（上层统一 NotFound）。结构父级与行归属必须一致：创建
时 parent_id 只允许省略（缺省 = 调用方租户）或等于调用方租户。表无
@scope，行级无范围；动态查询能力由 schema/20_tenant.sql 的列注释 DSL
声明（ParseSysTenantQuery 等顶层函数为生成物），本文件只写固定命名查询。
code（身份列，同 dict code / feature flag_key 口径，创建后不可变）与 name
的唯一约束限定未删除行间（部分唯一索引），软删后可复用；更新路径只覆盖
name 等可变列，输入 code 被忽略。
本表是租户树实体面而非受控写面：role.sql 未对其 REVOKE 直写授权，写路径
为普通 DML；API 方法名带 super 表达平台级租户实体管理，不表示绕过 RLS。
删除前置的子租户计数经 98_tenant_guard.sql 的受控判定函数
（fn_tenant_active_child_count，SECURITY DEFINER，只返回计数）：祖父看不到
子租户自己创建的孙行，直接 count 会漏掉它们并产生孤儿。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysTenantByID | :one | id → SysTenant | 按 id 读取未软删的租户（租户树边界由 RLS 承担；树外行等同不存在） |
| LockSysTenantByID | :one | id → SysTenant | 按 id 锁定未软删的租户（FOR UPDATE，更新/停用/恢复/删除路径先锁后写） |
| CountSysTenantActiveChildren | :one | id → 行数 | 经 fn_tenant_active_child_count（SECURITY DEFINER，只返回计数）统计未删除的直接子租户数——租户树 RLS 下祖父看不到孙行，直接 count 会漏掉它们；删除前置校验大于 0 拒绝软删 |
| InsertSysTenant | :one | 租户字段 + 操作者 → SysTenant | 插入租户记录；tenant_id 取会话 app.tenant_id（行归属父租户，RLS WITH CHECK 兜底），未删除行间 code/name 唯一冲突映射 AlreadyExists |
| UpdateSysTenant | :one | status/name/contact_*/expired_at/max_user_count/default_locale + 操作者 + id → SysTenant | 覆盖可变字段（不含 code——身份列；也不含 id/parent_id/tenant_id——身份/结构与归属列）；updated_at 由触发器刷新 |
| SetSysTenantStatus | :one | status/操作者 + id → SysTenant | 置位目标状态（停用 0 / 恢复 1，幂等目标状态写）；updated_at 由触发器刷新 |
| SoftDeleteSysTenant | :execrows | 操作者 + id → 行数 | 置位 deleted_at 软删除租户；活跃子租户校验由调用方在同一事务内先行完成 |

### sys_audit_log.sql（审计日志固定点查）

登录日志 sys_login_log 与操作日志 sys_operation_log 均 append-only 只读：
无写入路径（日志行由业务现场写入）、无更新/删除面；动态查询能力由
schema/70_audit.sql 的列注释 DSL 声明（ParseSysLoginLogQuery 等顶层函数为
生成物），本文件只写管理面的按 id 点查。两表是租户内公共资源：表无
@scope，租户边界由 RLS 承担，行级无范围。

| 查询 | 类型 | 参数 → 返回 | 作用描述 |
|---|---|---|---|
| GetSysLoginLogByID | :one | id → SysLoginLog | 按 id 读取单条登录日志（租户边界由 RLS 承担；表无 @scope，租户内全可见） |
| GetSysOperationLogByID | :one | id → SysOperationLog | 按 id 读取单条操作日志（含操作前后数据快照；租户边界由 RLS 承担） |

### sys_session.sql（会话固定 CRUD）

管理面（治理面）的租户内动态 List 由 schema/80_session.sql 的列注释 DSL
声明（ParseSysSessionQuery 等顶层函数为生成物，令牌哈希列 @internal 协议
不可查），本文件只写认证生命周期与介质维护的固定命名查询。

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
读侧 WHERE 显式带同源租户谓词作应用层兜底（RLS 被关闭时仍不越租户）。

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
| ListAgentRetrievalLogs | :many | 会话（可空）/来源（可空）/limit/offset → 行集 | 按会话/来源过滤分页查询（供运维复现视图按会话/时间回放），created_at DESC, id DESC |
| CountAgentRetrievalLogs | :one | 会话（可空）/来源（可空）→ int64 | 与 List 同条件计数，供分页信封 |

租户边界与审计同型双重声明：RLS（97 域文件的策略）为主，读侧 WHERE
的同源租户谓词（`current_setting('app.tenant_id', true)`）作应用层兜
底——RLS 被关闭时读取仍不越租户。写入侧租户一律取会话变量，调用方
无法指定或伪造归属。


批量过滤 fn_filter_visible_rows 没有对应命名查询：sqlc 解析不了
表函数的返回列，该函数暂无消费方（按需以参数化 SQL 调用）。

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
策略声明在各自的域文件 schema/95_agent_audit.sql（新域独立成文件，
不改共享的 rls.sql）。

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
