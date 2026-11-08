# 权限体系

多租户 RBAC 数据权限的落点规范：每类约束由哪一层强制、数据范围如何推导、
读写判定路径、错误语义与新增资源清单。

SQL 对象签名与索引见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)，
部署顺序见 [deploy_db.md](deploy_db.md)。

## 三层约束

| 约束 | 回答的问题 | 执行者 | 失效姿态 |
| --- | --- | --- | --- |
| 租户边界 | 数据属于哪个租户 | PostgreSQL RLS（ENABLE + FORCE，策略随域文件走） | 未设 `app.tenant_id` → 全拒 |
| 行级数据范围 | 能看见、操作哪些行 | 读：`@scope` 与 `fn_check_visible`；写：`fn_can_*` | 缺 Scope 报错；判定 false 拒绝 |
| 功能权限码 | 能否调用某操作 | 入口层按操作名查码表调 `fn_has_permission` | 判定 false 返回 403 |

租户表清单与各域策略位置见 [sqlc/README.md](internal/data/pg_sqlc/sqlc/README.md)。

- 租户边界由数据库兜底，应用层不重复检查；RLS 不承担行级数据范围。
- 行级数据范围依赖目标行与用户权限关系，写侧判定必须在库内、与写入同一
  事务完成。
- 功能权限码与行无关，在入口层判定，不进入仓储方法。
- 操作者与 Scope 只从服务端认证上下文注入（`IdentityTx.Auth()`）；请求体
  中的 `tenantId`、`owner*`、`scope` 一律不可信。

Agent 设施面（`agent` schema，如向量表 `agent.agent_document`）不走 RLS
与 `@scope`：租户边界由应用层适配器强制——所有读写从认证 ctx 提取身份、
每条 SQL 带租户过滤、缺身份 fail-closed，身份同时写入行与 metadata，调用
方伪造不生效。建表脚本附 RLS 语句供按需启用，属第二道防线、不作隔离前提；
集成测试以无 RLS 场景断言隔离仍成立。跨租户管理操作（在线重建索引、租户级
全量清理）属 super 管理口径：向量层仅留占位，不暴露普通入口。

## 数据范围推导

可见范围只由三个函数推导，入参只有用户 ID：

| 函数 | 维度 |
| --- | --- |
| `fn_accessible_user_ids` | 归属人 |
| `fn_accessible_dept_ids` | 归属部门 |
| `fn_accessible_group_ids` | 归属用户组 |

角色、`data_scope` 1–7、部门闭包与用户组成员全部在库内查，调用方不传
权限参数；撤销角色下一次查询立即生效。

| 返回值 | 含义 |
| --- | --- |
| `NULL` | 有全租户角色（`data_scope=7`），该维度不过滤 |
| `'{}'` | 用户无效或无有效角色，什么都看不到 |
| 数组 | 可见 ID 并集 |

列表守卫、单行判定与写前判定共用同一口径：

- 三个维度 OR，任一非空归属命中对应可见集合即可见；
- 三个归属全空的数据只有全租户角色可见；
- 用户无效什么都看不到。

`@scope` 挂在两类表上：有归属三元组（`owner_user_id` / `owner_dept_id` /
`owner_user_group_id`）的 `basic` 与 `sys_user`（守卫取
`fn_accessible_*` 三件套），以及 `sys_role`（守卫取
`fn_visible_role_ids`，与写侧 `fn_can_see_role` 共用 A AND G 口径）；
其余表租户内全可见。修改推导口径时，列表守卫、单行判定、写前判定三处
必须同改。

角色可见性（A AND G）：A=目标角色在 `sys_role_allowed_dept` 的有效映射
部门集合（M）为空恒真，非空时要求 M ∩ 操作人可见部门集合 ≠ ∅；命中口径
是「可见部门集合」而非「所属部门」，且命中不能替代 G。G=组织可见范围
`scope_dept_id`（S）为 NULL 时仅全租户操作人（O_d 为 NULL）可见；S 非空
时要求 S ∈ O_d 且目标角色 `data_scope` 档位满足对应条件（D=1/2 操作人
存在有效角色 D≠1；D=3 存在 D∉{1,2}；D=4/5/6 该角色的 ID 范围明细全部落在
操作人可见集合内；D=7 仅全租户操作人）。角色未软删即可见（不按启用状态
过滤——管理面需要看到停用角色；候选接口自行叠加启用过滤）。

## 写形态

锁定、取行与行权限判定合并为一条语句：`LockXxxPermitted` 用
`fn_can_operate_row` 过滤并 `FOR UPDATE`，返回的行即已锁定且可操作；
业务逻辑与写入在同一 `IdentityTx` 内执行。查询定义见
[queries/sys_user.sql](internal/data/pg_sqlc/sqlc/queries/sys_user.sql)。

- 无行 = 不存在、不可见或权限码不匹配，统一 NotFound；不可见行不会被锁定。
- READ COMMITTED 下 `FOR UPDATE` 对并发更新后的行复核 `WHERE`，不按旧
  归属放行。
- 判定与写入同事务，行锁持有到提交。
- 业务授权（新归属可达、角色分发）用专用函数单独判定，失败返回 Forbidden。
- 可见性谓词只在判定函数里写一份，`UPDATE` / `DELETE` 不重复谓词。
- 不在 biz / Go 里重算 `data_scope`。

`permission_code` 可空：传值则同时校验功能权限，传 NULL 只校验行级数据
范围；空串按无此权限处理。

| 业务授权 | 判定函数 |
| --- | --- |
| 修改 / 删除 | `fn_can_operate_row` |
| 挂新归属（创建路径） | `fn_can_set_owner`（归属对象存在、启用且在可见范围） |
| 修改归属 | `fn_can_transfer_owner`（旧归属可操作 + 新归属经 `fn_can_set_owner`） |
| 分配角色 | `fn_can_assign_role` / `fn_can_assign_roles`（可见性 + 目标部门范围 M ∩ subtree(S) + 分配权限码） |
| 撤销角色 | `fn_can_revoke_role`（沿用可见性与目标范围，放宽角色/目标状态校验） |
| 角色读面 | `fn_can_see_role`（详情）+ `fn_visible_role_ids`（List @scope 守卫），共用 A AND G |
| 其他业务授权 | 在 `operate.sql` 新增专用 `fn_can_*`，配命名查询并同步索引 |

### 角色整组替换（SetUserRoles）

`SetUserRoles` 只表达调用方可管理范围内的目标集合：locked 角色（新分配
判定不可达的既有映射，含范围外与停用/软删角色的残留映射）原样保留，
请求触及 locked 返回 Forbidden，绝不误删调用方看不见的分配。不变量：

1. 目标按 ID 定位：`LockSysUserByID` 锁定目标行——新分配不要求目标可见，
   无行等同不存在（NotFound）；行锁与 `AssignRoles` 共用，串行化并发
   分配/撤销。
2. managed/locked 分区：`currentAll`（全部活跃映射）→ `activeCurrent`
   （活跃角色）→ `fn_can_assign_role` 逐角色判定；
   `locked = currentAll - manageable`。
3. `desired` 必须全部是活跃角色；新增要求 `fn_can_assign_role=true`；撤销
   按 `fn_can_revoke_role` 判定并软删（放宽角色/目标状态校验），
   `DeleteUserRoles` 在 SQL 内再判一次，受影响行数不等同样回滚。
4. 版本 `sha256("<user_id>|<升序 manageable ID 逗号列表>")`，与
   `expected_version` 不一致返回 Conflict（`USER_ROLE_SET_CHANGED`），
   客户端应重载而非重试。
5. 自锁保护：目标为操作者本人时，最终集合（locked ∪ desired）至少保留
   一个携带 `sys:user:assign-role` 的有效角色，否则 Forbidden。

### 选择器候选（Option 契约）

角色候选 `ListAssignableRoles` 按 ID 判目标存在（分配不要求目标可见），
再以 `fn_can_assign_role` 过滤活跃、可分配且目标尚未拥有的角色；当前角色
`ListUserRoles` 同样按 ID 读取。三个归属候选（`ListOwnerUserOptions` /
`ListOwnerDeptOptions` / `ListOwnerGroupOptions`）无目标参数，直接按
`fn_accessible_*` 三态过滤活跃对象。

候选不复用 `ListUsers`/`ListRoles`/`ListDepts`/`ListUserGroups`：动态查询
的 `@scope` 按行归属/可见性过滤，不等于「人员可见集」或「可分配角色集」；
`where` 恒为 AND 也无法做 name OR code 的跨字段搜索。分页以 limit+1 判定
`has_more`，`total` v1 不返回。功能码分离：角色管理读面用 `sys:role:query`，
分配候选与当前角色属分配面用 `sys:user:assign-role`；可见性判定是数据层，
不替代功能码。

## 读形态

| 场景 | 路径 |
| --- | --- |
| 列表 / 计数 | 动态查询协议；生成代码在 WHERE 头部注入 `@scope` 三维守卫，缺 Scope fail-closed；List 与 Count 共用守卫，`total` 与 `items` 权限视图一致 |
| 单行 / 详情 | `GetXxxPermitted`：`fn_check_visible`（掩码 7）+ 可空权限码，一条查询完成取行与判定；不可见 → NotFound |
| 选择器候选 | 专用命名查询：角色候选按 ID 判目标存在 + `fn_can_assign_role`；归属候选直接按 `fn_accessible_*` 三态过滤；不复用动态 List |
| 外部批量过滤 | `fn_filter_visible_rows`；只读，拒绝表现为空集，不是错误 |

- 软删过滤由表注释 `@filter` 在解析期注入；固定命名查询在 SQL 里自写
  `deleted_at IS NULL`。
- 不手写绕过 `@scope` 的聚合查询；专用列表走专用命名查询，守卫仍取自
  `fn_accessible_*`。
- 单行读用 `GetXxxPermitted` 一次完成取行与判定。
- 登录凭据 `GetUserCredentialByName` 走 `TenantTx`：身份成立前只注入租户，
  不做数据范围判定，仅返回未软删行的口令哈希。

## 错误语义

| 情况 | 返回 | 理由 |
| --- | --- | --- |
| 行不存在 / 数据范围不可见 / 权限码不匹配 | NotFound | 不泄露存在性；未命中返回 `pgx.ErrNoRows`，由仓储 `mapErr` 统一映射 |
| 看得见但业务授权不允许（分配角色、转归属到不可达对象） | Forbidden（`ErrUserAccessDenied`） | 存在性不敏感，明确拒绝 |
| 整组替换触及 locked、新增不可分配、撤销不可撤销或触发自锁 | Forbidden（`ErrUserAccessDenied`） | 存在性不敏感；整批回滚，不产生部分写入 |
| 分配类接口的目标不存在或已软删 | NotFound（按 ID 锁定/判定无行） | 分配不要求目标可见，但目标必须存在 |
| 整组替换的 `expected_version` 不一致 | Conflict（`USER_ROLE_SET_CHANGED`） | 并发改动要求重载，不是权限拒绝；不自动重试写入 |
| `expected_version` 缺失/空串、`user_id`/`role_ids` 非正 | InvalidArgument（`ErrUserInvalidArgument`） | 请求参数非法 |
| OptionQuery 越界（page<1、page_size<1 或 >100） | 归一化后正常返回 | page→1、page_size→20/100 |
| 只读判定返回 false | 不是错误 | 调用方按布尔值处理 |

取行查询里的权限码校验未命中同样落到 NotFound；需要 403 的功能权限拒绝应
在入口层单独判 `fn_has_permission`，取行查询传 NULL。权限失败不用「0 行」
隐式表达：0 行既可能是没权限，也可能是并发删除，仓储层无法区分。

## 功能权限码

功能权限码与数据范围正交：只看角色 → 菜单 → 权限码，不管能看见哪些行。

落点：入口层判定中间件（[middleware/permission.go](internal/middleware/permission.go)）
按操作名查码表调 `fn_has_permission`，不通过返回 `ErrPermissionDenied`
（403）。码表即 `biz` 权限码常量，与部署种子权限点一一对应；管理读端点
并入 `:query` 码、结构移动并入 `:update` 码、授权类写动作单设码。Agent
工具授权手写端点用 `agent:grant:query` / `agent:grant:revoke`；本人授权
列表与本人吊销不设码（认证身份即凭据），判定收在 `biz.GrantUsecase`。

知识库与条目共用 `sys:knowledge:query/create/update/delete`（重建索引并入
`:update`；模型选项端点 `ListModelOptions` 也用 `:query`，避免知识库管理员
被迫持有 `sys:model:query`）。模型目录租户行用 `sys:model:query/create/
update/delete`；平台行写面另设 `sys:model:platform:create/update/delete`，
只应由平台管理员持有。`sys_model` 的平台行读面对所有租户开放（RLS 读策略
`tenant_id='' OR 本租户`），平台写只能经受控 SECURITY DEFINER 函数。

- 与行无关，可缓存；失效用 `sys_perm_version`（租户级 + 用户级版本）。
- 平台信任边界：`sys:config:global:*` 写码只应由平台根租户持有，且写路径
  强制校验调用方租户为根租户（`parent_id IS NULL`）——子租户即使自造或
  持有写码也返回 403（`CONFIG_PLATFORM_REQUIRED`）。读端点不设此守卫。
- 权限码可空：传值则在取行时一并校验，NULL 只校验数据范围；空串按无此
  权限处理。仓储缺省传 NULL；入口层强制功能权限时把码透传到仓储参数。
- 取行查询内的权限码拒绝表现为 NotFound；不与 `@scope` 合并成一层。

码表外的端点（认证生命周期、agent 业务面与权限码查询端点）只受令牌校验
与行级判定约束。

## 新增资源 / 操作速查

1. **建表**：`tenant_id`（列注释 `[@internal]`，不参与动态 schema/投影/
   过滤/排序；`sys_tenant.tenant_id` 为租户树结构例外，保留
   `[@dynamic: exact]`）+ 审计列 + 归属三元组；表注释声明 `@scope`、
   `@filter: deleted_at is_null`、`@pk`；分页与 UI 显隐元数据在 sqlc.yaml
   的 `codegen.options.page` / `codegen.options.ui` 声明；`owner_*` 建索引；
   `rls.sql` 补策略并 ENABLE + FORCE。
2. **列表 / 计数**：复用 `query.v1.Query` 动态协议，Scope 由 `IdentityTx`
   注入。
3. **详情**：`GetXxxPermitted`（`fn_check_visible` 掩码 7，权限码可空），
   不可见返回 NotFound。
4. **修改 / 删除 / 改密**：`LockXxxPermitted`（`fn_can_operate_row` +
   `FOR UPDATE`，权限码可空）→ 写入；业务授权另判。
5. **修改归属**：`LockXxxPermitted`（旧归属可操作）+
   `fn_can_transfer_owner`（新归属可达）→ 写入。
6. **批量写**：单事务内逐行锁 + 判，或新增批量判定函数；外部批量读过滤用
   `fn_filter_visible_rows`。
7. **无归属三元组的表**：不声明 `@scope`，行级无范围；写侧靠功能权限码
   或数据库授权。`sys_menu` / `sys_config` 对运行角色 REVOKE 写权限，由
   部署面 SECURITY DEFINER 受控写函数承担（`fn_menu_create/update/move/delete`、
   `fn_config_create/update/delete`）：函数内复位或校验 `app.tenant_id`
   租户边界；API 写方法按 `super` 约定命名（`CreateMenuSuper` 等），读
   方法不带。
8. **文档化例外**：登录点查与登录日志追加（`TenantTx`）、令牌校验/登出/
   刷新（`TokenTx`）、介质维护（`SECURITY DEFINER` 清理）、受控写面与租户
   删除前置受控计数（`fn_tenant_active_child_count`）是仅有的绕过路径，
   不得扩展到其他读写。受控计数以 DEFINER 读取全局事实、只返回计数不返回
   行数据，函数内校验 `app.tenant_id` 已设置且目标对调用方可见；口径见
   [data/README.md](internal/data/README.md#租户管理与删除孤儿判定)。
9. **测试**：每个资源覆盖全租户看全集、范围角色看子集、范围外读 NotFound、
   范围外写不改行、转归属不可达 Forbidden、缺 Scope fail-closed、未设
   `app.tenant_id` 全拒。
10. **约定**：有权限的领域方法默认按权限处理（Scope / RLS 强制生效），
    无需额外标注；只有「身份已成立、接口有意绕过其租户/数据范围约束」的
    方法才在名称带 `super`（如管理端跨租户查询），并配受限端点与跨范围
    测试。
    - 显式例外，不使用 `super`：身份成立前的凭据/令牌查询与令牌维度撤销
      （`GetCredentialForLogin`、`SessionRepo.Validate` /
      `RevokeByAccessTokenHash` / `Refresh`）、设施维护面
      （`FacilityMaintenance.Maintain`）、已授权事务内的 data 层内部回读
      （`GetSysUserByID`）。
    - `super` 的第二种用法（平台级实体管理，不是绕过例外）：`sys_tenant`
      管理面方法名带 `super`（`CreateTenantSuper` / `GetTenantSuper` /
      `ListTenantsSuper` / `UpdateTenantSuper` / `DeleteTenantSuper` /
      `DisableTenantSuper` / `RestoreTenantSuper`）表达「被管理的是一整个
      租户实体」，而非调用方租户内的业务数据。租户树边界仍由 RLS 承担
      （允许命中 `tenant_id = app.tenant_id` 或 `id = app.tenant_id`，树外
      行等同不存在）；创建时 `parent_id` 只允许省略或等于调用方租户。默认
      种子只向平台根租户授予 `sys:tenant:*`，子租户自助管理需另行铺菜单/
      角色。本面无新增 RLS 绕过机制或 SECURITY DEFINER 写函数（删除前置的
      只读计数例外见第 8 条），写路径是普通 DML，不因命名放宽审计与测试
      口径。
    - 区分要点：绕过类 super 的方法刻意不经过租户/范围约束；本类 super 的
      方法只是对象粒度是租户实体、约束照常生效。
    - SQL 命名查询不参与该约定；内部回读靠使用边界注释约束，不在 biz
      接口上暴露。

Agent 审计写入面（工具调用、授权确认、登录与操作日志）见
[replay.md](replay.md)。
