-- ======================================================================
-- 030_sys_user.sql — 生产种子：admin 账号
-- 依赖：010_sys_tenant.sql（租户 t-super-01 存在）。
-- 密码统一为 "123456"（scrypt 加密）；登录校验通过后由应用透明重哈希为
-- argon2id。部门树属测试数据（test_seed.sql），故 admin 不挂部门。
-- ======================================================================

INSERT INTO public.sys_user (id, status, created_by, created_at, updated_by, updated_at, deleted_at, tenant_id,
                             owner_user_id, owner_dept_id, "name", nickname, "password", email, phone, last_login_at, remark, locale)
VALUES (1, 1, 1, '2026-06-01 10:00:00.000', 1, '2026-06-01 10:00:00.000', NULL, 't-super-01',
        1, NULL, 'admin', '系统管理员',
        '{scrypt}$e0801$A7n6UaC5UUO/K09rwLa00JQXL5WipLnXHmT/k8xfCDU7Ei3szBiwEPj0O5G/MKznY5Wb3tTkRZLH+IORCt43MQ==$zk5WhEHfnzqbkGAu67xgRtvspcnsr08uuBeGFaVHOv8=',
        'admin@example.com', '13800000001', '2026-06-30 08:00:00.000', '超级管理员账号', 'zh-CN') ON CONFLICT DO NOTHING;
