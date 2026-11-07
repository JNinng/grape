-- ======================================================================
-- 040_sys_user_role_mapping.sql — 生产种子：admin 的角色映射
-- 依赖：020_sys_role.sql（角色 101）与 030_sys_user.sql（用户 1）。
-- ======================================================================

-- 用户角色映射：admin → 超级管理员
INSERT INTO public.sys_user_role_mapping (id, user_id, role_id, tenant_id, created_by)
VALUES (900101, 1, 101, 't-super-01', 1) ON CONFLICT DO NOTHING;
