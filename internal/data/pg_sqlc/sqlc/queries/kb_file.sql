-- 文件介质固定 CRUD（kb_file）。介质行是租户表：租户边界由 RLS 承担；
-- 读写面只有本文件的固定查询（创建 / 点查 / 锁定 / 软删），不参与动态
-- 查询协议。object_key 指向内嵌 OSS，本表只存元数据；软删是唯一删除
-- 形态（OSS 对象由用例尽力清理），保留介质行是为了审计与误删恢复线索。

-- 按 id 读取未软删介质行（kb_document.file_id 引用的本租户行校验点：
-- 跨租户行受 RLS 隔离，无行即拒绝）
-- name: GetKbFileByID :one
SELECT * FROM public.kb_file
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删介质行（宿主条目删除路径先锁后写）
-- name: LockKbFileByID :one
SELECT * FROM public.kb_file
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入介质行；租户取会话 app.tenant_id
-- name: InsertKbFile :one
INSERT INTO public.kb_file (id, tenant_id, object_key, etag, sha256, mime_type, file_size,
                            created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('object_key'),
        sqlc.arg('etag'), sqlc.arg('sha256'), sqlc.arg('mime_type'), sqlc.arg('file_size')::bigint,
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 置位 deleted_at 软删介质行（宿主条目删除后清理；OSS 对象删除由用例完成）
-- name: SoftDeleteKbFile :execrows
UPDATE public.kb_file
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
