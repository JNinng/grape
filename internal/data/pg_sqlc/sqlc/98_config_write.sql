-- ============================================================
-- 98_config_write.sql — sys_config 受控写面：全局配置 CRUD
-- 的 SECURITY DEFINER 固定口径写函数。
--
-- 背景：sys_config 是平台级全局配置表（不绑定租户、无 tenant_id、无
-- RLS），role.sql 已对 grape_u/test_u REVOKE INSERT/UPDATE/DELETE；
-- 运行角色保持无直写权限。配置管理（API super 方法）经本文件的受控
-- 函数写入——SECURITY DEFINER 以函数属主（部署超级用户）身份执行，
-- 绕过直写禁令，是 sys_config 唯一的写路径。
--
-- 固定口径（收在函数内，调用方只传业务字段）：
--   1. 认证事务纪律的防御性检查：sys_config 无租户列、也不设 RLS，
--      但调用方事务仍须每事务设置 app.tenant_id（IdentityTx 门面的
--      会话纪律），函数内以 current_setting('app.tenant_id', true)
--      显式校验——未设置即拒绝（fail-closed），与菜单受控写面同口径，
--      防止绕过门面的直连调用。
--   2. 身份/值语义列：config_key（唯一键）与 value_type（消费方取值
--      预处理声明）创建后不可变，更新函数只覆盖 name/status/value/
--      remark。软删口径收在删除函数内（deleted_at 置位）。
--   3. 审计与派生：created_by/updated_by 记操作者；updated_at 由
--      trg_touch_updated_at 兜底刷新。
--
-- EXECUTE 授权照 fn_cleanup_expired_sessions / fn_menu_* 先例：沿用
-- PostgreSQL 默认授权 PUBLIC（无显式 GRANT）。任何角色都能调用，但
-- 只能以函数内固定口径写入，不构成扩权面。SET search_path = public
-- 是 SECURITY DEFINER 的安全惯例（防 search_path 劫持函数体内的对象
-- 解析）。
--
-- 部署：尾部独立追加（DeployFiles 清单中位于 98_menu_write.sql 之后、
-- 99_perm_seed.sql 之前），只依赖 schema/90_config.sql 的 sys_config
-- 存在；入 sqlc catalog（函数签名），命名查询见 queries/sys_config.sql。
-- ============================================================

-- fn_config_create：新建全局配置。认证事务纪律校验（app.tenant_id 必须
-- 已设置）；config_key 在未删除行间唯一（部分唯一索引兜底，冲突以唯一
-- 约束异常上抛）；返回新建行。
CREATE OR REPLACE FUNCTION public.fn_config_create(
    p_id         int8,
    p_status     int2,
    p_name       varchar,
    p_config_key varchar,
    p_value      text,
    p_value_type varchar,
    p_remark     varchar,
    p_operator   int8
)
    RETURNS public.sys_config
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_config;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'config write requires app.tenant_id';
    END IF;

    INSERT INTO public.sys_config (id, status, "name", config_key, value, value_type,
                                   remark, created_by, updated_by)
    VALUES (p_id, p_status, p_name, p_config_key, p_value, p_value_type,
            p_remark, p_operator, p_operator)
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_config_create(int8, int2, varchar, varchar, text, varchar, varchar, int8) IS '全局配置受控写面（D2）：新建全局配置。SECURITY DEFINER 以属主（部署超级用户）执行绕过 role.sql 的直写 REVOKE——sys_config 无租户列、无 RLS，函数内仍以 app.tenant_id 校验认证事务纪律（未设置即拒绝，fail-closed），grape_u 无直写权限、只能经本函数以固定口径写入。config_key 未删除行间唯一由部分唯一索引兜底。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public 防 search_path 劫持';

-- fn_config_update：覆盖可变字段（status/name/value/remark）。
-- config_key / value_type 是身份/值语义列不经本路径变更；目标不存在或
-- 已软删返回 NULL（调用方按无行映射 NotFound）。updated_at 由触发器
-- 兜底刷新。
CREATE OR REPLACE FUNCTION public.fn_config_update(
    p_id       int8,
    p_status   int2,
    p_name     varchar,
    p_value    text,
    p_remark   varchar,
    p_operator int8
)
    RETURNS public.sys_config
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_config;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'config write requires app.tenant_id';
    END IF;

    UPDATE public.sys_config
    SET status     = p_status,
        "name"     = p_name,
        value      = p_value,
        remark     = p_remark,
        updated_by = p_operator
    WHERE id = p_id
      AND deleted_at IS NULL
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_config_update(int8, int2, varchar, text, varchar, int8) IS '全局配置受控写面（D2）：覆盖可变字段（status/name/value/remark）。SECURITY DEFINER 绕过直写 REVOKE，函数内以 app.tenant_id 校验认证事务纪律（未设置即拒绝）；config_key/value_type 是身份/值语义列不经本路径变更；目标未命中返回 NULL（调用方按无行映射 NotFound）。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';

-- fn_config_delete：软删除全局配置（deleted_at 置位）。目标不存在/已
-- 软删返回 0，调用方按无行映射 NotFound；命中返回 1。
CREATE OR REPLACE FUNCTION public.fn_config_delete(
    p_id       int8,
    p_operator int8
)
    RETURNS bigint
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant  varchar(16);
    v_deleted bigint;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'config write requires app.tenant_id';
    END IF;

    UPDATE public.sys_config
    SET deleted_at = CURRENT_TIMESTAMP,
        updated_by = p_operator
    WHERE id = p_id
      AND deleted_at IS NULL;
    GET DIAGNOSTICS v_deleted = ROW_COUNT;
    RETURN v_deleted;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_config_delete(int8, int8) IS '全局配置受控写面（D2）：软删除全局配置（deleted_at 置位）。SECURITY DEFINER 绕过直写 REVOKE，函数内以 app.tenant_id 校验认证事务纪律（未设置即拒绝）；目标未命中返回 0（调用方映射 NotFound），命中返回 1。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';
