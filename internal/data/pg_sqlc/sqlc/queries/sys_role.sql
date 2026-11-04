-- 角色固定 CRUD（sys_role）与数据范围明细、分发部门、角色-菜单映射的
-- 读写（sys_role_id_scope / sys_role_allowed_dept / sys_role_menu）。
-- 角色是租户内资源：表无 @scope，租户边界由 RLS 承担，行级无范围；动态
-- 查询能力由 schema/40_rbac.sql 的列注释 DSL 声明，本文件只写固定命名
-- 查询。三张明细/映射表不单独立资源，随宿主角色的管理面维护：
-- sys_role_id_scope 与 sys_role_allowed_dept 是配置明细行（软删架构），
-- 设置走差量同步——移除走物理删除（避免重复配置堆积软删残留），补建
-- 记录配置人，既有命中行保留原审计；sys_role_menu 映射行无可变业务字段
-- （仅 created_by/created_at 最小审计），解除授权走物理删除，设置同为
-- 差量同步以保留既有授权的授权人/授权时间。差量在应用层于锁定的角色
-- 事务内计算（先读集合、在 Go 侧对比、逐行执行增删），无并发写者；菜单
-- 侧的读取面联结菜单表活跃过滤（回显=有效授权集），设置以映射物理行为
-- 对账基线、不在集合内的行（含失活菜单的残留授权）物理删除。角色软删后明细/映射行物理
-- 残留：消费侧（fn_accessible_* / fn_can_assign_role / fn_has_permission）
-- 都联结角色表 status=1 AND deleted_at IS NULL 活跃过滤，残留行不产生
-- 可见副作用。

-- 按 id 读取未软删的角色（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysRoleByID :one
SELECT * FROM public.sys_role
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的角色（FOR UPDATE，写路径先锁后写；行级无 @scope，
-- 租户边界由 RLS 承担）
-- name: LockSysRoleByID :one
SELECT * FROM public.sys_role
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入角色记录；租户取会话 app.tenant_id
-- name: InsertSysRole :one
INSERT INTO public.sys_role (id, tenant_id, status, "name", code, role_type,
                             data_scope, scope_dept_id, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.arg('code'), sqlc.arg('role_type'), sqlc.arg('data_scope'), sqlc.narg('scope_dept_id'),
        sqlc.narg('remark'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status/remark；不含 code/role_type——身份列，与
-- data_scope/scope_dept_id——数据范围配置走专项口径）；updated_at 由
-- 触发器刷新
-- name: UpdateSysRole :one
UPDATE public.sys_role
SET "name"     = sqlc.arg('name'),
    status     = sqlc.arg('status'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 更新数据范围配置标量（data_scope/scope_dept_id，SetRoleDataScope 专项
-- 口径；scope_dept_id 为 NULL 表示全租户可分配）；updated_at 由触发器刷新
-- name: UpdateSysRoleDataScope :one
UPDATE public.sys_role
SET data_scope    = sqlc.arg('data_scope'),
    scope_dept_id = sqlc.narg('scope_dept_id'),
    updated_by    = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除角色（明细/映射行物理残留，消费侧活跃过滤排除）；
-- code/name 唯一约束限定未删除行间，软删后可复用
-- name: SoftDeleteSysRole :execrows
UPDATE public.sys_role
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 读取角色的数据范围明细（type/data_id 对，仅活跃行，升序稳定排序）
-- name: ListSysRoleIDScopes :many
SELECT "type", data_id
FROM public.sys_role_id_scope
WHERE role_id = sqlc.arg('role_id')
  AND deleted_at IS NULL
ORDER BY "type", data_id;

-- 读取角色的分发部门集合（仅活跃行，升序稳定排序）
-- name: ListSysRoleAllowedDepts :many
SELECT dept_id
FROM public.sys_role_allowed_dept
WHERE role_id = sqlc.arg('role_id')
  AND deleted_at IS NULL
ORDER BY dept_id;

-- 读取角色的有效授权菜单 ID（联结菜单表活跃过滤：停用/软删菜单的残留
-- 映射不出现在回显集合，与 fn_has_permission 的活跃联结口径一致，回显
-- 集合可原样作为角色-菜单授权设置的输入；升序稳定排序）
-- name: ListSysRoleMenus :many
SELECT rm.menu_id
FROM public.sys_role_menu rm
JOIN public.sys_menu m ON m.id = rm.menu_id AND m.status = 1 AND m.deleted_at IS NULL
WHERE rm.role_id = sqlc.arg('role_id')
ORDER BY rm.menu_id;

-- 读取角色的角色-菜单映射物理行（不过滤菜单活跃性，供设置的差量对账：
-- 不在新集合内的物理行删除，含失活菜单的残留授权；升序稳定排序）
-- name: ListSysRoleMenuGrantsAll :many
SELECT menu_id
FROM public.sys_role_menu
WHERE role_id = sqlc.arg('role_id')
ORDER BY menu_id;

-- 统计引用对象中租户内启用且未删除的用户数（数据范围明细 type=1 的存在
-- 性判定：命中数与请求数不等即存在未知/跨租户/停用/已删除对象，整组拒绝
-- 不写入；与数据范围推导 fn_accessible_* 的活跃联结同口径）
-- name: CountSysRoleScopeUserRefs :one
SELECT count(*)
FROM public.sys_user
WHERE id = ANY (sqlc.arg('ids')::int8[])
  AND status = 1
  AND deleted_at IS NULL;

-- 统计引用对象中租户内启用且未删除的部门数（数据范围明细 type=2、分发
-- 部门与 scope_dept_id 的存在性判定共用，口径同 fn_accessible_* 的活跃联结）
-- name: CountSysRoleScopeDeptRefs :one
SELECT count(*)
FROM public.sys_dept
WHERE id = ANY (sqlc.arg('ids')::int8[])
  AND status = 1
  AND deleted_at IS NULL;

-- 统计引用对象中租户内启用且未删除的用户组数（数据范围明细 type=3 的
-- 存在性判定，口径同 fn_accessible_* 的活跃联结）
-- name: CountSysRoleScopeGroupRefs :one
SELECT count(*)
FROM public.sys_user_group
WHERE id = ANY (sqlc.arg('ids')::int8[])
  AND status = 1
  AND deleted_at IS NULL;

-- 统计引用菜单中租户内启用且未删除的菜单数（角色-菜单授权的存在性判定，
-- 口径同 fn_has_permission 的活跃联结）
-- name: CountSysRoleMenuRefs :one
SELECT count(*)
FROM public.sys_menu
WHERE id = ANY (sqlc.arg('ids')::int8[])
  AND status = 1
  AND deleted_at IS NULL;

-- 清除指定的一条数据范围明细（物理删除避免软删残留；角色行已在本事务
-- 内锁定，差量在应用层算好后逐行执行）
-- name: DeleteSysRoleIDScope :execrows
DELETE FROM public.sys_role_id_scope
WHERE role_id = sqlc.arg('role_id')
  AND "type" = sqlc.arg('type')
  AND data_id = sqlc.arg('data_id')
  AND deleted_at IS NULL;

-- 补建一条数据范围明细（id 为应用层发号的新主键，created_by/updated_by
-- 记配置人，租户取会话 app.tenant_id；角色行已锁定且差量在应用层算好，
-- 不会与活跃行冲突）
-- name: AddSysRoleIDScope :execrows
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, tenant_id, created_by, updated_by)
VALUES (sqlc.arg('id'), 1, sqlc.arg('type'), sqlc.arg('role_id'), sqlc.arg('data_id'),
        current_setting('app.tenant_id'), sqlc.arg('created_by'), sqlc.arg('created_by'));

-- 清除指定的一条分发部门映射（物理删除避免软删残留）
-- name: DeleteSysRoleAllowedDept :execrows
DELETE FROM public.sys_role_allowed_dept
WHERE role_id = sqlc.arg('role_id')
  AND dept_id = sqlc.arg('dept_id')
  AND deleted_at IS NULL;

-- 补建一条分发部门映射（id 为应用层发号的新主键，租户取会话 app.tenant_id）
-- name: AddSysRoleAllowedDept :execrows
INSERT INTO public.sys_role_allowed_dept (id, status, role_id, dept_id, tenant_id, created_by, updated_by)
VALUES (sqlc.arg('id'), 1, sqlc.arg('role_id'), sqlc.arg('dept_id'),
        current_setting('app.tenant_id'), sqlc.arg('created_by'), sqlc.arg('created_by'));

-- 清除指定的一条角色-菜单映射（物理删除；解除授权即删除）
-- name: DeleteSysRoleMenu :execrows
DELETE FROM public.sys_role_menu
WHERE role_id = sqlc.arg('role_id')
  AND menu_id = sqlc.arg('menu_id');

-- 补建一条角色-菜单授权；租户取会话 app.tenant_id，主键冲突跳过
-- （保留原授权人/授权时间）
-- name: AddSysRoleMenu :execrows
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (sqlc.arg('role_id'), sqlc.arg('menu_id'), current_setting('app.tenant_id'), sqlc.arg('created_by'))
ON CONFLICT (role_id, menu_id) DO NOTHING;
