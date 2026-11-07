-- ======================================================================
-- 080_sys_tenant_config.sql — 生产种子：平台树根租户配置
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）。
-- ======================================================================

INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (1, 1, '租户 Logo', 'tenant.logo', '/logo/default.png', 'string', '租户 Logo 路径', 1, '2026-06-01 10:00:00.000',
        1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (2, 1, '租户显示名称', 'tenant.display-name', '默认租户', 'string', '租户在界面上的显示名称', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
