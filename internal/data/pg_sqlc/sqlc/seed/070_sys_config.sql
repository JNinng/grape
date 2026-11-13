-- ======================================================================
-- 070_sys_config.sql — 生产种子：系统全局配置
-- 依赖：schema/90_config.sql（sys_config 存在）；全局表无租户边界。
-- ======================================================================

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (1, 1, '会话超时时间', 'session.timeout', '1800', 'integer', '用户会话超时时间（秒），默认 30 分钟', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (2, 1, '刷新令牌过期时间', 'session.refresh-timeout', '604800', 'integer', '刷新令牌过期时间（秒），默认 7 天', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (3, 1, '密码错误锁定阈值', 'auth.lockout-threshold', '5', 'integer',
        '连续密码错误次数超过此值将锁定账号，0 表示不锁定', 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (4, 1, '密码错误锁定时长', 'auth.lockout-duration', '1800', 'integer', '账号锁定持续时间（秒），默认 30 分钟', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (5, 1, '密码最小长度', 'auth.password-min-length', '6', 'integer', '用户密码最小长度要求', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (6, 1, 'SMTP 主机', 'smtp.host', 'smtp.example.com', 'string', '邮件发送服务器地址', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (7, 1, '系统名称', 'system.name', '基础多租户管理系统', 'string', '系统显示名称', 1, '2026-06-01 10:00:00.000',
        1, '2026-06-01 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (8, 1, '默认文本模型', 'ai.text.model', 'deepseek-flash', 'string',
        '全局默认文本模型 model_key；租户配置同名键优先，键须指向可见的 text 模型或 conf.agent.llm', 1,
        '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (9, 1, '默认嵌入模型', 'ai.embedding.model', 'qwen3-embedding:0.6b', 'string',
        '全局默认嵌入模型 model_key；租户配置同名键（兼容旧键 vector.embedder）优先，键须指向可见的 embedding 模型或 conf.vector.embedders', 1,
        '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

-- 内嵌 OSS（gofakes3）端点事实，value_type=json（值为 JSON 字面量）。
-- 非密钥四键随种子给出缺省；access_key / secret_key 属明文凭据（与
-- jwt.secret 同口径），不落仓库种子，启用前经管理端受控写函数配置——
-- conf.oss.enabled=true 而配置不齐在装配期拒绝启动（fail-closed）。

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (10, 1, '对象存储 provider', 'oss.provider', '"local"', 'json',
        '内嵌对象存储后端：local（服务内嵌 gofakes3，挂载 /s3/）/ minio / s3 / aliyun；仅 local 时本服务提供 S3 API，其余取值走外部端点', 1,
        '2026-11-10 10:00:00.000', 1, '2026-11-10 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (11, 1, '对象存储端点', 'oss.endpoint', '"http://127.0.0.1:8000/s3"', 'json',
        'S3 兼容端点根地址（业务客户端 BaseEndpoint）；local 指本服务 /s3/ 前缀，切换外部 OSS 时改为对应端点', 1,
        '2026-11-10 10:00:00.000', 1, '2026-11-10 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (12, 1, '对象存储桶名', 'oss.bucket', '"grape"', 'json',
        '业务对象统一落桶；桶即命名空间，内嵌实现不单独登记桶（首个对象写入即视为桶存在）', 1,
        '2026-11-10 10:00:00.000', 1, '2026-11-10 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (13, 1, '对象存储 region', 'oss.region', '"us-east-1"', 'json',
        'SigV4 region；内嵌实现按请求 scope 接受任意取值，切外部 OSS 时改为对应 region（如 oss-cn-hangzhou）', 1,
        '2026-11-10 10:00:00.000', 1, '2026-11-10 10:00:00.000', NULL) ON CONFLICT DO NOTHING;
