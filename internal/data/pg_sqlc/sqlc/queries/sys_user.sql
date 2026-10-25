-- 按 id 读取未软删的用户（不做数据范围过滤；仅限同事务内已锁定并判定的回读）
-- name: GetSysUserByID :one
SELECT * FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 读取数据范围内可见的用户；permission_code 非空时同时校验功能权限，NULL 表示不校验
-- name: GetSysUserPermitted :one
SELECT *
FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_check_visible(sqlc.arg('operator'), 7::int2,
                              owner_user_id, owner_dept_id, owner_user_group_id)
  AND (sqlc.narg('permission_code')::varchar IS NULL
    OR public.fn_has_permission(sqlc.arg('operator'), sqlc.narg('permission_code')::varchar));

-- 按 id 锁定数据范围内可操作的用户；permission_code 非空时同时校验功能权限，NULL 表示不校验
-- name: LockSysUserPermitted :one
SELECT *
FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_can_operate_row(sqlc.arg('operator'),
                                owner_user_id, owner_dept_id, owner_user_group_id)
  AND (sqlc.narg('permission_code')::varchar IS NULL
    OR public.fn_has_permission(sqlc.arg('operator'), sqlc.narg('permission_code')::varchar))
FOR UPDATE;

-- 插入用户记录；租户取会话 app.tenant_id
-- name: InsertSysUser :one
INSERT INTO public.sys_user (id, tenant_id, status, "name", nickname, "password", email, phone,
                             remark, locale, owner_user_id, owner_dept_id,
                             owner_user_group_id, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'), sqlc.arg('nickname'),
        sqlc.arg('password'), sqlc.narg('email'), sqlc.narg('phone'), sqlc.narg('remark'),
        sqlc.arg('locale'), sqlc.narg('owner_user_id'), sqlc.narg('owner_dept_id'),
        sqlc.narg('owner_user_group_id'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（不含 password 与归属三元组）；updated_at 由触发器刷新
-- name: UpdateSysUser :one
UPDATE public.sys_user
SET status   = sqlc.arg('status'),
    "name"   = sqlc.arg('name'),
    nickname = sqlc.arg('nickname'),
    email    = sqlc.narg('email'),
    phone    = sqlc.narg('phone'),
    remark   = sqlc.narg('remark'),
    locale   = sqlc.arg('locale'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
RETURNING *;

-- 替换口令哈希
-- name: SetSysUserPassword :execrows
UPDATE public.sys_user
SET "password" = sqlc.arg('password'), updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id');

-- 置位 deleted_at 软删除用户
-- name: SoftDeleteSysUser :execrows
UPDATE public.sys_user
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 更新用户归属三元组；归属参数为 NULL 表示清空该归属
-- name: UpdateSysUserOwner :exec
UPDATE public.sys_user
SET owner_user_id       = sqlc.narg('owner_user_id'),
    owner_dept_id       = sqlc.narg('owner_dept_id'),
    owner_user_group_id = sqlc.narg('owner_user_group_id'),
    updated_by          = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id');

-- 登录路径专用：按用户名读取口令哈希与状态（仅未软删行）
-- name: GetUserCredentialByName :one
SELECT id,
       status,
       "name",
       nickname,
       "password" AS password_hash
FROM public.sys_user
WHERE "name" = sqlc.arg('name')
  AND deleted_at IS NULL;
