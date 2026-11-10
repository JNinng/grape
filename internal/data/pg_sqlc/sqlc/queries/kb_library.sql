-- 知识库固定 CRUD（kb_library）。知识库是租户表，租户边界由 RLS 承担；
-- 租户内的库级可见性由归属三元组 + @scope 守护（表注释见
-- schema/91_knowledge.sql），单行读与写前锁定走 fn_check_visible /
-- fn_can_operate_row，与动态 List 的 @scope 守卫同一口径（fn_accessible_*）。
-- 动态查询能力由 schema/91_knowledge.sql 的列注释 DSL 声明
-- （ParseKbLibraryQuery 等为生成物）。本文件只写固定命名查询。

-- 按 id 读取数据范围内可见的未软删知识库；不可见与不存在统一无行
-- （NotFound，不泄露存在性）
-- name: GetKbLibraryPermitted :one
SELECT * FROM public.kb_library
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_check_visible(sqlc.arg('operator'), 7::int2,
                              owner_user_id, owner_dept_id, owner_user_group_id);

-- 按 id 锁定数据范围内可操作的知识库（更新/删除路径先锁后写）；
-- 不可见与不存在统一无行
-- name: LockKbLibraryPermitted :one
SELECT * FROM public.kb_library
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
  AND public.fn_can_operate_row(sqlc.arg('operator'),
                                owner_user_id, owner_dept_id, owner_user_group_id)
FOR UPDATE;

-- 枚举数据范围内可见的未软删知识库：检索用例的可见库集合解析与列库工具
-- 共用本查询（单一实现，避免「列得出、查不到」的口径漂移）
-- name: ListVisibleKbLibraries :many
SELECT * FROM public.kb_library
WHERE deleted_at IS NULL
  AND public.fn_check_visible(sqlc.arg('operator'), 7::int2,
                              owner_user_id, owner_dept_id, owner_user_group_id)
ORDER BY id;

-- 插入知识库记录；租户取会话 app.tenant_id，创建人缺省为归属人；
-- 租户内未删除库间 name 唯一冲突映射 AlreadyExists
-- name: InsertKbLibrary :one
INSERT INTO public.kb_library (id, tenant_id, status, "name", remark, embedding_model_key,
                               chunk_size, chunk_overlap, chunk_delimiter, valid_from, valid_to,
                               owner_user_id, owner_dept_id, owner_user_group_id,
                               created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.narg('remark'), sqlc.narg('embedding_model_key'),
        sqlc.arg('chunk_size'), sqlc.arg('chunk_overlap'), sqlc.arg('chunk_delimiter'),
        sqlc.narg('valid_from'), sqlc.narg('valid_to'),
        sqlc.arg('owner_user_id'), sqlc.narg('owner_dept_id'), sqlc.narg('owner_user_group_id'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status/remark/embedding_model_key/chunk_size/
-- chunk_overlap/chunk_delimiter/valid_from/valid_to）；updated_at 由触发器刷新；
-- 归属不可经本查询变更（写入路径先经 LockKbLibraryPermitted 锁定）
-- name: UpdateKbLibrary :one
UPDATE public.kb_library
SET "name"              = sqlc.arg('name'),
    status              = sqlc.arg('status'),
    remark              = sqlc.narg('remark'),
    embedding_model_key = sqlc.narg('embedding_model_key'),
    chunk_size          = sqlc.arg('chunk_size'),
    chunk_overlap       = sqlc.arg('chunk_overlap'),
    chunk_delimiter     = sqlc.arg('chunk_delimiter'),
    valid_from          = sqlc.narg('valid_from'),
    valid_to            = sqlc.narg('valid_to'),
    updated_by          = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 统计库下未删除条目数（库删除前置校验：>0 拒绝软删）
-- name: CountKbDocumentsByLibrary :one
SELECT count(*)
FROM public.kb_document
WHERE library_id = sqlc.arg('library_id')
  AND deleted_at IS NULL;

-- 置位 deleted_at 软删除知识库；活跃条目校验与可见性锁定由调用方在同一
-- 事务内先行完成
-- name: SoftDeleteKbLibrary :execrows
UPDATE public.kb_library
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
