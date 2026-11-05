-- ======================================================================
-- test_seed.sql — 集成测试专用种子数据
-- 依赖：完整生产清单（DeployFiles，seed.sql 提供树根租户 t-super-01）之后执行；
-- 仅由测试部署清单（deploy.TestDeployFiles）装载，生产 dbdeploy 不执行。
-- 以超级用户/表 owner 连接执行（RLS 已启用，超级用户绕过 RLS）。
-- 仅用于全新建库的一次性部署；重复执行会重复追加登录/操作日志。
--
-- 组成（除用户 12 superplain 属 t-super-01 外，全部属测试租户
-- t-test-01 / t-test-02，与生产默认数据分离）：
--   1. 测试租户（t-test-01 主测试租户；t-test-02 跨租户对照租户）；
--   2. 部门树（t-test-01：总公司 → 技术部/产品部/设计部/市场部/财务部，
--      技术部 → 前端组/后端组）；
--   3. 角色与数据范围（102-109 覆盖 data_scope=2/3/4/5/6 逐档演示，
--      110 为测试管理员 data_scope=7；105 属 t-test-02 作对照）；
--   4. 用户与角色映射（2-5 基础 + 6-10 数据范围演示 + 11 测试管理员 +
--      12 平台租户无角色用户；用户 5 属 t-test-02 作跨租户对照）；
--   5. 用户组与成员映射（用户组档位演示）；
--   6. basic 表归属矩阵（11 行，覆盖归属三元组各维度 + 无归属行 +
--      跨租户对照行），期望可见性由部署方验收场景断言；
--   7. 菜单副本与角色-菜单映射（t-test-01 用 32xx 副本，t-test-02 用
--      12xx 副本——RLS 下跨租户 JOIN 不可见，租户角色只能授予本租户
--      菜单副本）；
--   8. 登录/操作日志示例（t-test-01 为主，t-test-02 一条对照）；
--   9. t-test-02 的租户配置与功能开关；
--  10. 字典演示数据（t-test-01：证件类型/客户来源/行业分类等租户运营型
--      分类；t-test-02 无字典作跨租户对照；系统级枚举不进字典，由列
--      注释 @options 声明）。
--
-- 密码统一为 "123456"（scrypt 加密）；登录校验通过后由应用透明重哈希为 argon2id。
-- ======================================================================

-- 历史库守卫：test_seed 复用固定主键，仅适用于全新建库。检测到旧演示/
-- 测试租户、平台租户旧字典或已播种日志时显式失败，避免
-- ON CONFLICT DO NOTHING 静默产生残缺行或跨租户孤儿数据。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM public.sys_tenant
               WHERE id IN ('t-test-01', 't-test-02', 't-demo-01', 't-dev-01', 't-stg-01'))
       OR EXISTS (SELECT 1 FROM public.sys_dict_type WHERE tenant_id = 't-super-01')
       OR EXISTS (SELECT 1 FROM public.sys_dict_data WHERE tenant_id = 't-super-01')
       OR EXISTS (SELECT 1 FROM public.sys_login_log WHERE tenant_id = 't-test-01') THEN
        RAISE EXCEPTION 'test_seed.sql requires a fresh database; rebuild it before applying test seed';
    END IF;
END
$$;

-- ===========================
-- 1. 测试租户 (sys_tenant)：树根 t-super-01 名下的直接子租户
-- ===========================
INSERT INTO public.sys_tenant (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, code,
                               "name", parent_id, contact_name, contact_phone, expired_at, max_user_count, default_locale)
VALUES ('t-test-01', 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-super-01',
        'test', '测试租户', 't-super-01', '测试联系人', '13800000020', NULL, 100, 'zh-CN') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_tenant (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, code,
                               "name", parent_id, contact_name, contact_phone, expired_at, max_user_count, default_locale)
VALUES ('t-test-02', 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-super-01',
        'test2', '测试租户 2', 't-super-01', '测试联系人 2', '13800000021', NULL, 50, 'zh-CN') ON CONFLICT DO NOTHING;

-- ===========================
-- 2. 部门表 (sys_dept)：t-test-01 的组织树
--    总公司 → 技术部/产品部/设计部/市场部/财务部；技术部 → 前端组/后端组
-- ===========================
INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (1, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01',
        NULL, '总公司', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (2, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01',
        1, '技术部', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (3, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01',
        1, '产品部', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (4, 1, 1, '2026-06-01 10:15:00.000', 1, '2026-06-01 10:15:00.000', NULL, 't-test-01',
        1, '设计部', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (5, 1, 1, '2026-06-01 10:20:00.000', 1, '2026-06-01 10:20:00.000', NULL, 't-test-01',
        1, '市场部', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (6, 1, 1, '2026-06-01 10:25:00.000', 1, '2026-06-01 10:25:00.000', NULL, 't-test-01',
        1, '财务部', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (7, 1, 1, '2026-06-01 10:30:00.000', 1, '2026-06-01 10:30:00.000', NULL, 't-test-01',
        2, '前端组', 1) ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dept (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             parent_id, "name", owner_user_id)
VALUES (8, 1, 1, '2026-06-01 10:35:00.000', 1, '2026-06-01 10:35:00.000', NULL, 't-test-01',
        2, '后端组', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 3. 角色表 (sys_role)
--    102-109 为数据范围演示角色（与 basic 表归属矩阵配合使 data_scope
--    2/3/4/5/6 逐档可验）；110 为测试管理员（data_scope=7）。
--    105 属 t-test-02，作跨租户对照。
-- ===========================

-- 102. 部门管理员（本部门及子部门）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (102, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01',
        '部门管理员', 'dept_admin', 2, 3, 2, '管理本部门及子部门的用户与数据') ON CONFLICT DO NOTHING;

-- 103. 普通用户（仅本人数据）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (103, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01',
        '普通用户', 'user', 2, 1, NULL, '默认角色，仅可操作本人数据') ON CONFLICT DO NOTHING;

-- 104. 审计员（全租户只读）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (104, 1, 1, '2026-06-01 10:15:00.000', 1, '2026-06-01 10:15:00.000', NULL, 't-test-01',
        '审计员', 'auditor', 2, 7, NULL, '拥有全租户数据查看权限，用于合规审计') ON CONFLICT DO NOTHING;

-- 105. 测试租户 2 管理员（归属 t-test-02）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (105, 1, 1, '2026-06-01 10:20:00.000', 1, '2026-06-01 10:20:00.000', NULL, 't-test-02',
        '租户管理员', 'tenant_admin', 2, 7, NULL, '测试租户 2 的管理角色') ON CONFLICT DO NOTHING;

-- 106. 指定人演示（data_scope=4，经 sys_role_id_scope type=1 指定）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (106, 1, 1, '2026-06-01 10:40:00.000', 1, '2026-06-01 10:40:00.000', NULL, 't-test-01',
        '指定人演示', 'demo_scope_users', 2, 4, NULL, '演示 data_scope=4：指定 lisi/wangwu 的数据') ON CONFLICT DO NOTHING;

-- 107. 指定部门演示（data_scope=5，经 sys_role_id_scope type=2 指定）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (107, 1, 1, '2026-06-01 10:45:00.000', 1, '2026-06-01 10:45:00.000', NULL, 't-test-01',
        '指定部门演示', 'demo_scope_depts', 2, 5, NULL, '演示 data_scope=5：指定市场部的数据') ON CONFLICT DO NOTHING;

-- 108. 用户组演示（data_scope=6，经 sys_role_id_scope type=3 指定）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (108, 1, 1, '2026-06-01 10:50:00.000', 1, '2026-06-01 10:50:00.000', NULL, 't-test-01',
        '用户组演示', 'demo_scope_groups', 2, 6, NULL, '演示 data_scope=6：指定产品设计组的数据') ON CONFLICT DO NOTHING;

-- 109. 本部门演示（data_scope=2）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (109, 1, 1, '2026-06-01 10:55:00.000', 1, '2026-06-01 10:55:00.000', NULL, 't-test-01',
        '本部门演示', 'demo_scope_dept', 2, 2, NULL, '演示 data_scope=2：本部门（前端组）的数据') ON CONFLICT DO NOTHING;

-- 110. 测试管理员（全租户数据范围）
INSERT INTO public.sys_role (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name",
                             code, role_type, data_scope, scope_dept_id, remark)
VALUES (110, 1, 1, '2026-06-01 11:00:00.000', 1, '2026-06-01 11:00:00.000', NULL, 't-test-01',
        '测试管理员', 'test_admin', 2, 7, NULL, '测试专用：全租户数据范围') ON CONFLICT DO NOTHING;

-- ===========================
-- 4. 角色数据范围表 (sys_role_id_scope)
-- ===========================

-- 106 指定人演示 → 指定用户 3（lisi）
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, created_by, created_at, updated_by,
                                      updated_at, deleted_at, tenant_id)
VALUES (1003, 1, 1, 106, 3, 1, '2026-06-01 10:40:00.000', 1, '2026-06-01 10:40:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 106 指定人演示 → 指定用户 4（wangwu）
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, created_by, created_at, updated_by,
                                      updated_at, deleted_at, tenant_id)
VALUES (1004, 1, 1, 106, 4, 1, '2026-06-01 10:40:00.000', 1, '2026-06-01 10:40:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 107 指定部门演示 → 指定部门 5（市场部）
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, created_by, created_at, updated_by,
                                      updated_at, deleted_at, tenant_id)
VALUES (1005, 1, 2, 107, 5, 1, '2026-06-01 10:45:00.000', 1, '2026-06-01 10:45:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 108 用户组演示 → 指定用户组 2（产品设计组）
INSERT INTO public.sys_role_id_scope (id, status, "type", role_id, data_id, created_by, created_at, updated_by,
                                      updated_at, deleted_at, tenant_id)
VALUES (1006, 1, 3, 108, 2, 1, '2026-06-01 10:50:00.000', 1, '2026-06-01 10:50:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- ===========================
-- 5. 角色分发部门映射表 (sys_role_allowed_dept)
--    角色 102 可分发到部门集 {2,5}：部门 5 命中映射但不在任何演示
--    操作者的数据范围内，用于独立验证"操作者部门命中映射 → 豁免
--    data_scope 覆盖"分支（fn_can_assign_role 分支一）
-- ===========================
INSERT INTO public.sys_role_allowed_dept (id, status, role_id, dept_id, created_by, created_at, updated_by,
                                          updated_at, deleted_at, tenant_id)
VALUES (1101, 1, 102, 2, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_role_allowed_dept (id, status, role_id, dept_id, created_by, created_at, updated_by,
                                          updated_at, deleted_at, tenant_id)
VALUES (1102, 1, 102, 5, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- ===========================
-- 6. 用户表 (sys_user)
--    1-5 为基础种子；6-9 为数据范围演示用户（各持单一演示角色）；
--    10 为角色分配演示目标（无角色）；11 为测试管理员。
--    密码统一为 "123456"。
-- ===========================

-- 2. zhangsan — 技术部员工（普通用户 + 用户组演示 → 多角色并集演示）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (2, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01',
        1, 2, 'zhangsan', '张三',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'zhangsan@example.com', '13800000002', NULL, '技术部-后端组', 'zh-CN') ON CONFLICT DO NOTHING;

-- 3. lisi — 产品部员工
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (3, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01',
        1, 3, 'lisi', '李四',
        '{scrypt}$e0801$Ztjk9jl3scCFvshR0OgATvTBSUqu5kYn0k803sKHm4GAoF43WujIOnG2x6PFELsSYLaTvHtTrmyCNPrjTX6kqg==$RzY/GgWF0uSmLNnYC7g+gYO5DD8Tas8UYsuZEi2lGbI=',
        'lisi@example.com', '13800000003', NULL, '产品部-产品经理', 'zh-CN') ON CONFLICT DO NOTHING;

-- 4. wangwu — 设计部员工
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (4, 1, 1, '2026-06-01 10:15:00.000', 1, '2026-06-01 10:15:00.000', NULL, 't-test-01',
        1, 4, 'wangwu', '王五',
        '{scrypt}$e0801$sMnGKuCundaB6372FYv4Gha7tpPEFQnD2cswbudIfhjRY392rrufnUnUV/NgET+EZ2nmMtpD39cUwNfPNrD8pQ==$+q8C/PcFbJPpWd6yMUH9wIDqLHVmMPG+JK567/Q+XCw=',
        'wangwu@example.com', '13800000004', NULL, '设计部-UI 设计师', 'zh-CN') ON CONFLICT DO NOTHING;

-- 5. zhaoliu — 测试租户 2 用户（跨租户对照组）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (5, 1, 1, '2026-06-01 10:20:00.000', 1, '2026-06-01 10:20:00.000', NULL, 't-test-02',
        5, 1, 'zhaoliu', '赵六',
        '{scrypt}$e0801$3KDaCR21e9FCbuLRzxiNApLS4M8w5xzu82jgpM+7s9AfPUX8VlhnQauYkQLS9yyTVF0kaZ3K8sVyA/QnKSklLw==$3DiwLAxdmfrOhuCfvMKCIRiiB57BKQ/PLrkECnXKEaE=',
        'zhaoliu@example.com', '13800000005', NULL, '测试租户 2 的普通用户', 'zh-CN') ON CONFLICT DO NOTHING;

-- 6. sunqi — 前端组员工（本部门演示，data_scope=2）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (6, 1, 1, '2026-06-01 11:00:00.000', 1, '2026-06-01 11:00:00.000', NULL, 't-test-01',
        1, 7, 'sunqi', '孙七',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'sunqi@example.com', '13800000006', NULL, '技术部-前端组', 'zh-CN') ON CONFLICT DO NOTHING;

-- 7. zhouba — 技术部员工（部门管理员，data_scope=3 子树演示）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (7, 1, 1, '2026-06-01 11:05:00.000', 1, '2026-06-01 11:05:00.000', NULL, 't-test-01',
        1, 2, 'zhouba', '周八',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'zhouba@example.com', '13800000007', NULL, '技术部-部门管理员', 'zh-CN') ON CONFLICT DO NOTHING;

-- 8. wujiu — 设计部员工（指定人演示，data_scope=4）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (8, 1, 1, '2026-06-01 11:10:00.000', 1, '2026-06-01 11:10:00.000', NULL, 't-test-01',
        1, 4, 'wujiu', '吴九',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'wujiu@example.com', '13800000008', NULL, '设计部-指定人演示', 'zh-CN') ON CONFLICT DO NOTHING;

-- 9. zhengshi — 财务部员工（指定部门演示，data_scope=5）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (9, 1, 1, '2026-06-01 11:15:00.000', 1, '2026-06-01 11:15:00.000', NULL, 't-test-01',
        1, 6, 'zhengshi', '郑十',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'zhengshi@example.com', '13800000009', NULL, '财务部-指定部门演示', 'zh-CN') ON CONFLICT DO NOTHING;

-- 10. demo-market — 市场部员工，无任何角色；角色 102 分发映射（部门集
--     {2,5}）的分配演示目标：部门 5 命中映射，但不在任何演示操作者的
--     数据范围内，用于独立验证"操作者部门命中映射 → 豁免 data_scope
--     覆盖"分支（fn_can_assign_role 分支一）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (10, 1, 1, '2026-06-01 11:20:00.000', 1, '2026-06-01 11:20:00.000', NULL, 't-test-01',
        1, 5, 'demo-market', '市场演示',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'demo-market@example.com', '13800000010', NULL, '市场部-分配演示目标', 'zh-CN') ON CONFLICT DO NOTHING;

-- 11. testadmin — 测试管理员（全租户数据范围；basic 矩阵等验收场景的
--     data_scope=7 操作者身份）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (11, 1, 1, '2026-06-01 11:25:00.000', 1, '2026-06-01 11:25:00.000', NULL, 't-test-01',
        1, 1, 'testadmin', '测试管理员',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'testadmin@example.com', '13800000011', NULL, '测试专用管理员账号', 'zh-CN') ON CONFLICT DO NOTHING;

-- 12. superplain — 平台租户的测试专用普通用户（无任何角色）：授权边界
--     用例的"同租户无权限码"身份（吊销他人授权须先经权限码判定，
--     跨租户目标在可见性判定即 404，需要同租户行才能走到 403 分支）
INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (12, 1, 1, '2026-06-01 11:30:00.000', 1, '2026-06-01 11:30:00.000', NULL, 't-super-01',
        1, NULL, 'superplain', '平台普通用户',
        '{scrypt}$e0801$yVcA71vEiZj0CtnDIGwcXLkxU4e5p32yOlBIjss1SpeWyrPITLNc+AVe/L/SgMwt4R0vcbVpEhGJIAqTTlw1Xw==$AVWyQX1A7rz1fqYFHi0dwd2JdoIWT8l4VHcJ4n7yaMw=',
        'superplain@example.com', '13800000012', NULL, '测试专用：平台租户无角色用户', 'zh-CN') ON CONFLICT DO NOTHING;

-- ===========================
-- 7. 用户角色映射表 (sys_user_role_mapping)
-- ===========================
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (2, 103, 't-test-01', 1) ON CONFLICT DO NOTHING;
-- zhangsan 追加用户组演示角色 → 与 103 组成多角色并集演示
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (2, 108, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (3, 103, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (4, 102, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (5, 105, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (6, 109, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (7, 102, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (8, 106, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (9, 107, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_role_mapping (user_id, role_id, tenant_id, created_by)
VALUES (11, 110, 't-test-01', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 8. 用户组表 (sys_user_group) 与成员映射
-- ===========================
INSERT INTO public.sys_user_group (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                                   "name", code, owner_user_id, remark)
VALUES (1, 1, 1, '2026-06-01 10:30:00.000', 1, '2026-06-01 10:30:00.000', NULL, 't-test-01',
        '核心开发组', 'core_dev', 1, '核心技术团队成员') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_user_group (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                                   "name", code, owner_user_id, remark)
VALUES (2, 1, 1, '2026-06-01 10:35:00.000', 1, '2026-06-01 10:35:00.000', NULL, 't-test-01',
        '产品设计组', 'product_design', 1, '产品与设计团队成员') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_user_group_mapping (group_id, user_id, tenant_id, created_by)
VALUES (1, 2, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_group_mapping (group_id, user_id, tenant_id, created_by)
VALUES (2, 3, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_user_group_mapping (group_id, user_id, tenant_id, created_by)
VALUES (2, 4, 't-test-01', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 9. basic 表归属矩阵（数据范围守卫的验收数据）
--    id=1..10 属 t-test-01；id=11 属 t-test-02（跨租户对照行，
--    即使 data_scope=7 也不可见——RLS 租户隔离兜底）。
--    各行期望可见性由部署方验收场景断言：
--      1  owner_user_id=3           → lisi / wujiu / zhangsan（用户组 2 成员）
--      2  owner_dept_id=2           → zhouba（子树）/ zhangsan（仅本人档位含本部门）
--      3  owner_dept_id=7           → sunqi（本部门）/ zhouba（子树）
--      4  owner_user_id=4           → wujiu（指定人）/ zhangsan（用户组 2 成员）
--      5  owner_dept_id=5           → zhengshi（指定部门）
--      6  owner_user_group_id=2     → zhangsan（用户组档位）
--      7  owner_user_id=2           → zhangsan（仅本人）
--      8  无归属                    → 仅 data_scope=7（testadmin）可见：守卫返回 NULL
--                                     放行全部（in_any 三态）；
--                                     其余档位对 NULL 归属恒不命中
--      9  owner_user_id=6           → sunqi（仅本人）
--      10 owner_dept_id=8           → zhouba（子树，部门 8 无成员）
--      11 t-test-02/owner_user_id=5 → 仅 t-test-02 租户内
-- ===========================
INSERT INTO public.basic (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                          owner_user_id, owner_dept_id, owner_user_group_id)
VALUES (1,  1, 1, '2026-06-02 10:00:00.000', 1, '2026-06-02 10:00:00.000', NULL, 't-test-01', 3,    NULL, NULL),
       (2,  1, 1, '2026-06-02 10:01:00.000', 1, '2026-06-02 10:01:00.000', NULL, 't-test-01', NULL, 2,    NULL),
       (3,  1, 1, '2026-06-02 10:02:00.000', 1, '2026-06-02 10:02:00.000', NULL, 't-test-01', NULL, 7,    NULL),
       (4,  1, 1, '2026-06-02 10:03:00.000', 1, '2026-06-02 10:03:00.000', NULL, 't-test-01', 4,    NULL, NULL),
       (5,  1, 1, '2026-06-02 10:04:00.000', 1, '2026-06-02 10:04:00.000', NULL, 't-test-01', NULL, 5,    NULL),
       (6,  1, 1, '2026-06-02 10:05:00.000', 1, '2026-06-02 10:05:00.000', NULL, 't-test-01', NULL, NULL, 2),
       (7,  1, 1, '2026-06-02 10:06:00.000', 1, '2026-06-02 10:06:00.000', NULL, 't-test-01', 2,    NULL, NULL),
       (8,  1, 1, '2026-06-02 10:07:00.000', 1, '2026-06-02 10:07:00.000', NULL, 't-test-01', NULL, NULL, NULL),
       (9,  1, 1, '2026-06-02 10:08:00.000', 1, '2026-06-02 10:08:00.000', NULL, 't-test-01', 6,    NULL, NULL),
       (10, 1, 1, '2026-06-02 10:09:00.000', 1, '2026-06-02 10:09:00.000', NULL, 't-test-01', NULL, 8,    NULL),
       (11, 1, 1, '2026-06-02 10:10:00.000', 1, '2026-06-02 10:10:00.000', NULL, 't-test-02', 5,    NULL, NULL)
ON CONFLICT DO NOTHING;

-- ===========================
-- 10. 菜单副本 (sys_menu)
--     菜单为租户数据（带 tenant_id + RLS），租户开通时从平台模板复制
--     所需菜单子树。RLS 下跨租户 JOIN 不可见：租户角色只能授予本租户
--     菜单副本，不可映射 t-super-01 的 2xx 模板。
--     t-test-01 用 32xx 段（覆盖角色 102/103/104 所需子树）；
--     t-test-02 用 12xx 段（角色 105 所需子树）。
-- ===========================

-- ---------- t-test-01 菜单副本 (32xx) ----------

-- 一级目录：系统管理（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3201, 1, '系统管理', 'sys:manage', NULL, 1, 'setting', '/system', NULL, 1, 1, '2026-06-02 09:00:00.000', 1,
        '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 二级菜单：用户管理（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3202, 1, '用户管理', 'sys:user', 3201, 2, 'user', '/system/user', 'system/user/index', 1, 1,
        '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-查询（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3203, 1, '用户查询', 'sys:user:query', 3202, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-新增（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3204, 1, '用户新增', 'sys:user:create', 3202, 3, 2, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-编辑（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3205, 1, '用户编辑', 'sys:user:update', 3202, 3, 3, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-重置密码（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3207, 1, '重置密码', 'sys:user:reset-pwd', 3202, 3, 5, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 二级菜单：字典管理（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3224, 1, '字典管理', 'sys:dict', 3201, 2, 'dict', '/system/dict', 'system/dict/index', 5, 1,
        '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-查询（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3225, 1, '字典查询', 'sys:dict:query', 3224, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 一级目录：审计管理（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3229, 1, '审计管理', 'sys:audit', NULL, 1, 'audit', '/audit', NULL, 2, 1, '2026-06-02 09:00:00.000', 1,
        '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 二级菜单：操作日志（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3230, 1, '操作日志', 'sys:audit:operation-log', 3229, 2, 'operation-log', '/audit/operation-log',
        'audit/operation-log/index', 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 二级菜单：登录日志（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3231, 1, '登录日志', 'sys:audit:login-log', 3229, 2, 'login-log', '/audit/login-log', 'audit/login-log/index', 2,
        1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 一级目录：部门管理（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3236, 1, '部门管理', 'sys:dept', NULL, 1, 'dept', '/dept', 'dept/index', 4, 1, '2026-06-02 09:00:00.000', 1,
        '2026-06-02 09:00:00.000', NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-查询（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3237, 1, '部门查询', 'sys:dept:query', 3236, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-新增（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3238, 1, '部门新增', 'sys:dept:create', 3236, 3, 2, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-编辑（t-test-01）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (3239, 1, '部门编辑', 'sys:dept:update', 3236, 3, 3, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-01') ON CONFLICT DO NOTHING;

-- ---------- t-test-02 菜单副本 (12xx) ----------

-- 一级目录：系统管理（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1201, 1, '系统管理', 'sys:manage', NULL, 1, 'setting', '/system', NULL, 1, 1, '2026-06-02 09:00:00.000', 1,
        '2026-06-02 09:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 二级菜单：用户管理（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1202, 1, '用户管理', 'sys:user', 1201, 2, 'user', '/system/user', 'system/user/index', 1, 1,
        '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-查询（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1203, 1, '用户查询', 'sys:user:query', 1202, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-新增（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1204, 1, '用户新增', 'sys:user:create', 1202, 3, 2, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 三级按钮：用户管理-编辑（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1205, 1, '用户编辑', 'sys:user:update', 1202, 3, 3, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 二级菜单：字典管理（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1224, 1, '字典管理', 'sys:dict', 1201, 2, 'dict', '/system/dict', 'system/dict/index', 5, 1,
        '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 三级按钮：字典管理-查询（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1225, 1, '字典查询', 'sys:dict:query', 1224, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 一级目录：部门管理（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, icon, route_path, component,
                             sort_order, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1236, 1, '部门管理', 'sys:dept', NULL, 1, 'dept', '/dept', 'dept/index', 4, 1, '2026-06-02 09:00:00.000', 1,
        '2026-06-02 09:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- 三级按钮：部门管理-查询（t-test-02）
INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type, sort_order, created_by,
                             created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (1237, 1, '部门查询', 'sys:dept:query', 1236, 3, 1, 1, '2026-06-02 09:00:00.000', 1, '2026-06-02 09:00:00.000',
        NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- ===========================
-- 11. 角色-菜单映射表 (sys_role_menu)
--     102 部门管理员 → 系统管理目录 + 用户管理查询/新增/编辑/重置密码 +
--                      部门管理全部（一级目录必须分配，否则子菜单成为
--                      孤儿节点，前端无法构建菜单树）
--     103 普通用户   → 系统管理目录 + 仅用户管理查询 + 部门查询
--     104 审计员     → 审计管理全部菜单（只读，无写权限按钮）+
--                      系统管理目录 + 用户管理与字典查询
--     105 测试租户 2 管理员 → 12xx 副本
-- ===========================

-- 部门管理员 (102) → 32xx 副本
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3201, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3202, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3203, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3204, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3205, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3207, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3236, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3237, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3238, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (102, 3239, 't-test-01', 1) ON CONFLICT DO NOTHING;

-- 普通用户 (103) → 32xx 副本
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (103, 3201, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (103, 3202, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (103, 3203, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (103, 3236, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (103, 3237, 't-test-01', 1) ON CONFLICT DO NOTHING;

-- 审计员 (104) → 32xx 副本
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3229, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3230, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3231, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3201, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3202, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3203, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3224, 't-test-01', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (104, 3225, 't-test-01', 1) ON CONFLICT DO NOTHING;

-- 测试租户 2 管理员 (105) → 12xx 副本
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1201, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1202, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1203, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1204, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1205, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1224, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1225, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1236, 't-test-02', 1) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by)
VALUES (105, 1237, 't-test-02', 1) ON CONFLICT DO NOTHING;

-- ===========================
-- 12. 登录日志 (sys_login_log) — 示例数据
--     t-test-01 共 4 条；t-test-02 一条（zhaoliu）作对照。
-- ===========================
INSERT INTO public.sys_login_log (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success,
                                  failure_reason, login_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', '192.168.1.100', 'Mozilla/5.0 Chrome/120.0', 'password', true, NULL,
        '2026-06-30 08:00:00.000', 1, '2026-06-30 08:00:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_login_log (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success,
                                  failure_reason, login_at, created_by, created_at)
VALUES (2, 'zhangsan', 't-test-01', '192.168.1.101', 'Mozilla/5.0 Chrome/120.0', 'password', true, NULL,
        '2026-06-30 08:30:00.000', 2, '2026-06-30 08:30:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_login_log (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success,
                                  failure_reason, login_at, created_by, created_at)
VALUES (3, 'lisi', 't-test-01', '192.168.1.102', 'Mozilla/5.0 Firefox/121.0', 'password', false, '密码错误',
        '2026-06-30 09:00:00.000', 3, '2026-06-30 09:00:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_login_log (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success,
                                  failure_reason, login_at, created_by, created_at)
VALUES (3, 'lisi', 't-test-01', '192.168.1.102', 'Mozilla/5.0 Firefox/121.0', 'password', true, NULL,
        '2026-06-30 09:01:00.000', 3, '2026-06-30 09:01:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_login_log (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success,
                                  failure_reason, login_at, created_by, created_at)
VALUES (5, 'zhaoliu', 't-test-02', '10.0.0.50', 'Mozilla/5.0 Safari/17.0', 'password', true, NULL,
        '2026-06-30 10:00:00.000', 5, '2026-06-30 10:00:00.000') ON CONFLICT DO NOTHING;

-- ===========================
-- 13. 操作日志 (sys_operation_log) — 示例数据（t-test-01 共 5 条）
-- ===========================
INSERT INTO public.sys_operation_log (user_id, username, tenant_id, operation_type, target_table, target_id, old_value,
                                      new_value, ip_address, api_path, http_method, duration_ms, result_code,
                                      request_id, operated_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', 'CREATE', 'sys_user', '2',
        NULL,
        '{"name":"zhangsan","nickname":"张三","dept_id":2}',
        '192.168.1.100', '/api/system/user', 'POST', 45, '200', 'req-001', '2026-06-01 10:05:00.000', 1,
        '2026-06-01 10:05:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_operation_log (user_id, username, tenant_id, operation_type, target_table, target_id, old_value,
                                      new_value, ip_address, api_path, http_method, duration_ms, result_code,
                                      request_id, operated_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', 'UPDATE', 'sys_role', '103',
        '{"data_scope":1}',
        '{"data_scope":1,"remark":"默认角色"}',
        '192.168.1.100', '/api/system/role/103', 'PUT', 30, '200', 'req-002', '2026-06-01 10:10:00.000', 1,
        '2026-06-01 10:10:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_operation_log (user_id, username, tenant_id, operation_type, target_table, target_id, old_value,
                                      new_value, ip_address, api_path, http_method, duration_ms, result_code,
                                      request_id, operated_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', 'DELETE', 'sys_user', '99',
        '{"name":"temp_user","nickname":"临时用户"}',
        NULL,
        '192.168.1.100', '/api/system/user/99', 'DELETE', 20, '200', 'req-003', '2026-06-01 11:00:00.000', 1,
        '2026-06-01 11:00:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_operation_log (user_id, username, tenant_id, operation_type, target_table, target_id, old_value,
                                      new_value, ip_address, api_path, http_method, duration_ms, result_code,
                                      request_id, operated_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', 'ASSIGN_ROLE', 'sys_user_role_mapping', '2',
        NULL,
        '{"user_id":2,"role_ids":[103]}',
        '192.168.1.100', '/api/system/user/2/roles', 'POST', 15, '200', 'req-004', '2026-06-01 11:05:00.000', 1,
        '2026-06-01 11:05:00.000') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_operation_log (user_id, username, tenant_id, operation_type, target_table, target_id, old_value,
                                      new_value, ip_address, api_path, http_method, duration_ms, result_code,
                                      request_id, operated_at, created_by, created_at)
VALUES (1, 'admin', 't-test-01', 'LOGIN', NULL, NULL,
        NULL, NULL,
        '192.168.1.100', '/api/auth/login', 'POST', 120, '200', 'req-005', '2026-06-30 08:00:00.000', 1,
        '2026-06-30 08:00:00.000') ON CONFLICT DO NOTHING;

-- ===========================
-- 14. 测试租户 2 的租户配置与功能开关
--     （租户配置 3-4、功能开关 5-8：有限功能 + 个性化配置演示）
-- ===========================
INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (3, 1, '测试租户显示名称', 'tenant.display-name', '测试环境 2', 'string', '租户在界面上的显示名称', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_tenant_config (id, status, "name", config_key, value, value_type, remark, created_by, created_at,
                                      updated_by, updated_at, deleted_at, tenant_id)
VALUES (4, 1, '会话超时', 'session.timeout', '3600', 'integer', '租户级会话超时覆盖全局配置（秒）', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (5, 1, '数据导出', 'export_enabled', '是否允许租户导出数据', true, '测试租户 2 开启导出功能', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (6, 1, 'API 访问', 'api_access', '是否允许租户通过 API 密钥访问系统', false, '测试租户 2 关闭 API 访问', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (7, 1, 'SSO 单点登录', 'sso_enabled', '是否允许租户配置 SSO', false, '测试租户 2 关闭 SSO', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_feature_flag (id, status, "name", flag_key, description, is_enabled, remark, created_by,
                                     created_at, updated_by, updated_at, deleted_at, tenant_id)
VALUES (8, 1, '审计日志', 'audit_log', '是否启用操作日志记录', true, '测试租户 2 开启审计日志', 1,
        '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-02') ON CONFLICT DO NOTHING;

-- ===========================
-- 15. 字典演示数据 (sys_dict_type / sys_dict_data)
--     t-test-01：租户运营型分类（证件类型/客户来源/行业分类）；t-test-02
--     无字典，供跨租户断言。数据状态/数据范围/角色类型等系统级枚举不进
--     字典（由列注释 @options 声明），故演示类型取真实业务分类。
-- ===========================
INSERT INTO public.sys_dict_type (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name", code, remark)
VALUES (1, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01',
        '证件类型', 'cert_type', '人员/客户证件类型归类') ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_type (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name", code, remark)
VALUES (2, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01',
        '客户来源', 'customer_source', '客户获客渠道归类') ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_type (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, "name", code, remark)
VALUES (3, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01',
        '行业分类', 'industry', '客户所属行业归类') ON CONFLICT DO NOTHING;

INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (1, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '身份证', 'id-card', 'string', 1, '居民身份证') ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (2, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '护照', 'passport', 'string', 2, '中华人民共和国护照') ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (3, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '港澳居民来往内地通行证', 'hk-permit', 'string', 3, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (4, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '台湾居民来往大陆通行证', 'tw-permit', 'string', 4, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (5, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '外国人永久居留身份证', 'foreign-resident-id', 'string', 5, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (6, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-test-01', 1, '其他证件', 'other', 'string', 6, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (7, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01', 2, '广告投放', 'ad', 'string', 1, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (8, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01', 2, '搜索引擎', 'search-engine', 'string', 2, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (9, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01', 2, '朋友推荐', 'referral', 'string', 3, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (10, 1, 1, '2026-06-01 10:05:00.000', 1, '2026-06-01 10:05:00.000', NULL, 't-test-01', 2, '线下活动', 'offline-event', 'string', 4, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (11, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01', 3, '互联网', 'internet', 'string', 1, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (12, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01', 3, '金融', 'finance', 'string', 2, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (13, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01', 3, '教育培训', 'education', 'string', 3, NULL) ON CONFLICT DO NOTHING;
INSERT INTO public.sys_dict_data (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id, dict_type_id, label, value, value_type, sort_order, remark)
VALUES (14, 1, 1, '2026-06-01 10:10:00.000', 1, '2026-06-01 10:10:00.000', NULL, 't-test-01', 3, '其他行业', 'other', 'string', 4, NULL) ON CONFLICT DO NOTHING;
