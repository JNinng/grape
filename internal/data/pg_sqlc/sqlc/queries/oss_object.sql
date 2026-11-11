-- 内嵌 OSS（gofakes3 PG Backend）的固定命名查询：调用方是
-- internal/data/pg_sqlc/oss 的 gofakes3 Backend，以池直连执行（设施面，
-- 无租户/身份语义，oss_object 无 RLS）。不参与动态查询协议；bucket 即
-- 命名空间，主键 (bucket, object_key)。覆盖写（upsert）是唯一更新形态
-- —— S3 PutObject 的幂等覆盖语义，etag 随内容更新，created_at 保持
-- 首次写入。Range 读取用 substr 只取切片字节，避免整对象出库。

-- 覆盖写一个对象（PutObject）；返回写入后的口径事实（size/etag 与时间）
-- name: UpsertOssObject :one
INSERT INTO public.oss_object (bucket, object_key, data, size, content_type, etag, metadata)
VALUES (sqlc.arg('bucket'), sqlc.arg('object_key'), sqlc.arg('data'),
        sqlc.arg('size')::bigint, sqlc.arg('content_type'), sqlc.arg('etag'),
        sqlc.arg('metadata')::jsonb)
ON CONFLICT (bucket, object_key) DO UPDATE
    SET data         = EXCLUDED.data,
        size         = EXCLUDED.size,
        content_type = EXCLUDED.content_type,
        etag         = EXCLUDED.etag,
        metadata     = EXCLUDED.metadata,
        updated_at   = CURRENT_TIMESTAMP
RETURNING size, etag, created_at, updated_at;

-- 读取整个对象（GetObject 全量）
-- name: GetOssObject :one
SELECT bucket, object_key, data, size, content_type, etag, metadata, created_at, updated_at
FROM public.oss_object
WHERE bucket = sqlc.arg('bucket') AND object_key = sqlc.arg('object_key');

-- 读取对象的字节区间（GetObject Range）：substr 按 1 基字节偏移切 bytea，
-- start/length 由 Backend 从 ObjectRangeRequest 换算，避免整对象出库
-- （substr 的 bytea 签名收 integer，单对象上限 20MB 内恒可安全截取）
-- name: GetOssObjectRange :one
SELECT bucket, object_key, size, content_type, etag, metadata, created_at, updated_at,
       substr(data, sqlc.arg('start_offset')::int, sqlc.arg('length')::int) AS data_slice
FROM public.oss_object
WHERE bucket = sqlc.arg('bucket') AND object_key = sqlc.arg('object_key');

-- 读取对象元数据（HeadObject，不出字节）
-- name: GetOssObjectMeta :one
SELECT bucket, object_key, size, content_type, etag, metadata, created_at, updated_at
FROM public.oss_object
WHERE bucket = sqlc.arg('bucket') AND object_key = sqlc.arg('object_key');

-- 删除单个对象（DeleteObject 幂等：S3 语义删除不存在的对象不算错误）
-- name: DeleteOssObject :execrows
DELETE FROM public.oss_object
WHERE bucket = sqlc.arg('bucket') AND object_key = sqlc.arg('object_key');

-- 批量删除（DeleteMulti），返回实际删除的对象键
-- name: DeleteOssObjects :many
DELETE FROM public.oss_object
WHERE bucket = sqlc.arg('bucket') AND object_key = ANY (sqlc.arg('object_keys')::text[])
RETURNING object_key;

-- 桶内对象行计数（BucketExists 的存在性判定与 DeleteBucket 非空守卫共用）
-- name: CountOssBucketObjects :one
SELECT count(*) FROM public.oss_object WHERE bucket = sqlc.arg('bucket');

-- 列举桶（ListBuckets）：桶不单独登记，按对象行聚合，桶创建时间取
-- 首个对象的写入时间（cast 供 sqlc 推断聚合类型）
-- name: ListOssBucketHeads :many
SELECT bucket, min(created_at)::timestamp AS created_at
FROM public.oss_object
GROUP BY bucket;

-- 列举桶内对象条目（ListBucket）：pattern 是前缀字面量转义后拼 '%' 的
-- LIKE 模式（通配符语义在 Go 侧由 gofakes3 Prefix 处理，SQL 只做前缀
-- 收窄）；marker 是上一页最后一个对象键（字典序严格大于）
-- name: ListOssObjectEntries :many
SELECT object_key, size, etag, updated_at
FROM public.oss_object
WHERE bucket = sqlc.arg('bucket')
  AND object_key LIKE sqlc.arg('pattern')
  AND (sqlc.narg('marker')::text IS NULL OR object_key > sqlc.narg('marker'))
ORDER BY object_key ASC;

-- 复制对象（CopyObject）：单语句完成「读源 + 写目标」，源不存在时无行
-- 返回（Backend 映射 KeyNotFound）；content_type/metadata 由服务端按
-- x-amz-metadata-directive 给出的完整元数据传入
-- name: CopyOssObject :one
INSERT INTO public.oss_object (bucket, object_key, data, size, content_type, etag, metadata)
SELECT sqlc.arg('dst_bucket'), sqlc.arg('dst_key'), src.data, src.size,
       sqlc.arg('content_type'), src.etag, sqlc.arg('metadata')::jsonb
FROM public.oss_object AS src
WHERE src.bucket = sqlc.arg('src_bucket') AND src.object_key = sqlc.arg('src_key')
ON CONFLICT (bucket, object_key) DO UPDATE
    SET data         = EXCLUDED.data,
        size         = EXCLUDED.size,
        content_type = EXCLUDED.content_type,
        etag         = EXCLUDED.etag,
        metadata     = EXCLUDED.metadata,
        updated_at   = CURRENT_TIMESTAMP
RETURNING size, etag, updated_at;
