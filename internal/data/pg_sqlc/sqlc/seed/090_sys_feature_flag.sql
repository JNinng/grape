-- ======================================================================
-- 090_sys_feature_flag.sql — 生产种子：平台树根租户功能开关
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）。
-- ======================================================================

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1, 1, '数据导出', 'export_enabled', '是否允许租户导出数据（Excel/CSV）', true, '默认租户开启导出功能', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (2, 1, 'API 访问', 'api_access', '是否允许租户通过 API 密钥访问系统', true, '默认租户开启 API 访问', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3, 1, 'SSO 单点登录', 'sso_enabled', '是否允许租户配置 SSO 单点登录', true, '默认租户开启 SSO', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (4, 1, '审计日志', 'audit_log', '是否启用操作日志记录', true, '默认租户开启审计日志', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
