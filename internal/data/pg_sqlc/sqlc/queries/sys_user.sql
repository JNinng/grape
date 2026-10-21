-- sys_user 用户表固定 CRUD：全部经会话门面在租户事务内执行（RLS 限
-- 租户）。写路径纪律：先锁行取当前归属 → fn_can_operate_row /
-- fn_can_transfer_owner 判定 → 再写入，判断与写入同一事务。

-- 按 id 读取用户（租户内，软删除行不可见）。可见性由调用方经
-- CheckVisible 判定：不可见等同不存在。
-- name: GetSysUserByID :one
SELECT * FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定并读取一行用户（FOR UPDATE）：写操作前取当前归属，
-- 防止并发修改后按旧值判断；软删除行不可操作
-- name: GetSysUserForUpdate :one
SELECT * FROM public.sys_user
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 创建用户。主键 id 由应用层发号（体系表主键无数据库默认值）；可空列
-- 用 sqlc.narg（NULL 原样落库）；租户取自会话 app.tenant_id；
-- created_at/updated_at 由列默认值生成。租户内 name/email/phone 唯一
-- 冲突由调用方映射为 ALREADY_EXISTS
-- name: InsertSysUser :one
INSERT INTO public.sys_user (id, tenant_id, status, "name", nickname, "password", email, phone,
                             remark, locale, owner_user_id, owner_dept_id,
                             owner_user_group_id, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'), sqlc.arg('nickname'),
        sqlc.arg('password'), sqlc.narg('email'), sqlc.narg('phone'), sqlc.narg('remark'),
        sqlc.arg('locale'), sqlc.narg('owner_user_id'), sqlc.narg('owner_dept_id'),
        sqlc.narg('owner_user_group_id'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 覆盖可变字段（不含 password 与归属三元组）。updated_by 记操作者，
-- updated_at 由触发器兜底刷新
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

-- 替换密码哈希；updated_by 记操作者
-- name: SetSysUserPassword :execrows
UPDATE public.sys_user
SET "password" = sqlc.arg('password'), updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id');

-- 软删除：deleted_at 置位后租户内唯一键（name/email/phone）随之可复用
-- name: SoftDeleteSysUser :execrows
UPDATE public.sys_user
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 归属转移落库（fn_can_transfer_owner 判定通过后调用）；
-- 归属参数传 NULL 表示清掉该归属
-- name: UpdateSysUserOwner :exec
UPDATE public.sys_user
SET owner_user_id       = sqlc.narg('owner_user_id'),
    owner_dept_id       = sqlc.narg('owner_dept_id'),
    owner_user_group_id = sqlc.narg('owner_user_group_id'),
    updated_by          = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id');

-- 登录路径专用：按租户内用户名读取口令哈希与状态。调用发生在操作者
-- 身份成立之前，经 TenantSession（只注入 app.tenant_id）执行；软删除
-- 行等同不存在。哈希仅供 biz 口令校验单点使用
-- name: GetUserCredentialByName :one
SELECT id,
       status,
       "name",
       nickname,
       "password" AS password_hash
FROM public.sys_user
WHERE "name" = sqlc.arg('name')
  AND deleted_at IS NULL;
