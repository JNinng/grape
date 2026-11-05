-- ============================================================
-- 99_perm_seed.sql — 管理面功能权限码种子
-- 依赖：seed.sql（租户/角色/既有菜单树）与 98_menu_write.sql 之后，
-- 部署清单尾部独立追加。纯种子（全量 ON CONFLICT DO NOTHING，幂等可
-- 重复），不动 schema/rls.sql，不含任何授权语句。
--
-- 解决什么问题：入口层功能权限码判定（permission middleware 调
-- fn_has_permission）需要种子权限点作为授权载体。本文件补齐既有菜单树
-- 未覆盖的管理面操作权限点，并给会话治理预铺权限点：
--   246  sys:user:transfer-owner   用户归属转移（用户管理，sort 7）
--   247  sys:user-group:assign-member 用户组成员增删（用户组管理，sort 5）
--   248  sys:role:set-data-scope   角色数据范围配置（角色管理，sort 5）
--   249  sys:role:set-menus        角色菜单授权（角色管理，sort 6）
--   254  sys:session               会话管理页面（审计管理目录，sort 3）
--   255  sys:session:query         会话列表查询（已接入会话治理 ListSessions）
--   256  sys:session:revoke        会话主动撤销（已接入会话治理 RevokeSession）
--   258  sys:config:global:query   全局配置查询（系统配置，sort 1）
--   259  sys:config:global:create  全局配置创建（系统配置，sort 2）
--   260  sys:config:global:update  全局配置更新（系统配置，sort 3）
--   261  sys:config:global:delete  全局配置删除（系统配置，sort 4）
--   262  sys:config:tenant:query   租户配置查询（租户配置，sort 1）
--   263  sys:config:tenant:create  租户配置创建（租户配置，sort 2）
--   264  sys:config:tenant:update  租户配置更新（租户配置，sort 3）
--   265  sys:config:tenant:delete  租户配置删除（租户配置，sort 4）
--   257  sys:config:translation      通用翻译页面（配置管理目录，sort 4）
--   266  sys:config:feature-flag:query   功能开关查询（功能特性，sort 1）
--   267  sys:config:feature-flag:create  功能开关创建（功能特性，sort 2）
--   268  sys:config:feature-flag:update  功能开关更新（功能特性，sort 3）
--   269  sys:config:feature-flag:delete  功能开关删除（功能特性，sort 4）
--   270  sys:config:translation:query    翻译查询（通用翻译，sort 1）
--   271  sys:config:translation:create   翻译创建（通用翻译，sort 2）
--   272  sys:config:translation:update   翻译更新（通用翻译，sort 3）
--   273  sys:config:translation:delete   翻译删除（通用翻译，sort 4）
--
-- 铺设粒度（与既有 seed.sql 菜单树口径一致）：本文件落位时管理 API 已
-- 落地的模块铺到「操作」级（每个敏感写动作一码）；后实施的字典（225-228
-- 沿用 seed.sql 既有操作码）、审计日志（230/231 页面码，List/点查同码）、
-- 会话治理（255/256 操作码）、配置族（258-265 操作码）、功能开关
--（266-269 操作码）与翻译（257 页面码 + 270-273 操作码）随各自纵切接入
-- 入口层判定表；其余模块的按钮级操作码随各自纵切再铺，不预设 API 形态。
--
-- 角色映射：仅超级管理员（101，t-super-01）授予全部新码，其余角色不
-- 扩权（最小意外，演示角色的授权面维持既有种子口径）。测试租户 2
--（t-test-02，见 test_seed.sql）不复制新码——其管理员（105）的可见面
-- 维持既有种子口径。
--
-- 写入经普通 INSERT（部署面超级用户执行）；挂在 sys_menu /
-- sys_role_menu 上的 trg_bump_perm_version_tenant 会顺带 bump 租户级
-- 权限版本，属预期行为（种子装载即权限事实变更）。
-- ============================================================

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
VALUES (254, 1, '会话管理', 'sys:session', 229, 2, 'session', '/audit/session', 'audit/session/index', 3, 1,
        '2026-11-03 10:00:00.000', 1, '2026-11-03 10:00:00.000', NULL, 't-super-01') ON CONFLICT DO NOTHING;

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

-- 二级菜单：通用翻译（id 257，父 232 配置管理）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (257, 1, '通用翻译', 'sys:config:translation', 232, 2, 'translation', '/config/translation',
        'config/translation/index', 4, 1, '2026-11-04 10:00:00.000', 1, '2026-11-04 10:00:00.000', NULL,
        't-super-01') ON CONFLICT DO NOTHING;

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

-- 超级管理员 (101) → 新增权限点全部授予
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 246, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 247, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 248, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 249, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 254, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 255, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 256, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 258, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 259, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 260, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 261, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 262, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 263, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 264, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 265, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 257, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 266, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 267, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 268, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 269, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 270, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 271, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 272, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 273, 't-super-01', 1) ON CONFLICT DO NOTHING;
