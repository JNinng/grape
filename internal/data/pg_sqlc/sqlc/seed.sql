-- ======================================================================
-- seed.sql — 生产默认种子数据
-- 依赖：schema/ 全部文件 → rls.sql → operate.sql → expose.sql 之后执行
-- （RLS 已启用，种子需以超级用户/表 owner 连接执行，超级用户绕过 RLS；
-- expose.sql 必须先于本文件，角色映射写入要触发范围版本 bump）。
--
-- 本文件只含生产默认数据（平台树根租户 t-super-01 的自举数据）：
-- 树根租户、admin 账号、超级管理员角色与菜单授权、全局/租户配置、
-- 特性开关与翻译。字典不播种——数据状态/数据范围/角色类型等系统级
-- 枚举由列注释 @options 声明，字典表仅承载租户运营的分类数据，生产由
-- 租户经字典管理界面维护；测试用演示字典在 test_seed.sql。集成测试
-- 用的演示数据（t-test-* 租户、部门树、演示用户、数据范围矩阵、示例
-- 日志等）也在 test_seed.sql，仅测试部署装载，生产部署（dbdeploy）
-- 不执行。
--
-- 密码统一为 "123456"（scrypt 加密）；登录校验通过后由应用透明重哈希为 argon2id。
-- ======================================================================

-- ===========================
-- 1. 租户表 (sys_tenant)：平台树根
-- ===========================
INSERT INTO public.sys_tenant (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, code,
                               "name", parent_id, contact_name, contact_phone, expired_at, max_user_count, default_locale)
VALUES ('t-super-01', 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        'default', '默认租户', NULL, '系统管理员', '13800000000', NULL, 1000, 'zh-CN') ON CONFLICT DO NOTHING;

-- ===========================
-- 2. 角色表 (sys_role)：超级管理员（系统预置，全租户数据范围）
-- ===========================
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (101, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        '超级管理员', 'super_admin', 1, 7, NULL, '系统预置角色，拥有系统全部权限') ON CONFLICT DO NOTHING;

-- 角色数据范围示例明细：超级管理员可管理用户 1（admin）——role 101 为
-- data_scope=7，此明细为种子示例
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, created_by, created_at, updated_by,
                                      updated_at, deleted_at, tenant_id)
VALUES (1001, 1, 1, 101, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- ===========================
-- 3. 用户表 (sys_user)：admin — 平台树根的系统管理员
--    （部门树属测试数据，见 test_seed.sql，故不挂部门）
-- ===========================
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (1, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        1, NULL, 'admin', '系统管理员',
        '{scrypt}$e0801$A7n6UaC5UUO/K09rwLa00JQXL5WipLnXHmT/k8xfCDU7Ei3szBiwEPj0O5G/MKznY5Wb3tTkRZLH+IORCt43MQ==$zk5WhEHfnzqbkGAu67xgRtvspcnsr08uuBeGFaVHOv8=',
        'admin@example.com', '13800000001', '2026-06-30 08:00:00.000', '超级管理员账号', 'zh-CN') ON CONFLICT DO NOTHING;

-- 用户角色映射：admin → 超级管理员
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (1, 101, 't-super-01', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 4. 菜单/权限表 (sys_menu)
--    为平台树根租户 t-super-01 构建标准的后台管理菜单树
--    菜单结构：系统管理 → 用户管理/角色管理/菜单管理/租户管理/字典管理
--                      审计管理 → 操作日志/登录日志
--                      配置管理 → 系统配置/租户配置/功能特性
--                      部门管理 / 用户组管理 / 智能助手
-- ===========================

-- 一级目录：系统管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (201, 1, '系统管理', 'sys:manage', NULL, 1, 'setting', '/system', NULL, 1, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：用户管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (202, 1, '用户管理', 'sys:user', 201, 2, 'user', '/system/user', 'system/user/index', 1, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (203, 1, '用户查询', 'sys:user:query', 202, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (204, 1, '用户新增', 'sys:user:create', 202, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (205, 1, '用户编辑', 'sys:user:update', 202, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (206, 1, '用户删除', 'sys:user:delete', 202, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-重置密码
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (207, 1, '重置密码', 'sys:user:reset-pwd', 202, 3, 5, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-分配角色
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (208, 1, '分配角色', 'sys:user:assign-role', 202, 3, 6, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：角色管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (209, 1, '角色管理', 'sys:role', 201, 2, 'role', '/system/role', 'system/role/index', 2, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (210, 1, '角色查询', 'sys:role:query', 209, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (211, 1, '角色新增', 'sys:role:create', 209, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (212, 1, '角色编辑', 'sys:role:update', 209, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (213, 1, '角色删除', 'sys:role:delete', 209, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：菜单管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (214, 1, '菜单管理', 'sys:menu', 201, 2, 'menu', '/system/menu', 'system/menu/index', 3, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：菜单管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (215, 1, '菜单查询', 'sys:menu:query', 214, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：菜单管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (216, 1, '菜单新增', 'sys:menu:create', 214, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：菜单管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (217, 1, '菜单编辑', 'sys:menu:update', 214, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：菜单管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (218, 1, '菜单删除', 'sys:menu:delete', 214, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：租户管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (219, 1, '租户管理', 'sys:tenant', 201, 2, 'tenant', '/system/tenant', 'system/tenant/index', 4, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (220, 1, '租户查询', 'sys:tenant:query', 219, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (221, 1, '租户新增', 'sys:tenant:create', 219, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (222, 1, '租户编辑', 'sys:tenant:update', 219, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (223, 1, '租户删除', 'sys:tenant:delete', 219, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：字典管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (224, 1, '字典管理', 'sys:dict', 201, 2, 'dict', '/system/dict', 'system/dict/index', 5, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (225, 1, '字典查询', 'sys:dict:query', 224, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (226, 1, '字典新增', 'sys:dict:create', 224, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (227, 1, '字典编辑', 'sys:dict:update', 224, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (228, 1, '字典删除', 'sys:dict:delete', 224, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：审计管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (229, 1, '审计管理', 'sys:audit', NULL, 1, 'audit', '/audit', NULL, 2, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：操作日志
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (230, 1, '操作日志', 'sys:audit:operation-log', 229, 2, 'operation-log', '/audit/operation-log',
        'audit/operation-log/index', 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：登录日志
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (231, 1, '登录日志', 'sys:audit:login-log', 229, 2, 'login-log', '/audit/login-log', 'audit/login-log/index', 2,
        1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：配置管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (232, 1, '配置管理', 'sys:config', NULL, 1, 'config', '/config', NULL, 3, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：系统配置
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (233, 1, '系统配置', 'sys:config:global', 232, 2, 'global-config', '/config/global', 'config/global/index', 1, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：租户配置
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (234, 1, '租户配置', 'sys:config:tenant', 232, 2, 'tenant-config', '/config/tenant', 'config/tenant/index', 2, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：功能特性
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (235, 1, '功能特性', 'sys:config:feature-flag', 232, 2, 'feature-flag', '/config/feature-flag',
        'config/feature-flag/index', 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：部门管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (236, 1, '部门管理', 'sys:dept', NULL, 1, 'dept', '/dept', 'dept/index', 4, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (237, 1, '部门查询', 'sys:dept:query', 236, 3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (238, 1, '部门新增', 'sys:dept:create', 236, 3, 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (239, 1, '部门编辑', 'sys:dept:update', 236, 3, 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (240, 1, '部门删除', 'sys:dept:delete', 236, 3, 4, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000',
        NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：用户组管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (241, 1, '用户组管理', 'sys:user-group', NULL, 1, 'group', '/user-group', 'user-group/index', 5, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户组管理-查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (242, 1, '用户组查询', 'sys:user-group:query', 241, 3, 1, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户组管理-新增
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (243, 1, '用户组新增', 'sys:user-group:create', 241, 3, 2, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户组管理-编辑
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (244, 1, '用户组编辑', 'sys:user-group:update', 241, 3, 3, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户组管理-删除
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (245, 1, '用户组删除', 'sys:user-group:delete', 241, 3, 4, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：智能助手
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (250, 1, '智能助手', 'agent', NULL, 1, 'robot', '/agent', NULL, 6, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：工具授权
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (251, 1, '工具授权', 'agent:grant', 250, 2, 'grant', '/agent/grant', 'agent/grant/index', 1, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：工具授权-全租户查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (252, 1, '工具授权全租户查询', 'agent:grant:query', 251, 3, 1, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：工具授权-吊销他人授权
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (253, 1, '工具授权吊销', 'agent:grant:revoke', 251, 3, 2, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- ===========================
-- 5. 角色-菜单映射表 (sys_role_menu)
--    超级管理员 (101) → 全部菜单（2xx 树 + 智能助手 25x；其余权限码
--    种子由 99_perm_seed.sql 追加）
-- ===========================
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 201, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 202, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 203, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 204, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 205, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 206, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 207, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 208, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 209, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 210, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 211, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 212, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 213, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 214, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 215, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 216, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 217, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 218, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 219, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 220, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 221, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 222, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 223, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 224, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 225, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 226, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 227, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 228, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 229, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 230, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 231, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 232, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 233, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 234, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 235, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 236, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 237, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 238, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 239, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 240, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 241, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 242, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 243, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 244, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 245, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 250, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 251, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 252, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 253, 't-super-01', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 6. 系统全局配置 (sys_config)
-- ===========================
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

-- ===========================
-- 7. 租户配置 (sys_tenant_config)
--    为平台树根租户设置部分个性化配置
-- ===========================
INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (1, 1, '租户 Logo', 'tenant.logo', '/logo/default.png', 'string', '租户 Logo 路径', 1, '2026-06-01 10:00:00.000',
        1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (2, 1, '租户显示名称', 'tenant.display-name', '默认租户', 'string', '租户在界面上的显示名称', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- ===========================
-- 8. 功能特性 (sys_feature_flag)
--     控制各租户的功能开关
-- ===========================
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

-- ===========================
-- 9. 通用翻译 (sys_translation)
--     为平台树根租户 t-super-01 提供 en-US（英文）翻译种子数据。
--     应用层查询时先按用户语言偏好匹配，未命中则回退原始值（中文）。
-- ===========================

-- ---------------------------
-- 9a. 菜单名称翻译 (sys_menu.name → en-US)
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

-- ---------------------------
-- 9b. 系统配置名称翻译 (sys_config.name → en-US)
--      配置名称仅管理界面可见，也提供翻译以保持国际化一致性
-- ---------------------------
INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (46, 1, 'sys_config', 1, 'name', 'en-US', 'Session Timeout', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (47, 1, 'sys_config', 2, 'name', 'en-US', 'Refresh Token Timeout', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (48, 1, 'sys_config', 3, 'name', 'en-US', 'Password Lockout Threshold', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (49, 1, 'sys_config', 4, 'name', 'en-US', 'Password Lockout Duration', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (50, 1, 'sys_config', 5, 'name', 'en-US', 'Password Min Length', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (51, 1, 'sys_config', 6, 'name', 'en-US', 'SMTP Host', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_translation (id, status, table_name, row_id, column_name, locale, value, remark, created_by,
                                    created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (52, 1, 'sys_config', 7, 'name', 'en-US', 'System Name', NULL, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
