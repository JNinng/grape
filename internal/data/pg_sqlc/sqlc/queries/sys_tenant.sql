-- 租户固定 CRUD（sys_tenant）。租户树的每一行归属其父租户（tenant_id
-- 列），RLS 策略 sys_tenant_tenant_isolation 允许命中 tenant_id =
-- app.tenant_id 或 id = app.tenant_id 的行：平台租户可读写自身与直接
-- 子租户；孙租户不可见（删除完整性由受控计数函数兜底）。树外行天然
-- 不可见（上层统一 NotFound）。表无 @scope，
-- 行级无范围；动态查询能力由 schema/20_tenant.sql 的列注释 DSL 声明
--（ParseSysTenantQuery 等顶层函数为生成物），本文件只写固定命名查询。
-- code 与 name 的唯一约束限定未删除行间（部分唯一索引），软删后可复用。
-- parent_id 是结构列（创建后不变）；tenant_id 是归属列，插入一律取会话
-- app.tenant_id，调用方无法指定或伪造。

-- 按 id 读取未软删的租户（租户树边界由 RLS 承担；树外行等同不存在）
-- name: GetSysTenantByID :one
SELECT * FROM public.sys_tenant
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的租户（FOR UPDATE，更新/停用/恢复/删除路径先锁后写）
-- name: LockSysTenantByID :one
SELECT * FROM public.sys_tenant
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 统计未删除的直接子租户数（删除前置校验：大于 0 拒绝软删，需自叶向根
-- 逐层删）。经部署面受控判定函数 fn_tenant_active_child_count 读取全局
-- 事实：租户树 RLS 下祖父看不到子租户自己创建的孙行，直接 count 会漏掉
-- 它们并产生孤儿；函数只返回计数、不构成跨租户数据读面（见
-- 98_tenant_guard.sql）。
-- name: CountSysTenantActiveChildren :one
SELECT public.fn_tenant_active_child_count(sqlc.arg('id')::varchar);

-- 插入租户记录；tenant_id 取会话 app.tenant_id（行归属父租户，RLS WITH
-- CHECK 兜底），未删除行间 code/name 唯一冲突映射 AlreadyExists
-- name: InsertSysTenant :one
INSERT INTO public.sys_tenant (id, tenant_id, status, code, "name", parent_id,
                               contact_name, contact_phone, expired_at, max_user_count,
                               default_locale, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('code'),
        sqlc.arg('name'), sqlc.narg('parent_id'), sqlc.narg('contact_name'),
        sqlc.narg('contact_phone'), sqlc.narg('expired_at'), sqlc.narg('max_user_count'),
        sqlc.arg('default_locale'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 覆盖可变字段（status/name/contact_name/contact_phone/expired_at/
-- max_user_count/default_locale）；code 是身份列（同 code 创建后不变的
-- 口径），不经本路径变更；id/parent_id/tenant_id 同属身份/结构/归属列；
-- updated_at 由触发器刷新
-- name: UpdateSysTenant :one
UPDATE public.sys_tenant
SET status         = sqlc.arg('status'),
    "name"         = sqlc.arg('name'),
    contact_name   = sqlc.narg('contact_name'),
    contact_phone  = sqlc.narg('contact_phone'),
    expired_at     = sqlc.narg('expired_at'),
    max_user_count = sqlc.narg('max_user_count'),
    default_locale = sqlc.arg('default_locale'),
    updated_by     = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位目标状态（停用 0 / 恢复 1，幂等目标状态写）；updated_at 由触发器刷新
-- name: SetSysTenantStatus :one
UPDATE public.sys_tenant
SET status     = sqlc.arg('status'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除租户；活跃子租户校验由调用方在同一事务内先行完成
-- name: SoftDeleteSysTenant :execrows
UPDATE public.sys_tenant
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
