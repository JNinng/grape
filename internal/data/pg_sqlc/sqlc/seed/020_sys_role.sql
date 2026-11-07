-- ======================================================================
-- 020_sys_role.sql — 生产种子：超级管理员角色
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）。
-- 角色 101 为系统预置角色（全部数据范围），后续文件把菜单与用户挂到它上面。
-- ======================================================================

INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (101, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        '超级管理员', 'super_admin', 1, 7, NULL, '系统预置角色，拥有系统全部权限') ON CONFLICT DO NOTHING;
