-- 按维度掩码判断归属是否可见/可操作（读写同权）；dims 1=归属人、
-- 2=归属部门、4=归属用户组，可按位组合；归属参数为 NULL 表示无该归属
-- name: CheckVisible :one
SELECT public.fn_check_visible(sqlc.arg('operator'), sqlc.arg('dims'),
                               sqlc.narg('owner_user'), sqlc.narg('owner_dept'), sqlc.narg('owner_group')) AS ok;

-- 判断用户是否持有指定功能权限码（角色→菜单→权限码，与数据范围正交）
-- name: HasPermission :one
SELECT public.fn_has_permission(sqlc.arg('operator'), sqlc.arg('code')) AS ok;

-- 一次取回三个维度的可见集合；列 NULL 表示全租户不过滤，
-- 空数组表示无可见项，数组为可见集合并集
-- name: GetScope :one
SELECT public.fn_accessible_user_ids(sqlc.arg('operator'))::int8[] AS user_ids,
       public.fn_accessible_dept_ids(sqlc.arg('operator'))::int8[] AS dept_ids,
       public.fn_accessible_group_ids(sqlc.arg('operator'))::int8[] AS group_ids;

-- 枚举用户的有效角色；用户无效（不存在/禁用/删除）时返回空集
-- name: GetUserRoles :many
SELECT r.*
FROM public.sys_role r
         JOIN public.sys_user_role_mapping m ON m.role_id = r.id
         JOIN public.sys_user u ON u.id = m.user_id
WHERE m.user_id = sqlc.arg('user_id')
  AND u.status = 1
  AND u.deleted_at IS NULL
  AND r.status = 1
  AND r.deleted_at IS NULL
ORDER BY r.id;

-- 枚举用户的有效权限码（去重）；用户无效（不存在/禁用/删除）时返回空集
-- name: GetPermissionCodes :many
SELECT DISTINCT mu.permission_code
FROM public.sys_user u
         JOIN public.sys_user_role_mapping m ON m.user_id = u.id
         JOIN public.sys_role r ON r.id = m.role_id
    AND r.status = 1 AND r.deleted_at IS NULL
         JOIN public.sys_role_menu rm ON rm.role_id = r.id
         JOIN public.sys_menu mu ON mu.id = rm.menu_id
    AND mu.status = 1 AND mu.deleted_at IS NULL
WHERE u.id = sqlc.arg('user_id')
  AND u.status = 1
  AND u.deleted_at IS NULL
ORDER BY mu.permission_code;

-- 读取租户级与用户级范围版本；行不存在时返回 0
-- name: GetScopeVersion :one
SELECT COALESCE((SELECT v.version
                 FROM public.sys_perm_version v
                 WHERE v.user_id = 0), 0)::int8 AS tenant_version,
       COALESCE((SELECT v.version
                 FROM public.sys_perm_version v
                 WHERE v.user_id = sqlc.arg('user_id')), 0)::int8 AS user_version;
