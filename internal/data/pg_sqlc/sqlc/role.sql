-- ============================================================
-- role.sql — 应用运行角色与演示用户授权
--
-- 在 schema/ 全部文件之后、rls.sql 之前执行：本文件只建角色与授权，
-- 不声明任何函数/表对象，因此不列入 sqlc.yaml 的 schema 输入；
-- 后续 operate/expose 的函数授权语句（GRANT ... TO test_u 等）
-- 依赖本文件创建的角色先行存在。
--
-- 内容：
--   1. grape_u 运行角色与 test_u 演示用户（DO 块判存，重复执行安全）
--   2. schema 使用权、全表/全序列 DML 授权、默认权限、RLS 会话变量
--      写入权（GRANT SET ON PARAMETER）
--
-- 口令以 {{GRAPE_U_PASSWORD}} / {{TEST_U_PASSWORD}} 占位，由 dbdeploy
-- 在执行前一次性替换，仓库内不落明文；角色已存在时不改写口令。
-- GRANT CONNECT ON DATABASE <库名> 依赖库名，由部署方在连接目标库后
-- 执行（dbdeploy 以 current_database() 动态补齐）。
--
-- grape_u 供 grape 应用运行连接使用，test_u 供演示/验收使用；均为普通
-- 数据库角色：真实承受 RLS 与 FORCE（超级用户会绕过 RLS，运行必须走
-- 这类连接）。
-- ============================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'grape_u') THEN
        CREATE USER grape_u WITH PASSWORD '{{GRAPE_U_PASSWORD}}';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'test_u') THEN
        CREATE USER test_u WITH PASSWORD '{{TEST_U_PASSWORD}}';
    END IF;
END
$$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO grape_u, test_u;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO grape_u, test_u;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO grape_u, test_u;

-- 功能入口表对运行用户只读：写入走管理端
REVOKE INSERT, UPDATE, DELETE ON public.sys_menu, public.sys_config FROM grape_u, test_u;

-- 其他角色所建表不自动放行，需要时另行显式 GRANT
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO grape_u, test_u;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO grape_u, test_u;

-- RLS 会话变量写入权（PG15+）：运行连接每事务 set_config(...) 的前提。
-- app.tenant_id 用于租户边界；app.session_* 仅供会话校验/刷新事务
-- 声明令牌哈希（见 sys_session_token_* 策略）
GRANT SET ON PARAMETER app.tenant_id TO grape_u, test_u;
GRANT SET ON PARAMETER app.session_token_hash TO grape_u, test_u;
GRANT SET ON PARAMETER app.session_refresh_token_hash TO grape_u, test_u;
GRANT SET ON PARAMETER app.session_new_refresh_token_hash TO grape_u, test_u;
