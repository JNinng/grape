-- ======================================================================
-- 120_sys_dict.sql — 生产种子：模型域字典枚举
-- 依赖：schema/60_dict.sql（字典两级表存在）与 010_sys_tenant.sql
-- （根租户 t-super-01 存在）。字典是租户级表（tenant_id + RLS），
-- 本文件只给根租户播种模型类别与 provider 枚举，供管理页展示与过滤使用；
-- 模型清单本身在 sys_model（平台/租户两级），不以字典承载。
-- 以超级用户/表 owner 执行；固定主键，ON CONFLICT DO NOTHING 可重复执行。
-- ======================================================================

-- 字典类型：model_kind / model_provider
INSERT INTO public.sys_dict_type (id, status, "name", code, remark, created_by, created_at, updated_by, updated_at,
                                  deleted_at, tenant_id)
VALUES (301, 1, '模型类别', 'model_kind', '文本 / 嵌入 / 重排模型类别枚举', 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_type (id, status, "name", code, remark, created_by, created_at, updated_by, updated_at,
                                  deleted_at, tenant_id)
VALUES (302, 1, '模型 Provider', 'model_provider', 'OpenAI 兼容 provider 变体枚举', 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 字典数据：模型类别
INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3101, 1, 301, '文本模型', 'text', 'string', 1, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3102, 1, 301, '嵌入模型', 'embedding', 'string', 2, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3103, 1, 301, '重排模型', 'rerank', 'string', 3, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 字典数据：模型 Provider
INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3111, 1, 302, 'OpenAI', 'openai', 'string', 1, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3112, 1, 302, 'DeepSeek', 'deepseek', 'string', 2, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3113, 1, 302, 'Qwen', 'qwen', 'string', 3, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3114, 1, 302, 'GLM', 'glm', 'string', 4, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3115, 1, 302, 'MiniMax', 'minimax', 'string', 5, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3116, 1, 302, 'Kimi', 'kimi', 'string', 6, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, dict_type_id, label, value, value_type, sort_order, remark,
                                  created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3117, 1, 302, 'Hunyuan', 'hunyuan', 'string', 7, NULL, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
