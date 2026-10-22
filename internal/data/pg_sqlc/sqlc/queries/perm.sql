-- 权限查询接口命名查询：只读判定与枚举，供权限查询服务层（宿主侧）
-- 调用；拒绝以 false/空集表达，不是错误。单点判断、功能权限码、
-- 可视范围、角色/权限码枚举、范围版本。
-- 按指定维度判断一条归属是否可见/可操作（读写同权）。
-- dims 为维度掩码：1 归属人、2 归属部门、4 归属用户组，可按位组合。
-- 归属参数传 NULL 表示"没有这个归属"（规则见 fn_check_visible）
-- name: CheckVisible :one
SELECT public.fn_check_visible(sqlc.arg('operator'), sqlc.arg('dims'),
                               sqlc.narg('owner_user'), sqlc.narg('owner_dept'), sqlc.narg('owner_group')) AS ok;

-- 判断用户是否持有某功能权限码（角色→菜单→权限码，与数据范围正交）
-- name: HasPermission :one
SELECT public.fn_has_permission(sqlc.arg('operator'), sqlc.arg('code')) AS ok;

-- 一次取回三个维度的可见集合（三件套一条 SQL 同返，省往返）。
-- 三态契约：列 NULL = 全租户不过滤（perm 层翻成 All=true），
-- 空数组 = 什么都看不见，数组 = 可见集合并集（NULL 由调用方翻成全租户）
-- name: GetScope :one
SELECT public.fn_accessible_user_ids(sqlc.arg('operator'))::int8[] AS user_ids,
       public.fn_accessible_dept_ids(sqlc.arg('operator'))::int8[] AS dept_ids,
       public.fn_accessible_group_ids(sqlc.arg('operator'))::int8[] AS group_ids;
-- 枚举有效用户的全部有效角色（展示/审计/外部缓存角色变更）。
-- 用户无效（不存在/禁用/删除）时返回空集
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

-- 枚举有效用户的全部权限码（去重；前端菜单渲染、网关/远程系统缓存）。
-- 用户无效（不存在/禁用/删除）时返回空集
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

-- 取范围版本（缓存失效信号）：租户级 + 用户级一次取回，
-- 缓存有效性 = 两个版本都未变。行不存在时返回 0；RLS 已限本租户
-- name: GetScopeVersion :one
SELECT COALESCE((SELECT v.version
                 FROM public.sys_perm_version v
                 WHERE v.user_id = 0), 0)::int8 AS tenant_version,
       COALESCE((SELECT v.version
                 FROM public.sys_perm_version v
                 WHERE v.user_id = sqlc.arg('user_id')), 0)::int8 AS user_version;
