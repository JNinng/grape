-- 写侧链路命名查询：写前判断（fn_can_*）→ 判定通过后写入（角色分配
-- 落库）。判断与写入放同一事务、先检后写，false 即报错回滚。
-- 归属类参数用 sqlc.narg（生成 *int64 可空指针），NULL 原样传给判断
-- 函数，表示"没有这个归属"，不做零值转换。
-- 判断当前用户能否把某个角色分配给某个用户（规则见 fn_can_assign_role）
-- name: CanAssignRole :one
SELECT public.fn_can_assign_role(sqlc.arg('operator'), sqlc.arg('role'), sqlc.arg('target')) AS ok;

-- 批量版：一次判断多个角色，每个角色一行（role_id, ok），
-- 全部通过才允许批量写入（规则同 fn_can_assign_role；
-- 与 fn_can_assign_roles 函数体一致，直接 SQL 调用时用那个函数）
-- name: CanAssignRoles :many
SELECT r::int8 AS role_id,
       public.fn_can_assign_role(sqlc.arg('operator'), r, sqlc.arg('target')) AS ok
FROM unnest(sqlc.arg('role_ids')::int8[]) AS r;

-- 判断能否把某条数据的归属改成新归属：两头都要过——这条数据本身
-- 有权操作，新归属的人/部门/组也在可见范围内（见 fn_can_transfer_owner）
-- name: CanTransferOwner :one
SELECT public.fn_can_transfer_owner(sqlc.arg('operator'),
                                    sqlc.narg('old_user'), sqlc.narg('old_dept'), sqlc.narg('old_group'),
                                    sqlc.narg('new_user'), sqlc.narg('new_dept'), sqlc.narg('new_group')) AS ok;
-- 写入用户-角色映射（判断通过后调用）。租户自动取当前会话的
-- app.tenant_id；重复分配不报错、不重写
-- name: AssignUserRole :exec
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (sqlc.arg('user_id'), sqlc.arg('role_id'), current_setting('app.tenant_id'), sqlc.arg('created_by'))
ON CONFLICT (user_id, role_id) DO NOTHING;

-- 批量写入用户-角色映射（CanAssignRoles 全部通过后调用），
-- 返回实际新增的行数（已拥有的角色跳过、不报错）
-- name: AssignUserRoles :execrows
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
SELECT sqlc.arg('user_id'), r, current_setting('app.tenant_id'), sqlc.arg('created_by')
FROM unnest(sqlc.arg('role_ids')::int8[]) AS r
ON CONFLICT (user_id, role_id) DO NOTHING;
-- 查某个用户是否已拥有某个角色（分配后的结果确认）
-- name: HasUserRole :one
SELECT EXISTS (SELECT 1
               FROM public.sys_user_role_mapping
               WHERE user_id = sqlc.arg('user_id')
                 AND role_id = sqlc.arg('role_id')) AS has;
