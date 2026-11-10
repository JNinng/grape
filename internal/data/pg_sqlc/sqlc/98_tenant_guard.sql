-- ============================================================
-- 98_tenant_guard.sql — sys_tenant 删除前置完整性判定：活跃直接子
-- 租户计数的受控读取面。
--
-- 背景：sys_tenant 的租户树策略（rls.sql 的 sys_tenant_tenant_isolation）
-- 只放行 tenant_id = app.tenant_id 或 id = app.tenant_id 的行——平台
-- 租户能看到挂在自身名下的直接子租户与自身，但看不到子租户自己创建的
-- 孙租户（孙行 tenant_id = 子租户）。删除租户要求「无未删除直接子租户」
-- 才能软删（自叶向根逐层删，口径同部门/菜单）；若只用 RLS 可见行做
-- count，祖父删除中间层租户时会漏掉孙租户，产生孤儿行。该完整性判定
-- 需要全局事实，不能由调用方的可见范围推导。
--
-- 为什么需要 DEFINER：本函数以属主（部署超级用户）身份执行，绕过
-- sys_tenant 的 ENABLE+FORCE 租户 RLS，直接统计 parent_id = p_id 的
-- 未删除子行——只返回计数、不返回任何行数据，不构成跨租户数据读面，
-- 也不提供跨租户写路径。函数内仍以 current_setting('app.tenant_id', true)
-- 校验认证事务纪律（未设置/空串即拒绝，fail-closed），并校验目标对
-- 调用方可见（id 或 tenant_id 命中调用方租户且未删除，否则拒绝）——
-- 消除把计数差异用作跨租户结构侧信道的可能。DEFINER 只用于取得同树
-- 之外的计数事实。
--
-- EXECUTE 授权照 fn_menu_* / fn_cleanup_expired_sessions 先例：沿用
-- PostgreSQL 默认授权 PUBLIC（无显式 GRANT）。任何角色都能调用，但只能
-- 取得一个计数，不能借本函数读取行内容。SET search_path = public 是
-- SECURITY DEFINER 的安全惯例（防 search_path 劫持函数体内的对象解析）。
--
-- 部署：尾部独立追加（DeployFiles 清单位于 98_config_write.sql 之后、
-- 清单末尾），只依赖 schema/20_tenant.sql 的 sys_tenant 存在；入
-- sqlc catalog（函数签名），命名查询见 queries/sys_tenant.sql。
--
-- 计数按 sys_tenant.parent_id 部分索引（WHERE deleted_at IS NULL，见
-- schema/20_tenant.sql）过滤；历史库升级需手工补建该索引（见 deploy_db.md）。
-- ============================================================

-- fn_tenant_active_child_count：统计指定租户名下未删除的直接子租户数
--（DEFINER 读全局事实，仅返回计数）。供删除前置校验使用：计数大于 0
-- 即拒绝软删。目标必须对调用方可见（id = app.tenant_id 或
-- tenant_id = app.tenant_id 且未删除），否则拒绝——防止把计数函数用作
-- 跨租户结构侧信道。
CREATE OR REPLACE FUNCTION public.fn_tenant_active_child_count(p_id varchar)
    RETURNS bigint
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_count  bigint;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'tenant guard requires app.tenant_id';
    END IF;

    -- 目标可见性校验：与 sys_tenant_tenant_isolation 同口径（自身 id 或
    -- tenant_id = 调用方租户），且未删除。目标不可见即拒绝，调用方无法
    -- 借计数差异探测树外租户的结构。
    IF NOT EXISTS (
        SELECT 1
        FROM public.sys_tenant
        WHERE id = p_id
          AND deleted_at IS NULL
          AND (tenant_id = v_tenant OR id = v_tenant)
    ) THEN
        RAISE EXCEPTION 'tenant % not found or not visible to caller', p_id;
    END IF;

    SELECT count(*)
    INTO v_count
    FROM public.sys_tenant
    WHERE parent_id = p_id
      AND deleted_at IS NULL;
    RETURN v_count;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_tenant_active_child_count(varchar) IS '租户删除前置完整性判定（受控读取面）：返回指定租户名下未删除直接子租户数。SECURITY DEFINER 以属主（部署超级用户）执行绕过 sys_tenant 的 ENABLE+FORCE 租户 RLS——租户树策略下祖父看不到子租户自己创建的孙行，删除前置校验需要全局事实；函数只返回计数、不返回行数据，不构成跨租户数据读面与跨租户写路径。函数内校验 app.tenant_id 已设置（未设置即拒绝，fail-closed）且目标对调用方可见（id 或 tenant_id 命中调用方租户，否则拒绝），消除跨租户结构侧信道；EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';
