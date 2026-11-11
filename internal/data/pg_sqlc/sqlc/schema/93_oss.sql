-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 内嵌 OSS 数据域：oss_object 单表承载全部对象。服务内嵌 gofakes3
-- （rclone fork，SigV4 鉴权）以库形态提供标准 S3 API，本表是其数据介质；
-- 业务侧只认 aws-sdk-go-v2 S3 客户端，后续切 MinIO / 阿里云 OSS / AWS
-- 只改 sys_config 的 oss.* 配置键（见根目录 operations.md）。
--
-- 设施面声明（与 agent schema 设施表同一口径）：
--   - 无租户列、无 RLS、无 @scope：bucket 即命名空间，S3 请求经 SigV4
--     签名自证身份（AK/SK 取自 sys_config 的 oss.access_key /
--     oss.secret_key，明文口径与 jwt.secret 同级，见 permission.md），
--     Backend 以池直连读写，不 经事务门面；
--   - 不声明 @dynamic/@updatable 动态协议：读写面由 gofakes3 Backend
--     独占，走 queries/oss_object.sql 固定命名查询；
--   - 不用 PG 大对象（lo）：bytea 直存，单对象上限 20MB 由 Backend 的
--     PutObject 按 Content-Length 限流（应用层保证，不设数据库 CHECK）；
--   - 本文件含 grape_u / test_u 的 DML 显式授权，依赖 role.sql 的角色
--     先行存在，部署清单排在 role.sql 之后（与 95/97 的尾部追加同一
--     口径；role.sql 的默认权限对本文件同样覆盖，显式声明是为脱离
--     默认权限口径自足成立）。
--
-- 已知边界：gofakes3 不支持版本控制、细粒度 ACL、lifecycle，业务不依赖；
-- bucket 不单独登记（零对象的桶不存在的语义由 Backend 与
-- gofakes3.WithAutoBucket 组合给出，见 internal/data/pg_sqlc/oss）。
-- ============================================================================
-- 内嵌 OSS 对象
--    S3 对象存储的数据介质：bucket + object_key 主键，data 直存字节，
--    etag 为对象内容的 MD5 十六进制（S3 ETag 语义，不带引号），metadata
--    存 S3 请求头透传的元数据（Content-Type / Last-Modified /
--    X-Amz-Meta-* 等，键值对原样落 jsonb），content_type 单列冗余自
--    metadata 便于按列读取。
-- ============================================================================

CREATE TABLE public.oss_object
(
    bucket       varchar(64)                            NOT NULL,
    object_key   text                                   NOT NULL,
    data         bytea                                  NOT NULL,
    size         int8                                   NOT NULL,
    content_type varchar(64)  DEFAULT ''                NOT NULL,
    etag         varchar(64)                            NOT NULL,
    metadata     jsonb                                  NULL,
    created_at   timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at   timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT oss_object_pkey PRIMARY KEY (bucket, object_key)
);

COMMENT ON TABLE public.oss_object IS '内嵌 OSS 对象表：服务内嵌 gofakes3（SigV4 鉴权）的标准 S3 API 数据介质，设施面无 RLS（bucket 即命名空间），单对象 ≤ 20MB 由应用层限流';

COMMENT ON COLUMN public.oss_object.bucket IS '桶名（varchar(64)，即命名空间；不单独登记，桶存在性以桶内对象行存在为准）';
COMMENT ON COLUMN public.oss_object.object_key IS '对象键（S3 key，原样存储，不做路径语义解释）';
COMMENT ON COLUMN public.oss_object.data IS '对象内容（bytea 直存，不用 PG 大对象；上限 20MB 由 Backend PutObject 按 Content-Length 限流）';
COMMENT ON COLUMN public.oss_object.size IS '对象字节数（Content-Length 口径，与 data 长度一致）';
COMMENT ON COLUMN public.oss_object.content_type IS '对象 Content-Type；请求未声明时空串（冗余自 metadata，便于按列读取）';
COMMENT ON COLUMN public.oss_object.etag IS '对象内容 MD5 十六进制（S3 ETag 语义，存储不带引号；覆盖写后随内容更新）';
COMMENT ON COLUMN public.oss_object.metadata IS 'S3 请求元数据（Content-Type / Last-Modified / X-Amz-Meta-* 等请求头键值对原样落 jsonb，GET/HEAD 时原样回带）';
COMMENT ON COLUMN public.oss_object.created_at IS '首次写入时间（覆盖写不变）';
COMMENT ON COLUMN public.oss_object.updated_at IS '最近写入时间（覆盖写刷新，List 的 LastModified 取本列）';

-- grape_u 的 DML 授权在本文件显式声明：本文件部署在 role.sql 之后
-- （角色必须先行存在），授权口径与 role.sql 的全表授权一致。
GRANT SELECT, INSERT, UPDATE, DELETE ON public.oss_object TO grape_u, test_u;
