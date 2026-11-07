-- ======================================================================
-- 100_sys_translation.sql — 生产种子：通用翻译（en-US）
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）；row_id 引用菜单/配置，
-- 但无外键，故不阻塞执行顺序。应用层查询先按用户语言偏好匹配，未命中
-- 回退原始值（中文）。
-- ======================================================================

-- ---------------------------
-- 菜单名称翻译 (sys_menu.name → en-US)
-- ---------------------------
INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1, 1, 'sys_menu', 201, 'name', 'en-US', 'System Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (2, 1, 'sys_menu', 202, 'name', 'en-US', 'User Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3, 1, 'sys_menu', 203, 'name', 'en-US', 'User Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (4, 1, 'sys_menu', 204, 'name', 'en-US', 'User Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (5, 1, 'sys_menu', 205, 'name', 'en-US', 'User Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (6, 1, 'sys_menu', 206, 'name', 'en-US', 'User Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (7, 1, 'sys_menu', 207, 'name', 'en-US', 'Reset Password', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (8, 1, 'sys_menu', 208, 'name', 'en-US', 'Assign Role', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (9, 1, 'sys_menu', 209, 'name', 'en-US', 'Role Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (10, 1, 'sys_menu', 210, 'name', 'en-US', 'Role Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (11, 1, 'sys_menu', 211, 'name', 'en-US', 'Role Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (12, 1, 'sys_menu', 212, 'name', 'en-US', 'Role Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (13, 1, 'sys_menu', 213, 'name', 'en-US', 'Role Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (14, 1, 'sys_menu', 214, 'name', 'en-US', 'Menu Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (15, 1, 'sys_menu', 215, 'name', 'en-US', 'Menu Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (16, 1, 'sys_menu', 216, 'name', 'en-US', 'Menu Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (17, 1, 'sys_menu', 217, 'name', 'en-US', 'Menu Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (18, 1, 'sys_menu', 218, 'name', 'en-US', 'Menu Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (19, 1, 'sys_menu', 219, 'name', 'en-US', 'Tenant Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (20, 1, 'sys_menu', 220, 'name', 'en-US', 'Tenant Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (21, 1, 'sys_menu', 221, 'name', 'en-US', 'Tenant Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (22, 1, 'sys_menu', 222, 'name', 'en-US', 'Tenant Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (23, 1, 'sys_menu', 223, 'name', 'en-US', 'Tenant Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (24, 1, 'sys_menu', 224, 'name', 'en-US', 'Dictionary Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (25, 1, 'sys_menu', 225, 'name', 'en-US', 'Dictionary Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (26, 1, 'sys_menu', 226, 'name', 'en-US', 'Dictionary Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (27, 1, 'sys_menu', 227, 'name', 'en-US', 'Dictionary Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (28, 1, 'sys_menu', 228, 'name', 'en-US', 'Dictionary Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (29, 1, 'sys_menu', 229, 'name', 'en-US', 'Audit Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (30, 1, 'sys_menu', 230, 'name', 'en-US', 'Operation Log', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (31, 1, 'sys_menu', 231, 'name', 'en-US', 'Login Log', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (32, 1, 'sys_menu', 232, 'name', 'en-US', 'Configuration Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (33, 1, 'sys_menu', 233, 'name', 'en-US', 'System Config', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (34, 1, 'sys_menu', 234, 'name', 'en-US', 'Tenant Config', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (35, 1, 'sys_menu', 235, 'name', 'en-US', 'Feature Flags', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (36, 1, 'sys_menu', 236, 'name', 'en-US', 'Department Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (37, 1, 'sys_menu', 237, 'name', 'en-US', 'Department Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (38, 1, 'sys_menu', 238, 'name', 'en-US', 'Department Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (39, 1, 'sys_menu', 239, 'name', 'en-US', 'Department Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (40, 1, 'sys_menu', 240, 'name', 'en-US', 'Department Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (41, 1, 'sys_menu', 241, 'name', 'en-US', 'User Group Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (42, 1, 'sys_menu', 242, 'name', 'en-US', 'User Group Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (43, 1, 'sys_menu', 243, 'name', 'en-US', 'User Group Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (44, 1, 'sys_menu', 244, 'name', 'en-US', 'User Group Edit', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (45, 1, 'sys_menu', 245, 'name', 'en-US', 'User Group Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 操作权限点与会话/配置族菜单翻译（id 64-67、70-91）。
INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (64, 1, 'sys_menu', 246, 'name', 'en-US', 'Transfer Owner', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (65, 1, 'sys_menu', 247, 'name', 'en-US', 'Member Manage', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (66, 1, 'sys_menu', 248, 'name', 'en-US', 'Data Scope Config', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (67, 1, 'sys_menu', 249, 'name', 'en-US', 'Menu Assignment', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (70, 1, 'sys_menu', 252, 'name', 'en-US', 'Grant Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (71, 1, 'sys_menu', 253, 'name', 'en-US', 'Grant Revoke', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (72, 1, 'sys_menu', 254, 'name', 'en-US', 'Session Management', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (73, 1, 'sys_menu', 255, 'name', 'en-US', 'Session Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (74, 1, 'sys_menu', 256, 'name', 'en-US', 'Session Revoke', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (75, 1, 'sys_menu', 257, 'name', 'en-US', 'Translation', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (76, 1, 'sys_menu', 258, 'name', 'en-US', 'Config Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (77, 1, 'sys_menu', 259, 'name', 'en-US', 'Config Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (78, 1, 'sys_menu', 260, 'name', 'en-US', 'Config Update', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (79, 1, 'sys_menu', 261, 'name', 'en-US', 'Config Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (80, 1, 'sys_menu', 262, 'name', 'en-US', 'Config Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (81, 1, 'sys_menu', 263, 'name', 'en-US', 'Config Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (82, 1, 'sys_menu', 264, 'name', 'en-US', 'Config Update', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (83, 1, 'sys_menu', 265, 'name', 'en-US', 'Config Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (84, 1, 'sys_menu', 266, 'name', 'en-US', 'Feature Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (85, 1, 'sys_menu', 267, 'name', 'en-US', 'Feature Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (86, 1, 'sys_menu', 268, 'name', 'en-US', 'Feature Update', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (87, 1, 'sys_menu', 269, 'name', 'en-US', 'Feature Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (88, 1, 'sys_menu', 270, 'name', 'en-US', 'Translation Query', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (89, 1, 'sys_menu', 271, 'name', 'en-US', 'Translation Create', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (90, 1, 'sys_menu', 272, 'name', 'en-US', 'Translation Update', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01'),
       (91, 1, 'sys_menu', 273, 'name', 'en-US', 'Translation Delete', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- ---------------------------
-- 系统配置名称翻译 (sys_config.name → en-US)
--      配置名称仅管理界面可见，也提供翻译以保持国际化一致性
-- ---------------------------
INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (57, 1, 'sys_config', 1, 'name', 'en-US', 'Session Timeout', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (58, 1, 'sys_config', 2, 'name', 'en-US', 'Refresh Token Timeout', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (59, 1, 'sys_config', 3, 'name', 'en-US', 'Password Lockout Threshold', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (60, 1, 'sys_config', 4, 'name', 'en-US', 'Password Lockout Duration', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (61, 1, 'sys_config', 5, 'name', 'en-US', 'Password Min Length', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (62, 1, 'sys_config', 6, 'name', 'en-US', 'SMTP Host', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (63, 1, 'sys_config', 7, 'name', 'en-US', 'System Name', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
