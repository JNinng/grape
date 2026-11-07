-- ======================================================================
-- 010_sys_tenant.sql — 生产种子：平台树根租户
-- 依赖：schema/20_tenant.sql（sys_tenant 存在）。
-- 执行：生产种子包首个文件；以超级用户/表 owner 连接执行（RLS 已启用，
--       超级用户绕过 RLS）。语句幂等，重复执行不覆盖已有行。
-- ======================================================================

INSERT INTO public.sys_tenant (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, code,
                               "name", parent_id, contact_name, contact_phone, expired_at, max_user_count, default_locale)
VALUES ('t-super-01', 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        'default', '默认租户', NULL, '系统管理员', '13800000000', NULL, 1000, 'zh-CN') ON CONFLICT DO NOTHING;
