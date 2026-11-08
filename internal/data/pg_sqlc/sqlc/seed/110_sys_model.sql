-- ======================================================================
-- 110_sys_model.sql — 生产种子：平台模型目录默认行
-- 依赖：schema/92_model.sql（sys_model 存在）。以超级用户/表 owner 执行
-- （RLS 已启用；平台行 tenant_id 为空串，运行角色无直写权，只能经受控
-- 函数写入，种子属部署面一次性自举）。
--
-- 两行均为本地开发默认端点（Ollama OpenAI 兼容面），生产部署前须在
-- 模型管理页替换为实际端点与模型名。model_key 与 070_sys_config.sql 的
-- ai.text.model / ai.embedding.model 默认值一致。
-- ======================================================================

-- 平台嵌入模型：qwen3-embedding:0.6b，1024 维（本期唯一允许维度）
INSERT INTO public.sys_model (id, tenant_id, status, kind, "name", model_key, provider,
                              base_url, model, dimension, timeout_ms, api_key_cipher, api_key_env,
                              remark, created_by, created_at, updated_by, updated_at, deleted_at)
VALUES (1, '', 1, 'embedding', 'Qwen3 Embedding 0.6B', 'qwen3-embedding:0.6b', 'openai',
        'http://127.0.0.1:11434/v1', 'qwen3-embedding:0.6b', 1024, 30000, NULL, NULL,
        '本地开发默认，生产部署前替换', 1, '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL)
ON CONFLICT DO NOTHING;

-- 平台文本模型：qwen3:8b，同本地端点
INSERT INTO public.sys_model (id, tenant_id, status, kind, "name", model_key, provider,
                              base_url, model, dimension, timeout_ms, api_key_cipher, api_key_env,
                              remark, created_by, created_at, updated_by, updated_at, deleted_at)
VALUES (2, '', 1, 'text', 'Qwen3 8B', 'qwen3:8b', 'openai',
        'http://127.0.0.1:11434/v1', 'qwen3:8b', NULL, 60000, NULL, NULL,
        '本地开发默认，生产部署前替换', 1, '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL)
ON CONFLICT DO NOTHING;
