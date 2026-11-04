-- ============================================================
-- 99_perm_seed.sql — 管理面功能权限码种子（M3-6，D4/D5 结论）
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
--   255  sys:session:query         会话列表查询（M5-2 实施前仅作权限点）
--   256  sys:session:revoke        会话主动撤销（M5-2 实施前仅作权限点）
--
-- 铺设粒度（与既有 seed.sql 菜单树口径一致）：管理 API 已落地的模块
-- 铺到「操作」级（每个敏感写动作一码）；M4-M7 才实施的管理 API（字典
-- 读写、日志查询、会话治理、配置、租户）沿用 seed.sql 既有页面级权限
-- 点（224-228 / 229-231 / 219-223 / 232-235），其中会话治理补查询/撤销
-- 两枚操作码（255/256），待 M5-2 端点落地后接入判定表；其余模块的按钮
-- 级操作码随各自纵切再铺，不预设 API 形态。
--
-- 角色映射：仅超级管理员（101，t-super-01）授予全部新码，其余角色不
-- 扩权（最小意外，演示角色的授权面维持既有种子口径）。演示租户
--（t-demo-01）不复制新码——其管理员（105）的可见面维持既有种子口径。
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
