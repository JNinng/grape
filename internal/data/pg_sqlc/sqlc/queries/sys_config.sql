-- 配置固定 CRUD（sys_config / sys_tenant_config）。配置族是「全局默认 +
-- 租户覆盖」两级：sys_config 是平台级全局表（无 tenant_id、无 RLS），
-- 运行角色对它的直写已被 role.sql REVOKE，写面经 98_config_write.sql 的
-- SECURITY DEFINER 固定口径写函数（本文件只写受控函数的 SELECT 调用）；
-- sys_tenant_config 是租户表（RLS 承担租户边界），写面为普通 DML。
-- 动态查询能力由 schema/90_config.sql 的列注释 DSL 声明
--（ParseSysConfigQuery / ParseSysTenantConfigQuery 等顶层函数为生成物），
-- 本文件只写固定命名查询。两表的 config_key 唯一约束限定未删除行间
--（部分唯一索引），软删后可复用；value_type 白名单与 status 取值校验
-- 收在 biz 用例，库层无 CHECK。

-- 按 id 读取未软删的全局配置（sys_config 不设 RLS：全局表，读取本身即
-- 全局口径）
-- name: GetSysConfigByID :one
SELECT * FROM public.sys_config
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 新建全局配置：调用受控写函数 fn_config_create（SECURITY DEFINER 固定
-- 口径：函数内校验认证事务纪律并落审计列）；运行角色无 sys_config 直写
-- 权限，本调用是唯一写路径
-- name: InsertSysConfig :one
SELECT *
FROM public.fn_config_create(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('name'),
                             sqlc.arg('config_key'), sqlc.narg('value'),
                             sqlc.arg('value_type'), sqlc.narg('remark'), sqlc.arg('operator'));

-- 覆盖可变字段（name/status/value/remark）：调用受控写函数 fn_config_update
--（config_key/value_type 是身份/值语义列不经本路径变更）；目标未命中
-- 函数返回 NULL 复合行，以 id IS NOT NULL 过滤成空集（映射 NotFound）
-- name: UpdateSysConfig :one
SELECT *
FROM public.fn_config_update(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('name'),
                             sqlc.narg('value'), sqlc.narg('remark'), sqlc.arg('operator'))
WHERE id IS NOT NULL;

-- 置位 deleted_at 软删除全局配置：调用受控写函数 fn_config_delete；
-- 返回删除行数，0 即目标未命中（映射 NotFound）
-- name: SoftDeleteSysConfig :one
SELECT public.fn_config_delete(sqlc.arg('id'), sqlc.arg('operator')) AS deleted_count;

-- 按 id 读取未软删的租户配置（租户边界由 RLS 承担；表无 @scope，租户内
-- 全可见）
-- name: GetSysTenantConfigByID :one
SELECT * FROM public.sys_tenant_config
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 插入租户配置：租户取会话 app.tenant_id（不取请求值）；租户内未删除
-- 配置间 config_key 唯一冲突映射 AlreadyExists
-- name: InsertSysTenantConfig :one
INSERT INTO public.sys_tenant_config (id, tenant_id, status, "name", config_key, value,
                                      value_type, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.arg('config_key'), sqlc.narg('value'), sqlc.arg('value_type'), sqlc.narg('remark'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新租户配置可变字段（name/status/value/remark；不含 config_key/
-- value_type/tenant_id——身份/值语义/归属列不经本路径变更）；updated_at
-- 由触发器刷新
-- name: UpdateSysTenantConfig :one
UPDATE public.sys_tenant_config
SET "name"     = sqlc.arg('name'),
    status     = sqlc.arg('status'),
    value      = sqlc.narg('value'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除租户配置（宿主无派生结构，不联动其他行）
-- name: SoftDeleteSysTenantConfig :execrows
UPDATE public.sys_tenant_config
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 功能开关（sys_feature_flag）与通用翻译（sys_translation）同属租户表：
-- 读写走普通 DML / 动态 List，租户边界由 RLS 承担，tenant_id 一律取会话
-- app.tenant_id（不取请求值）。身份列创建后不可变
--（sys_feature_flag.flag_key；sys_translation 的 table_name/row_id/
-- column_name/locale），更新查询不写这些列；软删口径收在 SoftDelete，
-- 唯一约束限定未删除行间（部分唯一索引），软删后可复用。

-- 按 id 读取未软删的功能开关（租户边界由 RLS 承担；表无 @scope，租户内
-- 全可见）
-- name: GetSysFeatureFlagByID :one
SELECT * FROM public.sys_feature_flag
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 插入功能开关：租户取会话 app.tenant_id（不取请求值）；租户内未删除
-- 开关间 flag_key 唯一冲突映射 AlreadyExists
-- name: InsertSysFeatureFlag :one
INSERT INTO public.sys_feature_flag (id, tenant_id, status, "name", flag_key, description,
                                     is_enabled, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.arg('flag_key'), sqlc.narg('description'), sqlc.arg('is_enabled'), sqlc.narg('remark'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新功能开关可变字段（name/description/is_enabled/remark/status；不含
-- flag_key/tenant_id——身份/归属列不经本路径变更）；updated_at 由触发器
-- 刷新
-- name: UpdateSysFeatureFlag :one
UPDATE public.sys_feature_flag
SET "name"      = sqlc.arg('name'),
    status      = sqlc.arg('status'),
    description = sqlc.narg('description'),
    is_enabled  = sqlc.arg('is_enabled'),
    remark      = sqlc.narg('remark'),
    updated_by  = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除功能开关（宿主无派生结构，不联动其他行）
-- name: SoftDeleteSysFeatureFlag :execrows
UPDATE public.sys_feature_flag
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 读取未软删的翻译（租户边界由 RLS 承担；表无 @scope，租户内全
-- 可见）
-- name: GetSysTranslationByID :one
SELECT * FROM public.sys_translation
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 插入翻译：租户取会话 app.tenant_id（不取请求值）；租户内未删除翻译间
-- 身份四元组 (table_name,row_id,column_name,locale) 唯一冲突映射
-- AlreadyExists
-- name: InsertSysTranslation :one
INSERT INTO public.sys_translation (id, tenant_id, status, table_name, row_id, column_name,
                                    locale, value, remark, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('table_name'),
        sqlc.arg('row_id'), sqlc.arg('column_name'), sqlc.arg('locale'), sqlc.arg('value'),
        sqlc.narg('remark'), sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新翻译可变字段（status/value/remark；不含 table_name/row_id/
-- column_name/locale/tenant_id——身份/归属列不经本路径变更）；updated_at
-- 由触发器刷新
-- name: UpdateSysTranslation :one
UPDATE public.sys_translation
SET status     = sqlc.arg('status'),
    value      = sqlc.arg('value'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除翻译（宿主无派生结构，不联动其他行）
-- name: SoftDeleteSysTranslation :execrows
UPDATE public.sys_translation
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
