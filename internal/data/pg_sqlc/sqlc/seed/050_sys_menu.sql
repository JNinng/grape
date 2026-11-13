-- ======================================================================
-- 050_sys_menu.sql — 生产种子：后台管理菜单与操作权限点
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）。
-- 菜单树与权限码是入口层判定码表（internal/middleware/permission.go）及
-- 前端页面/按钮显隐的唯一生产事实源：页面菜单 route_path/component 对齐
-- web/src 实际路由；agent 工具授权的后端判定码保留为根权限点（无前端页面、
-- 无路由）。操作权限点与 biz 常量一一对应。
-- ======================================================================

--    为平台树根租户 t-super-01 构建标准的后台管理菜单树
--    菜单结构：系统管理 → 用户管理/角色管理/菜单管理/租户管理/字典管理
--                      审计管理 → 操作日志/登录日志/会话管理
--                      配置管理 → 系统配置/租户配置/功能特性/通用翻译
--                      部门管理 / 用户组管理

-- 一级目录：系统管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (201, 1, '系统管理', 'sys:manage', NULL, 1, 'setting', '/settings', NULL, 1, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：用户管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (202, 1, '用户管理', 'sys:user', 201, 2, 'user', '/settings/users', 'pages/settings/UserManagePage.tsx', 1, 1,
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
VALUES (209, 1, '角色管理', 'sys:role', 201, 2, 'role', '/settings/roles', 'pages/settings/RoleManagePage.tsx', 2, 1,
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
VALUES (214, 1, '菜单管理', 'sys:menu', 201, 2, 'menu', '/settings/menus', 'pages/settings/MenuManagePage.tsx', 3, 1,
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
VALUES (219, 1, '租户管理', 'sys:tenant', 201, 2, 'tenant', '/settings/tenants', 'pages/settings/TenantManagePage.tsx', 4, 1,
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
VALUES (224, 1, '字典管理', 'sys:dict', 201, 2, 'dict', '/settings/dicts', 'pages/settings/DictManagePage.tsx', 5, 1,
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
VALUES (229, 1, '审计管理', 'sys:audit', NULL, 1, 'audit', '/settings', NULL, 2, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：操作日志
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (230, 1, '操作日志', 'sys:audit:operation-log', 229, 2, 'operation-log', '/settings/audit-logs',
        'pages/settings/AuditLogManagePage.tsx', 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：登录日志
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (231, 1, '登录日志', 'sys:audit:login-log', 229, 2, 'login-log', '/settings/audit-logs',
        'pages/settings/AuditLogManagePage.tsx', 2, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 一级目录：配置管理
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (232, 1, '配置管理', 'sys:config', NULL, 1, 'config', '/settings', NULL, 3, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：系统配置
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (233, 1, '系统配置', 'sys:config:global', 232, 2, 'global-config', '/settings/configs',
        'pages/settings/ConfigManagePage.tsx', 1, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：租户配置
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (234, 1, '租户配置', 'sys:config:tenant', 232, 2, 'tenant-config', '/settings/configs',
        'pages/settings/ConfigManagePage.tsx', 2, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：功能特性
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (235, 1, '功能特性', 'sys:config:feature-flag', 232, 2, 'feature-flag', '/settings/configs',
        'pages/settings/ConfigManagePage.tsx', 3, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 一级页面：部门管理（挂操作权限点子节点，menu_type=2）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (236, 1, '部门管理', 'sys:dept', NULL, 2, 'dept', '/settings/depts', 'pages/settings/DeptManagePage.tsx', 4, 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

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

-- 一级页面：用户组管理（挂操作权限点子节点，menu_type=2）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (241, 1, '用户组管理', 'sys:user-group', NULL, 2, 'group', '/settings/user-groups',
        'pages/settings/UserGroupManagePage.tsx', 5, 1,
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

-- 工具授权的后端权限点没有前端页面（不设菜单/路由），直接作为根权限点
-- 承载 agent 工具授权管理端点的判定码；父级为 NULL，无前端页面与路由。
-- 三级按钮：工具授权-全租户查询
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (252, 1, '工具授权全租户查询', 'agent:grant:query', NULL, 3, 6, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：工具授权-吊销他人授权
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (253, 1, '工具授权吊销', 'agent:grant:revoke', NULL, 3, 7, 1, '2026-06-01 10:00:00.000', 1,
        '2026-06-01 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-转移归属（id 246，父 202 用户管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (246, 1, '转移归属', 'sys:user:transfer-owner', 202, 3, 7, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户组管理-成员增删（id 247，父 241 用户组管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (247, 1, '成员增删', 'sys:user-group:assign-member', 241, 3, 5, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-数据范围配置（id 248，父 209 角色管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (248, 1, '数据范围配置', 'sys:role:set-data-scope', 209, 3, 5, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：角色管理-菜单授权（id 249，父 209 角色管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (249, 1, '菜单授权', 'sys:role:set-menus', 209, 3, 6, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：会话管理（id 254，父 229 审计管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (254, 1, '会话管理', 'sys:session', 229, 2, 'session', '/settings/sessions',
        'pages/settings/SessionManagePage.tsx', 3, 1, '2026-11-03 10:00:00.000', 1, '2026-11-03 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：会话管理-查询（id 255，父 254）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (255, 1, '会话查询', 'sys:session:query', 254, 3, 1, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：会话管理-撤销（id 256，父 254）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (256, 1, '会话撤销', 'sys:session:revoke', 254, 3, 2, 1, '2026-11-03 10:00:00.000', 1,
        '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 二级菜单：通用翻译（id 257，父 232 配置管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (257, 1, '通用翻译', 'sys:config:translation', 232, 2, 'translation', '/settings/configs',
        'pages/settings/ConfigManagePage.tsx', 4, 1, '2026-11-04 10:00:00.000', 1, '2026-11-04 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：系统配置-查询（id 258，父 233 系统配置）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (258, 1, '配置查询', 'sys:config:global:query', 233, 3, 1, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：系统配置-创建（id 259，父 233）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (259, 1, '配置创建', 'sys:config:global:create', 233, 3, 2, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：系统配置-更新（id 260，父 233）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (260, 1, '配置更新', 'sys:config:global:update', 233, 3, 3, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：系统配置-删除（id 261，父 233）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (261, 1, '配置删除', 'sys:config:global:delete', 233, 3, 4, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户配置-查询（id 262，父 234 租户配置）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (262, 1, '配置查询', 'sys:config:tenant:query', 234, 3, 1, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户配置-创建（id 263，父 234）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (263, 1, '配置创建', 'sys:config:tenant:create', 234, 3, 2, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户配置-更新（id 264，父 234）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (264, 1, '配置更新', 'sys:config:tenant:update', 234, 3, 3, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：租户配置-删除（id 265，父 234）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (265, 1, '配置删除', 'sys:config:tenant:delete', 234, 3, 4, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：功能特性-查询（id 266，父 235 功能特性）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (266, 1, '特性查询', 'sys:config:feature-flag:query', 235, 3, 1, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：功能特性-创建（id 267，父 235）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (267, 1, '特性创建', 'sys:config:feature-flag:create', 235, 3, 2, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：功能特性-更新（id 268，父 235）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (268, 1, '特性更新', 'sys:config:feature-flag:update', 235, 3, 3, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：功能特性-删除（id 269，父 235）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (269, 1, '特性删除', 'sys:config:feature-flag:delete', 235, 3, 4, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：通用翻译-查询（id 270，父 257 通用翻译）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (270, 1, '翻译查询', 'sys:config:translation:query', 257, 3, 1, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：通用翻译-创建（id 271，父 257）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (271, 1, '翻译创建', 'sys:config:translation:create', 257, 3, 2, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：通用翻译-更新（id 272，父 257）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (272, 1, '翻译更新', 'sys:config:translation:update', 257, 3, 3, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：通用翻译-删除（id 273，父 257）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (273, 1, '翻译删除', 'sys:config:translation:delete', 257, 3, 4, 1, '2026-11-04 10:00:00.000', 1,
        '2026-11-04 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级页面：知识库（id 274，路由 /settings/knowledge）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (274, 1, '知识库', 'sys:knowledge', NULL, 2, 'knowledge', '/settings/knowledge',
        'pages/settings/KnowledgeManagePage.tsx', 6, 1, '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：知识库-查询（id 275，父 274）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (275, 1, '知识库查询', 'sys:knowledge:query', 274, 3, 1, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：知识库-创建（id 276，父 274）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (276, 1, '知识库创建', 'sys:knowledge:create', 274, 3, 2, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：知识库-更新（id 277，父 274；含条目编辑与重建索引）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (277, 1, '知识库更新', 'sys:knowledge:update', 274, 3, 3, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：知识库-删除（id 278，父 274）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (278, 1, '知识库删除', 'sys:knowledge:delete', 274, 3, 4, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级页面：模型管理（id 279，路由 /settings/models）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (279, 1, '模型管理', 'sys:model', NULL, 2, 'model', '/settings/models',
        'pages/settings/ModelManagePage.tsx', 7, 1, '2026-11-08 10:00:00.000', 1, '2026-11-08 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-查询（id 280，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (280, 1, '模型查询', 'sys:model:query', 279, 3, 1, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-创建租户模型（id 281，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (281, 1, '模型创建', 'sys:model:create', 279, 3, 2, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-更新租户模型（id 282，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (282, 1, '模型更新', 'sys:model:update', 279, 3, 3, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-删除租户模型（id 283，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (283, 1, '模型删除', 'sys:model:delete', 279, 3, 4, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-平台模型创建（id 284，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (284, 1, '平台模型创建', 'sys:model:platform:create', 279, 3, 5, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-平台模型更新（id 285，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (285, 1, '平台模型更新', 'sys:model:platform:update', 279, 3, 6, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型管理-平台模型删除（id 286，父 279）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (286, 1, '平台模型删除', 'sys:model:platform:delete', 279, 3, 7, 1, '2026-11-08 10:00:00.000', 1,
        '2026-11-08 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 一级页面：模型调用（id 287，路由 /settings/model-calls）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (287, 1, '模型调用', 'agent:model-call', NULL, 2, 'activity', '/settings/model-calls',
        'pages/settings/ModelCallManagePage.tsx', 8, 1, '2026-11-13 10:00:00.000', 1,
        '2026-11-13 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

-- 三级按钮：模型调用-查询（id 288，父 287）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (288, 1, '模型调用查询', 'agent:model-call:query', 287, 3, 1, 1, '2026-11-13 10:00:00.000', 1,
        '2026-11-13 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;
