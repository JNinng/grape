-- 模型目录固定 CRUD（sys_model）。单表双态：tenant_id='' 为平台行（所有
-- 租户可读），非空为租户行并覆盖同 model_key 平台行。读面走 RLS
-- 「本租户行 + 平台行」；租户行走普通 DML，平台行经受控 SECURITY DEFINER
-- 函数（98_model_write.sql）写入。api_key_cipher 只写不读：固定查询返回
-- 整行仅供解析与内部回读，动态投影与服务响应不暴露该列。

-- 按 model_key 读取本租户模型行（解析顺序第一级；状态由调用方判定，
-- 停用行命中后 fail-closed，不回落到平台行）
-- name: GetSysModelForTenantByKey :one
SELECT * FROM public.sys_model
WHERE tenant_id = current_setting('app.tenant_id')
  AND model_key = sqlc.arg('model_key')
  AND deleted_at IS NULL;

-- 按 model_key 读取平台模型行（解析顺序第二级；RLS 读策略放行平台行；
-- 状态由调用方判定，停用行命中后 fail-closed）
-- name: GetSysModelPlatformByKey :one
SELECT * FROM public.sys_model
WHERE tenant_id = ''
  AND model_key = sqlc.arg('model_key')
  AND deleted_at IS NULL;

-- 按 id 读取未软删模型（本租户行或平台行；详情/内部回读用）
-- name: GetSysModelByID :one
SELECT * FROM public.sys_model
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定本租户模型行（租户写路径先锁后写；平台行不命中）
-- name: LockSysModelTenantByID :one
SELECT * FROM public.sys_model
WHERE id = sqlc.arg('id')
  AND tenant_id = current_setting('app.tenant_id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入租户模型行；租户取会话 app.tenant_id，租户内未删除行间 model_key
-- 唯一冲突映射 AlreadyExists
-- name: InsertSysModelTenant :one
INSERT INTO public.sys_model (id, tenant_id, status, kind, "name", model_key, provider,
                              base_url, model, dimension, timeout_ms,
                              context_window, max_output_tokens,
                              api_key_cipher, api_key_env, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('kind'),
        sqlc.arg('name'), sqlc.arg('model_key'), sqlc.arg('provider'),
        sqlc.narg('base_url'), sqlc.arg('model'), sqlc.narg('dimension'), sqlc.narg('timeout_ms'),
        sqlc.narg('context_window'), sqlc.narg('max_output_tokens'),
        sqlc.narg('api_key_cipher'), sqlc.narg('api_key_env'), sqlc.narg('remark'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新租户模型行可变字段（kind/model_key 是身份列，创建后不变）
-- name: UpdateSysModelTenant :one
UPDATE public.sys_model
SET status         = sqlc.arg('status'),
    "name"         = sqlc.arg('name'),
    provider       = sqlc.arg('provider'),
    base_url       = sqlc.narg('base_url'),
    model          = sqlc.arg('model'),
    dimension      = sqlc.narg('dimension'),
    timeout_ms     = sqlc.narg('timeout_ms'),
    context_window = sqlc.narg('context_window'),
    max_output_tokens = sqlc.narg('max_output_tokens'),
    api_key_cipher = sqlc.narg('api_key_cipher'),
    api_key_env    = sqlc.narg('api_key_env'),
    remark         = sqlc.narg('remark'),
    updated_by     = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND tenant_id = current_setting('app.tenant_id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除本租户模型行（平台行不命中）
-- name: SoftDeleteSysModelTenant :execrows
UPDATE public.sys_model
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND tenant_id = current_setting('app.tenant_id')
  AND deleted_at IS NULL;

-- 新建平台模型行：调用受控写函数 fn_model_platform_create（tenant_id 固定
-- 空串，函数内校验认证事务纪律）
-- name: InsertSysModelPlatform :one
SELECT *
FROM public.fn_model_platform_create(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('kind'),
                                     sqlc.arg('name'), sqlc.arg('model_key'), sqlc.arg('provider'),
                                     sqlc.narg('base_url'), sqlc.arg('model'), sqlc.narg('dimension'),
                                     sqlc.narg('timeout_ms'), sqlc.narg('context_window'),
                                     sqlc.narg('max_output_tokens'), sqlc.narg('api_key_cipher'),
                                     sqlc.narg('api_key_env'), sqlc.narg('remark'), sqlc.arg('operator'));

-- 覆盖平台模型行可变字段：调用受控写函数 fn_model_platform_update；目标
-- 未命中函数返回 NULL 复合行，以 id IS NOT NULL 过滤成空集（映射 NotFound）
-- name: UpdateSysModelPlatform :one
SELECT *
FROM public.fn_model_platform_update(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('name'),
                                     sqlc.arg('provider'), sqlc.narg('base_url'), sqlc.arg('model'),
                                     sqlc.narg('dimension'), sqlc.narg('timeout_ms'),
                                     sqlc.narg('context_window'), sqlc.narg('max_output_tokens'),
                                     sqlc.narg('api_key_cipher'), sqlc.narg('api_key_env'),
                                     sqlc.narg('remark'), sqlc.arg('operator'))
WHERE id IS NOT NULL;

-- 软删除平台模型行：调用受控写函数 fn_model_platform_delete；返回删除行数，
-- 0 即目标未命中（映射 NotFound）
-- name: SoftDeleteSysModelPlatform :one
SELECT public.fn_model_platform_delete(sqlc.arg('id'), sqlc.arg('operator')) AS deleted_count;

-- 列出可见模型（本租户行 + 平台行，含停用/各类别；管理面固定列表，
-- 按类别、model_key、平台行在后稳定排序）
-- name: ListSysModelAll :many
SELECT * FROM public.sys_model
WHERE deleted_at IS NULL
  AND kind = COALESCE(sqlc.narg('kind')::varchar, kind)
ORDER BY kind, model_key, (tenant_id = '');

-- 列出可见且启用的模型选项（同 model_key 租户行覆盖平台行，每键至多一行）
-- name: ListSysModelOptions :many
SELECT DISTINCT ON (model_key) *
FROM public.sys_model
WHERE kind = sqlc.arg('kind')
  AND status = 1
  AND deleted_at IS NULL
ORDER BY model_key, (tenant_id = '');
