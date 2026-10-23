-- 判断操作者能否把指定角色授予目标用户
-- name: CanAssignRole :one
SELECT public.fn_can_assign_role(sqlc.arg('operator'), sqlc.arg('role'), sqlc.arg('target')) AS ok;

-- 逐角色判断角色列表能否授予目标用户，每行返回 (role_id, ok)
-- name: CanAssignRoles :many
SELECT r::int8 AS role_id,
       public.fn_can_assign_role(sqlc.arg('operator'), r, sqlc.arg('target')) AS ok
FROM unnest(sqlc.arg('role_ids')::int8[]) AS r;

-- 判断操作者能否把数据的归属从旧三元组改为新三元组（旧数据可操作
-- 且新归属可达）
-- name: CanTransferOwner :one
SELECT public.fn_can_transfer_owner(sqlc.arg('operator'),
                                    sqlc.narg('old_user'), sqlc.narg('old_dept'), sqlc.narg('old_group'),
                                    sqlc.narg('new_user'), sqlc.narg('new_dept'), sqlc.narg('new_group')) AS ok;

-- 判断操作者能否修改/删除指定归属的数据（读写同权）
-- name: CanOperateRow :one
SELECT public.fn_can_operate_row(sqlc.arg('operator'),
                                 sqlc.narg('owner_user'), sqlc.narg('owner_dept'), sqlc.narg('owner_group')) AS ok;

-- 写入一条用户-角色映射；租户取会话 app.tenant_id，重复分配跳过
-- name: AssignUserRole :exec
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (sqlc.arg('user_id'), sqlc.arg('role_id'), current_setting('app.tenant_id'), sqlc.arg('created_by'))
ON CONFLICT (user_id, role_id) DO NOTHING;

-- 批量写入用户-角色映射，返回实际新增行数；已拥有的角色跳过
-- name: AssignUserRoles :execrows
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
SELECT sqlc.arg('user_id'), r, current_setting('app.tenant_id'), sqlc.arg('created_by')
FROM unnest(sqlc.arg('role_ids')::int8[]) AS r
ON CONFLICT (user_id, role_id) DO NOTHING;

-- 判断用户是否拥有指定角色
-- name: HasUserRole :one
SELECT EXISTS (SELECT 1
               FROM public.sys_user_role_mapping
               WHERE user_id = sqlc.arg('user_id')
                 AND role_id = sqlc.arg('role_id')) AS has;
