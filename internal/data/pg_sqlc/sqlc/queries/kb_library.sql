-- 知识库固定 CRUD（kb_library）。知识库是租户内资源：表无 @scope，
-- 租户边界由 RLS 承担；动态查询能力由 schema/91_knowledge.sql 的列注释
-- DSL 声明（ParseKbLibraryQuery 等为生成物）。本文件只写固定命名查询。

-- 按 id 读取未软删知识库（租户边界由 RLS 承担）
-- name: GetKbLibraryByID :one
SELECT * FROM public.kb_library
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删知识库（更新/删除路径先锁后写）
-- name: LockKbLibraryByID :one
SELECT * FROM public.kb_library
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入知识库记录；租户取会话 app.tenant_id，租户内未删除库间 name 唯一
-- 冲突映射 AlreadyExists
-- name: InsertKbLibrary :one
INSERT INTO public.kb_library (id, tenant_id, status, "name", remark, embedding_model_key,
                               chunk_size, chunk_overlap, valid_from, valid_to,
                               created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.narg('remark'), sqlc.narg('embedding_model_key'),
        sqlc.arg('chunk_size'), sqlc.arg('chunk_overlap'),
        sqlc.narg('valid_from'), sqlc.narg('valid_to'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status/remark/embedding_model_key/chunk_size/
-- chunk_overlap/valid_from/valid_to）；updated_at 由触发器刷新
-- name: UpdateKbLibrary :one
UPDATE public.kb_library
SET "name"              = sqlc.arg('name'),
    status              = sqlc.arg('status'),
    remark              = sqlc.narg('remark'),
    embedding_model_key = sqlc.narg('embedding_model_key'),
    chunk_size          = sqlc.arg('chunk_size'),
    chunk_overlap       = sqlc.arg('chunk_overlap'),
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

-- 置位 deleted_at 软删除知识库；活跃条目校验由调用方在同一事务内先行完成
-- name: SoftDeleteKbLibrary :execrows
UPDATE public.kb_library
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
