-- 用户分配选择器与「管理范围内的整组替换」的固定命名查询。
-- 角色分配/撤销复用 operate.sql 的判定函数与写入查询（fn_can_assign_role /
-- fn_can_assign_roles / fn_can_revoke_role / AssignUserRoles），本文件只补
-- 选择器候选、当前角色集合、撤销写入与指定权限码的角色计数。全部查询经
-- IdentityTx 在租户事务内执行（RLS 承担租户边界），操作者由认证上下文注入。
--
-- 关键字搜索统一用 strpos(lower(col), lower(keyword)) > 0（避免 LIKE
-- 通配符转义），keyword 为空串不过滤；分页以 limit+1 / offset 实现，
-- total 不计算（调用方裁掉多取的一行判定 has_more）。

-- 读取目标用户可分配、且尚未拥有的活跃角色（操作者按 fn_can_assign_role
-- 判定；角色匹配 name/code，按 name/id 稳定排序）；limit 由调用方传
-- page_size+1 以判定 has_more。「尚未拥有」只按活跃映射排除，已撤销的
-- 软删历史行不排除（重复授予由部分唯一索引与 ON CONFLICT 兜底）
-- name: ListAssignableRoleOptions :many
SELECT r.id,
       r."name"
FROM public.sys_role r
WHERE r.status = 1
  AND r.deleted_at IS NULL
  AND public.fn_can_assign_role(sqlc.arg('operator'), r.id, sqlc.arg('target'))
  AND NOT EXISTS (SELECT 1
                  FROM public.sys_user_role_mapping m
                  WHERE m.user_id = sqlc.arg('target')
                    AND m.role_id = r.id
                    AND m.deleted_at IS NULL)
  AND (sqlc.arg('keyword')::varchar = ''
    OR strpos(lower(r."name"), lower(sqlc.arg('keyword'))) > 0
    OR strpos(lower(r.code), lower(sqlc.arg('keyword'))) > 0)
ORDER BY r."name", r.id
LIMIT sqlc.arg('limit')::int4 OFFSET sqlc.arg('offset')::int4;

-- 读取用户的全部活跃角色映射（含停用/软删角色的残留映射；已撤销的
-- 软删行排除），升序稳定排序；供当前集合与 locked 差集计算
-- name: ListUserRoleIDs :many
SELECT role_id
FROM public.sys_user_role_mapping
WHERE user_id = sqlc.arg('user_id')
  AND deleted_at IS NULL
ORDER BY role_id;

-- 按 ID 读取活跃角色（启用且未软删）的 id/name，按 name/id 稳定排序；
-- 用于可管理集合的标签与 desired 集合的存在性校验
-- name: ListActiveRoleOptionsByIDs :many
SELECT r.id,
       r."name"
FROM public.sys_role r
WHERE r.id = ANY (sqlc.arg('ids')::int8[])
  AND r.status = 1
  AND r.deleted_at IS NULL
ORDER BY r."name", r.id;

-- 批量撤销用户-角色映射（软删留痕：deleted_at/deleted_by 记撤销人）：
-- 只撤销操作者按 fn_can_revoke_role（可见性 + 目标范围，放宽角色/目标
-- 状态校验）仍可撤销的角色；调用方要求受影响行数等于请求数，否则整批
-- 回滚
-- name: DeleteUserRoles :execrows
UPDATE public.sys_user_role_mapping AS m
SET deleted_at = CURRENT_TIMESTAMP,
    deleted_by = sqlc.arg('operator')
WHERE m.user_id = sqlc.arg('user_id')
  AND m.role_id = ANY (sqlc.arg('role_ids')::int8[])
  AND m.deleted_at IS NULL
  AND public.fn_can_revoke_role(sqlc.arg('operator'), m.role_id, m.user_id);

-- 统计给定角色中携带指定权限码的有效角色数（角色/菜单均活跃未删，与
-- fn_has_permission 的活跃联结同口径）；供自锁保护判定最终集合是否仍
-- 至少保留一个能授予分配权限的角色
-- name: CountRolesGrantingPermission :one
SELECT count(DISTINCT r.id)
FROM public.sys_role r
         JOIN public.sys_role_menu rm ON rm.role_id = r.id
         JOIN public.sys_menu m ON m.id = rm.menu_id
WHERE r.id = ANY (sqlc.arg('role_ids')::int8[])
  AND r.status = 1
  AND r.deleted_at IS NULL
  AND m.status = 1
  AND m.deleted_at IS NULL
  AND m.permission_code = sqlc.arg('permission_code');

-- 归属用户候选：fn_accessible_user_ids 三态过滤（NULL=全租户、
-- '{}'=无有效角色、数组=可见 ID 并集），只返回活跃且未删除的用户；
-- 关键字匹配 name/nickname，label 取 nickname 优先、空则 name；
-- limit 由调用方传 page_size+1
-- name: ListOwnerUserOptions :many
WITH scope AS (SELECT public.fn_accessible_user_ids(sqlc.arg('operator')) AS ids)
SELECT u.id,
       COALESCE(NULLIF(u.nickname, ''), u."name") AS label
FROM public.sys_user u
         CROSS JOIN scope s
WHERE u.status = 1
  AND u.deleted_at IS NULL
  AND (s.ids IS NULL OR u.id = ANY (s.ids))
  AND (sqlc.arg('keyword')::varchar = ''
    OR strpos(lower(u."name"), lower(sqlc.arg('keyword'))) > 0
    OR strpos(lower(u.nickname), lower(sqlc.arg('keyword'))) > 0)
ORDER BY label, u.id
LIMIT sqlc.arg('limit')::int4 OFFSET sqlc.arg('offset')::int4;

-- 归属部门候选：fn_accessible_dept_ids 三态过滤，只返回活跃且未删除的
-- 部门；关键字匹配 name，label 取 name
-- name: ListOwnerDeptOptions :many
WITH scope AS (SELECT public.fn_accessible_dept_ids(sqlc.arg('operator')) AS ids)
SELECT d.id,
       d."name" AS label
FROM public.sys_dept d
         CROSS JOIN scope s
WHERE d.status = 1
  AND d.deleted_at IS NULL
  AND (s.ids IS NULL OR d.id = ANY (s.ids))
  AND (sqlc.arg('keyword')::varchar = ''
    OR strpos(lower(d."name"), lower(sqlc.arg('keyword'))) > 0)
ORDER BY d."name", d.id
LIMIT sqlc.arg('limit')::int4 OFFSET sqlc.arg('offset')::int4;

-- 归属用户组候选：fn_accessible_group_ids 三态过滤，只返回活跃且未删除
-- 的用户组；关键字匹配 name/code，label 取 name
-- name: ListOwnerGroupOptions :many
WITH scope AS (SELECT public.fn_accessible_group_ids(sqlc.arg('operator')) AS ids)
SELECT g.id,
       g."name" AS label
FROM public.sys_user_group g
         CROSS JOIN scope s
WHERE g.status = 1
  AND g.deleted_at IS NULL
  AND (s.ids IS NULL OR g.id = ANY (s.ids))
  AND (sqlc.arg('keyword')::varchar = ''
    OR strpos(lower(g."name"), lower(sqlc.arg('keyword'))) > 0
    OR strpos(lower(g.code), lower(sqlc.arg('keyword'))) > 0)
ORDER BY g."name", g.id
LIMIT sqlc.arg('limit')::int4 OFFSET sqlc.arg('offset')::int4;
