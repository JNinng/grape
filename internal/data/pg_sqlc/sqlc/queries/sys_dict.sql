-- 字典固定 CRUD（sys_dict_type / sys_dict_data）。字典是租户内公共资源：
-- 表无 @scope，租户边界由 RLS 承担，行级无范围；动态查询能力由
-- schema/60_dict.sql 的列注释 DSL 声明（ParseSysDictTypeQuery 等顶层函数
-- 为生成物），本文件只写固定命名查询。两级同置一文件：数据随宿主类型的
-- 管理面维护——数据创建路径先锁宿主类型行再写入（同事务内判定宿主存在
-- 且未删除），类型删除路径在同一事务内校验活跃数据后软删。code（类型）
-- 与 value（数据）的唯一约束限定未删除行间（部分唯一索引），软删后可复用；
-- 类型软删后数据行物理残留，消费侧按需过滤宿主活跃性。

-- 按 id 读取未软删的字典类型（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysDictTypeByID :one
SELECT * FROM public.sys_dict_type
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的字典类型（FOR UPDATE，类型写路径与数据创建路径先锁后写）
-- name: LockSysDictTypeByID :one
SELECT * FROM public.sys_dict_type
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入字典类型记录；租户取会话 app.tenant_id，租户内未删除类型间 code 唯一
-- 冲突映射 AlreadyExists
-- name: InsertSysDictType :one
INSERT INTO public.sys_dict_type (id, tenant_id, status, "name", code, remark,
                                  created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.arg('code'), sqlc.narg('remark'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status/remark；不含 code——身份列不经本路径变更）；
-- updated_at 由触发器刷新
-- name: UpdateSysDictType :one
UPDATE public.sys_dict_type
SET "name"     = sqlc.arg('name'),
    status     = sqlc.arg('status'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 统计字典类型下未删除字典数据数（类型删除前置校验：计数大于 0 即拒绝软删，
-- 先清空该类型下的数据）
-- name: CountSysDictDataByType :one
SELECT count(*)
FROM public.sys_dict_data
WHERE dict_type_id = sqlc.arg('dict_type_id')
  AND deleted_at IS NULL;

-- 置位 deleted_at 软删除字典类型；活跃数据校验由调用方在同一事务内先行完成
-- name: SoftDeleteSysDictType :execrows
UPDATE public.sys_dict_type
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 读取未软删的字典数据（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysDictDataByID :one
SELECT * FROM public.sys_dict_data
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的字典数据（FOR UPDATE，数据更新/删除路径先锁后写）
-- name: LockSysDictDataByID :one
SELECT * FROM public.sys_dict_data
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入字典数据记录；租户取会话 app.tenant_id，同类型下未删除数据间 value
-- 唯一冲突映射 AlreadyExists（宿主存在性由调用方先锁类型行判定）
-- name: InsertSysDictData :one
INSERT INTO public.sys_dict_data (id, tenant_id, status, dict_type_id, label, value,
                                  value_type, sort_order, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('dict_type_id'),
        sqlc.arg('label'), sqlc.arg('value'), sqlc.arg('value_type'), sqlc.arg('sort_order'),
        sqlc.narg('remark'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（label/status/sort_order/remark；不含 value/value_type 与
-- dict_type_id——值语义列与宿主归属列不经本路径变更）；updated_at 由触发器刷新
-- name: UpdateSysDictData :one
UPDATE public.sys_dict_data
SET label      = sqlc.arg('label'),
    status     = sqlc.arg('status'),
    sort_order = sqlc.arg('sort_order'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除字典数据（宿主类型不受影响）
-- name: SoftDeleteSysDictData :execrows
UPDATE public.sys_dict_data
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
