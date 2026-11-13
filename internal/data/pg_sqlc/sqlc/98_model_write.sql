-- ============================================================
-- 98_model_write.sql — sys_model 平台行受控写面（建/改/软删）
--
-- 背景：sys_model 单表双态——tenant_id='' 为平台行，所有租户可读；租户行
-- 写自己的租户 id。RLS 读策略放行「本租户行 + 平台行」，写策略只放行本
-- 租户行（tenant_id = app.tenant_id），因此平台行无法经普通 DML 写入。
-- 平台行只能经本文件的 SECURITY DEFINER 函数写入：属主（部署超级用户）
-- 绕过 RLS，函数内把目标固定为 tenant_id = ''，要求调用方事务已设置
-- app.tenant_id（认证事务纪律，未设置即拒绝）。
--
-- 固定口径：
--   1. tenant_id 恒写空串，不接受调用方指定；
--   2. model_key/kind 是身份列，创建后不变（更新函数不覆盖）；
--   3. api_key_cipher 只写不读——更新函数按入参原值覆盖，调用方以
--      nil 表示清除；读取面由业务查询决定（动态投影不返回该列）；
--   4. updated_at 由 trg_touch_updated_at 兜底刷新。
--
-- EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC（同 98_menu_write.sql /
-- 98_config_write.sql 先例）；平台写端点在入口层由 sys:model:platform:*
-- 权限码把守，函数本身不承担权限码判定。
-- ============================================================

-- fn_model_platform_create：新建平台模型行。
-- 升级注意：生成预算两列（context_window / max_output_tokens）使参数表
-- 变化，旧签名的函数须先删除，否则历史库会残留旧重载（CREATE OR REPLACE
-- 只替换同签名函数）。
DROP FUNCTION IF EXISTS public.fn_model_platform_create(int8, int2, varchar, varchar, varchar, varchar, varchar, varchar, int4, int4, text, varchar, varchar, int8);

CREATE OR REPLACE FUNCTION public.fn_model_platform_create(
    p_id              int8,
    p_status          int2,
    p_kind            varchar,
    p_name            varchar,
    p_model_key       varchar,
    p_provider        varchar,
    p_base_url        varchar,
    p_model           varchar,
    p_dimension       int4,
    p_timeout_ms      int4,
    p_context_window  int4,
    p_max_output_tokens int4,
    p_api_key_cipher  text,
    p_api_key_env     varchar,
    p_remark          varchar,
    p_operator        int8
)
    RETURNS public.sys_model
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_model;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'model platform write requires app.tenant_id';
    END IF;

    INSERT INTO public.sys_model (id, tenant_id, status, kind, "name", model_key,
                                  provider, base_url, model, dimension, timeout_ms,
                                  context_window, max_output_tokens,
                                  api_key_cipher, api_key_env, remark,
                                  created_by, updated_by)
    VALUES (p_id, '', p_status, p_kind, p_name, p_model_key,
            p_provider, p_base_url, p_model, p_dimension, p_timeout_ms,
            p_context_window, p_max_output_tokens,
            p_api_key_cipher, p_api_key_env, p_remark,
            p_operator, p_operator)
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_model_platform_create(int8, int2, varchar, varchar, varchar, varchar, varchar, varchar, int4, int4, int4, int4, text, varchar, varchar, int8) IS '模型目录平台行受控写面：新建平台模型（tenant_id 固定为空串）。SECURITY DEFINER 以属主执行绕过 RLS 的租户写策略；函数内以 app.tenant_id 校验认证事务纪律（未设置即拒绝）。model_key 在平台行未删除行间唯一，由部分唯一索引兜底。EXECUTE 沿用默认授权 PUBLIC，权限码把守归入口层';

-- fn_model_platform_update：覆盖平台模型可变字段（不含 kind/model_key）。
-- 目标不存在、已软删或不是平台行时返回 NULL（调用方按无行映射 NotFound）。
DROP FUNCTION IF EXISTS public.fn_model_platform_update(int8, int2, varchar, varchar, varchar, varchar, int4, int4, text, varchar, varchar, int8);

CREATE OR REPLACE FUNCTION public.fn_model_platform_update(
    p_id              int8,
    p_status          int2,
    p_name            varchar,
    p_provider        varchar,
    p_base_url        varchar,
    p_model           varchar,
    p_dimension       int4,
    p_timeout_ms      int4,
    p_context_window  int4,
    p_max_output_tokens int4,
    p_api_key_cipher  text,
    p_api_key_env     varchar,
    p_remark          varchar,
    p_operator        int8
)
    RETURNS public.sys_model
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_model;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'model platform write requires app.tenant_id';
    END IF;

    UPDATE public.sys_model
    SET status          = p_status,
        "name"          = p_name,
        provider        = p_provider,
        base_url        = p_base_url,
        model           = p_model,
        dimension       = p_dimension,
        timeout_ms      = p_timeout_ms,
        context_window  = p_context_window,
        max_output_tokens = p_max_output_tokens,
        api_key_cipher  = p_api_key_cipher,
        api_key_env     = p_api_key_env,
        remark          = p_remark,
        updated_by      = p_operator
    WHERE id = p_id
      AND tenant_id = ''
      AND deleted_at IS NULL
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_model_platform_update(int8, int2, varchar, varchar, varchar, varchar, int4, int4, int4, int4, text, varchar, varchar, int8) IS '模型目录平台行受控写面：覆盖可变字段（status/name/provider/base_url/model/dimension/timeout_ms/context_window/max_output_tokens/api_key_cipher/api_key_env/remark）。SECURITY DEFINER 绕过 RLS，函数内仅命中 tenant_id='''' 的行；kind/model_key 是身份列不经本路径变更；未命中返回 NULL（调用方映射 NotFound）';

-- fn_model_platform_delete：软删除平台模型行。未命中返回 0。
CREATE OR REPLACE FUNCTION public.fn_model_platform_delete(
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
        RAISE EXCEPTION 'model platform write requires app.tenant_id';
    END IF;

    UPDATE public.sys_model
    SET deleted_at = CURRENT_TIMESTAMP,
        updated_by = p_operator
    WHERE id = p_id
      AND tenant_id = ''
      AND deleted_at IS NULL;
    GET DIAGNOSTICS v_deleted = ROW_COUNT;
    RETURN v_deleted;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_model_platform_delete(int8, int8) IS '模型目录平台行受控写面：软删除平台模型（deleted_at 置位），仅命中 tenant_id='''' 的行；未命中返回 0（调用方映射 NotFound）。SECURITY DEFINER 绕过 RLS，函数内以 app.tenant_id 校验认证事务纪律';
