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
VALUES (8, 1, '默认文本模型', 'ai.text.model', 'qwen3:8b', 'string',
        '全局默认文本模型 model_key；租户配置同名键优先，键须指向可见的 text 模型或 conf.agent.llm', 1,
        '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                               updated_by, updated_at, deleted_at)
VALUES (9, 1, '默认嵌入模型', 'ai.embedding.model', 'qwen3-embedding:0.6b', 'string',
        '全局默认嵌入模型 model_key；租户配置同名键（兼容旧键 vector.embedder）优先，键须指向可见的 embedding 模型或 conf.vector.embedders', 1,
        '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL) ON CONFLICT DO NOTHING;
