-- ======================================================================
-- 060_sys_role_menu.sql — 生产种子：超级管理员菜单授权
-- 依赖：020_sys_role.sql（角色 101）与 050_sys_menu.sql（菜单树）。
-- 超级管理员授予全部菜单；角色映射写入触发租户权限版本 bump 属预期行为。
-- ======================================================================

--    超级管理员 (101) → 全部菜单（201-249、252-286）：生产种子即自举
--    权限的唯一事实源，入口层码表（internal/middleware/permission.go）
--    与前端页面/按钮权限码均以此为基准。
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
VALUES (101, 252, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 253, 't-super-01', 1) ON CONFLICT DO NOTHING;
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
VALUES (101, 257, 't-super-01', 1) ON CONFLICT DO NOTHING;
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

-- 知识库（274-278）与模型管理（279-286）：超级管理员授予全部页面与按钮权限点。
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 274, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 275, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 276, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 277, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 278, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 279, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 280, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 281, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 282, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 283, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 284, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 285, 't-super-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (101, 286, 't-super-01', 1) ON CONFLICT DO NOTHING;
