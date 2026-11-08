-- 知识条目固定 CRUD（kb_document）。条目是租户内资源：表无 @scope，
-- 租户边界由 RLS 承担；动态查询能力由 schema/91_knowledge.sql 的列注释
-- DSL 声明。库内条目列表走固定 offset 分页（宿主 id 在路径参数，规避
-- 动态协议整型过滤值的 JSON number 精度限制）。

-- 按 id 读取未软删条目（租户边界由 RLS 承担）
-- name: GetKbDocumentByID :one
SELECT * FROM public.kb_document
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删条目（更新/删除/重摄取路径先锁后写）
-- name: LockKbDocumentByID :one
SELECT * FROM public.kb_document
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入知识条目；租户取会话 app.tenant_id
-- name: InsertKbDocument :one
INSERT INTO public.kb_document (id, tenant_id, library_id, kind, title, content, source_name,
                                content_hash, status, valid_from, valid_to,
                                indexed_at, indexed_embedder, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('library_id'), sqlc.arg('kind'),
        sqlc.arg('title'), sqlc.narg('content'), sqlc.narg('source_name'),
        sqlc.narg('content_hash'), sqlc.arg('status'), sqlc.narg('valid_from'), sqlc.narg('valid_to'),
        sqlc.narg('indexed_at'), sqlc.narg('indexed_embedder'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（title/content/source_name/content_hash/status/valid_from/
-- valid_to/indexed_at/indexed_embedder）；library_id 与 kind 创建后不变，
-- updated_at 由触发器刷新
-- name: UpdateKbDocument :one
UPDATE public.kb_document
SET title            = sqlc.arg('title'),
    content          = sqlc.narg('content'),
    source_name      = sqlc.narg('source_name'),
    content_hash     = sqlc.narg('content_hash'),
    status           = sqlc.arg('status'),
    valid_from       = sqlc.narg('valid_from'),
    valid_to         = sqlc.narg('valid_to'),
    indexed_at       = sqlc.narg('indexed_at'),
    indexed_embedder = sqlc.narg('indexed_embedder'),
    updated_by       = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 更新条目的状态与有效期（不重嵌，只影响检索过滤与 chunk metadata 同步）
-- name: UpdateKbDocumentStatus :one
UPDATE public.kb_document
SET status     = sqlc.arg('status'),
    valid_from = sqlc.narg('valid_from'),
    valid_to   = sqlc.narg('valid_to'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 分页列出指定库下的未删除条目（管理面子列表：固定 offset 分页，
-- updated_at 倒序、同秒 id 倒序稳定排序）
-- name: ListKbDocumentsByLibrary :many
SELECT * FROM public.kb_document
WHERE library_id = sqlc.arg('library_id')
  AND deleted_at IS NULL
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- 与 ListKbDocumentsByLibrary 同条件计数
-- name: CountKbDocumentsByLibraryList :one
SELECT count(*)
FROM public.kb_document
WHERE library_id = sqlc.arg('library_id')
  AND deleted_at IS NULL;

-- 置位 deleted_at 软删除条目（chunk 清理由用例经向量存储完成）
-- name: SoftDeleteKbDocument :execrows
UPDATE public.kb_document
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 记录最近一次成功摄取的时间与空间键（摄取完成后回写；不触发触发器之外的
-- 副作用）
-- name: MarkKbDocumentIndexed :one
UPDATE public.kb_document
SET indexed_at       = CURRENT_TIMESTAMP,
    indexed_embedder = sqlc.arg('indexed_embedder'),
    updated_by       = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;
