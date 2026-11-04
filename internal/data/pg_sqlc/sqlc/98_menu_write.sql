-- ============================================================
-- 98_menu_write.sql — sys_menu 受控写面（D2 结论）：菜单树 CRUD 的
-- SECURITY DEFINER 固定口径写函数。
--
-- 背景：sys_menu 是功能权限码链（角色 → 菜单 → 权限码）的宿主表，
-- role.sql 已对 grape_u/test_u REVOKE INSERT/UPDATE/DELETE（功能入口
-- 表对运行用户只读）；运行角色保持无直写权限，本文件不动该 REVOKE。
-- 菜单管理（api super 方法）经本文件的受控函数写入——SECURITY DEFINER
-- 以函数属主（部署超级用户）身份执行，绕过 sys_menu 的 ENABLE+FORCE
-- 租户 RLS 与直写禁令，是 sys_menu 唯一的写路径。
--
-- 固定口径（收在函数内，调用方只传业务字段）：
--   1. 租户边界复位：DEFINER 绕过 RLS 是机械事实，函数内一律以
--      current_setting('app.tenant_id') 显式过滤与落值；未设置即拒绝
--      （fail-closed）。调用方（IdentityTx 事务）仍每事务设
--      app.tenant_id——super 只豁免直写禁令，不豁免租户边界。
--   2. 父节点校验：父菜单必须同租户存在且未删除（父级停用不拦——
--      口径同 fn_maintain_dept_closure 的父级校验）；拒绝自身为父。
--   3. 防环：移动时沿新父级祖先链上溯，命中被移动菜单即拒绝（菜单
--      无闭包表，走 parent_id 链逐级上溯，迭代上限防御性兜底）。
--   4. 软删口径：删除为 deleted_at 置位；存在未删除直接子菜单时拒绝
--      （自叶向根逐层删，口径同部门）；角色-菜单映射行物理残留，
--      fn_has_permission 的活跃联结（status=1 且未删除）使其不产生
--      功能权限。permission_code / menu_type / parent_id 是身份/类型/
--      结构列：创建后不变，parent_id 变更走 fn_menu_move。
--   5. 审计与派生：created_by/updated_by 记操作者；updated_at 由
--      trg_touch_updated_at 兜底刷新；写路径经 trg_bump_perm_version
--      _tenant 触发器自动 bump 租户级权限版本。
--
-- EXECUTE 授权照 fn_cleanup_expired_sessions 先例：沿用 PostgreSQL
-- 默认授权 PUBLIC（无显式 GRANT）。任何角色都能调用，但只能以函数内
-- 固定口径在自身租户内写入，不构成跨租户扩权面。SET search_path =
-- public 是 SECURITY DEFINER 的安全惯例（防 search_path 劫持函数体内
-- 的对象解析）。
--
-- 部署：尾部独立追加（DeployFiles 清单末位），只依赖 schema/40_rbac.sql
-- 的 sys_menu 存在；入 sqlc catalog（函数签名），命名查询见
-- queries/menu.sql。
-- ============================================================

-- fn_menu_create：新建菜单。租户取会话 app.tenant_id；父菜单同租户
-- 存在且未删除才可挂载（NULL 为根）；permission_code 在租户内未删除
-- 菜单间唯一（部分唯一索引兜底，冲突以唯一约束异常上抛）。返回新建行。
CREATE OR REPLACE FUNCTION public.fn_menu_create(
    p_id              int8,
    p_status          int2,
    p_name            varchar,
    p_permission_code varchar,
    p_parent_id       int8,
    p_menu_type       int2,
    p_icon            varchar,
    p_route_path      varchar,
    p_component       varchar,
    p_permission_expr varchar,
    p_sort_order      int4,
    p_remark          varchar,
    p_operator        int8
)
    RETURNS public.sys_menu
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_menu;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'menu write requires app.tenant_id';
    END IF;

    IF p_parent_id = p_id THEN
        RAISE EXCEPTION 'menu % cannot be its own parent', p_id;
    END IF;
    IF p_parent_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM public.sys_menu
        WHERE id = p_parent_id
          AND tenant_id = v_tenant
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'parent menu % not found, deleted or cross-tenant', p_parent_id;
    END IF;

    INSERT INTO public.sys_menu (id, status, "name", permission_code, parent_id, menu_type,
                                 icon, route_path, component, permission_expr, sort_order,
                                 remark, created_by, updated_by, tenant_id)
    VALUES (p_id, p_status, p_name, p_permission_code, p_parent_id, p_menu_type,
            p_icon, p_route_path, p_component, p_permission_expr, p_sort_order,
            p_remark, p_operator, p_operator, v_tenant)
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_menu_create(int8, int2, varchar, varchar, int8, int2, varchar, varchar, varchar, varchar, int4, varchar, int8) IS '菜单受控写面（D2）：新建菜单。SECURITY DEFINER 以属主（部署超级用户）执行绕过 sys_menu 的 ENABLE+FORCE 租户 RLS 与 role.sql 的直写 REVOKE——函数内以 app.tenant_id 复位租户边界（未设置即拒绝，fail-closed），grape_u 无直写权限、只能经本函数以固定口径写入。父菜单须同租户存在且未删除（父级停用不拦，口径同 fn_maintain_dept_closure），拒绝自身为父；permission_code 租户内未删除菜单间唯一由部分唯一索引兜底。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public 防 search_path 劫持';

-- fn_menu_update：覆盖可变字段（status/name/icon/route_path/component/
-- permission_expr/sort_order/remark）。permission_code / menu_type /
-- parent_id 是身份/类型/结构列不经本路径变更（parent_id 走
-- fn_menu_move）；目标菜单须同租户存在且未删除，未命中返回 NULL
-- （调用方按无行映射 NotFound）。updated_at 由触发器兜底刷新。
CREATE OR REPLACE FUNCTION public.fn_menu_update(
    p_id              int8,
    p_status          int2,
    p_name            varchar,
    p_icon            varchar,
    p_route_path      varchar,
    p_component       varchar,
    p_permission_expr varchar,
    p_sort_order      int4,
    p_remark          varchar,
    p_operator        int8
)
    RETURNS public.sys_menu
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_menu;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'menu write requires app.tenant_id';
    END IF;

    UPDATE public.sys_menu
    SET status          = p_status,
        "name"          = p_name,
        icon            = p_icon,
        route_path      = p_route_path,
        component       = p_component,
        permission_expr = p_permission_expr,
        sort_order      = p_sort_order,
        remark          = p_remark,
        updated_by      = p_operator
    WHERE id = p_id
      AND tenant_id = v_tenant
      AND deleted_at IS NULL
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_menu_update(int8, int2, varchar, varchar, varchar, varchar, varchar, int4, varchar, int8) IS '菜单受控写面（D2）：覆盖可变字段（status/name/icon/route_path/component/permission_expr/sort_order/remark）。SECURITY DEFINER 绕过租户 RLS 与直写 REVOKE，函数内以 app.tenant_id 复位租户边界；permission_code/menu_type/parent_id 是身份/类型/结构列不经本路径变更；目标未命中返回 NULL（调用方按无行映射 NotFound）。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';

-- fn_menu_move：移动菜单到新父级。p_parent_id 为 NULL 表示移为根；
-- 新父级须同租户存在且未删除，沿新父级祖先链上溯命中被移动菜单即
-- 拒绝（防环，迭代上限为防御性兜底）；目标未命中返回 NULL（调用方
-- 按无行映射 NotFound）。
CREATE OR REPLACE FUNCTION public.fn_menu_move(
    p_id        int8,
    p_parent_id int8,
    p_operator  int8
)
    RETURNS public.sys_menu
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public
AS $fn$
DECLARE
    v_tenant varchar(16);
    v_row    public.sys_menu;
    v_cur    int8;
    v_steps  int4 := 0;
BEGIN
    v_tenant := current_setting('app.tenant_id', true);
    IF v_tenant IS NULL OR v_tenant = '' THEN
        RAISE EXCEPTION 'menu write requires app.tenant_id';
    END IF;

    IF p_parent_id = p_id THEN
        RAISE EXCEPTION 'menu % cannot be moved under itself', p_id;
    END IF;
    IF p_parent_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM public.sys_menu
        WHERE id = p_parent_id
          AND tenant_id = v_tenant
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'parent menu % not found, deleted or cross-tenant', p_parent_id;
    END IF;

    -- 防环：新父级不得位于被移动菜单的子树内。菜单无闭包表，沿
    -- parent_id 链从新父级逐级上溯，命中 p_id 即成环；迭代上限防
    -- 既有数据损坏（环）导致的死循环。
    v_cur := p_parent_id;
    WHILE v_cur IS NOT NULL LOOP
        IF v_cur = p_id THEN
            RAISE EXCEPTION 'menu % cannot be moved into its own subtree', p_id;
        END IF;
        SELECT m.parent_id
        INTO v_cur
        FROM public.sys_menu m
        WHERE m.id = v_cur
          AND m.tenant_id = v_tenant
          AND m.deleted_at IS NULL;
        v_steps := v_steps + 1;
        IF v_steps > 10000 THEN
            RAISE EXCEPTION 'menu ancestry walk exceeded safety bound';
        END IF;
    END LOOP;

    UPDATE public.sys_menu
    SET parent_id   = p_parent_id,
        updated_by  = p_operator
    WHERE id = p_id
      AND tenant_id = v_tenant
      AND deleted_at IS NULL
    RETURNING * INTO v_row;
    RETURN v_row;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_menu_move(int8, int8, int8) IS '菜单受控写面（D2）：移动菜单（p_parent_id 为 NULL 表示移为根）。SECURITY DEFINER 绕过租户 RLS 与直写 REVOKE，函数内以 app.tenant_id 复位租户边界；新父级须同租户存在且未删除，沿 parent_id 祖先链上溯命中被移动菜单即拒绝（防环，迭代上限防数据损坏死循环）；目标未命中返回 NULL。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';

-- fn_menu_delete：软删除菜单（deleted_at 置位）。存在未删除直接子
-- 菜单时拒绝（自叶向根逐层删，口径同 fn_maintain_dept_closure）；
-- 角色-菜单映射行物理残留，fn_has_permission 的活跃联结使其不再产生
-- 功能权限。目标未命中（不存在/已删除/跨租户）返回 0，调用方按无行
-- 映射 NotFound；命中返回 1。
CREATE OR REPLACE FUNCTION public.fn_menu_delete(
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
        RAISE EXCEPTION 'menu write requires app.tenant_id';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.sys_menu
        WHERE parent_id = p_id
          AND tenant_id = v_tenant
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'menu % has active children', p_id;
    END IF;

    UPDATE public.sys_menu
    SET deleted_at = CURRENT_TIMESTAMP,
        updated_by = p_operator
    WHERE id = p_id
      AND tenant_id = v_tenant
      AND deleted_at IS NULL;
    GET DIAGNOSTICS v_deleted = ROW_COUNT;
    RETURN v_deleted;
END;
$fn$
;

COMMENT ON FUNCTION public.fn_menu_delete(int8, int8) IS '菜单受控写面（D2）：软删除菜单（deleted_at 置位）。SECURITY DEFINER 绕过租户 RLS 与直写 REVOKE，函数内以 app.tenant_id 复位租户边界；存在未删除直接子菜单时拒绝（自叶向根逐层删）；角色-菜单映射行物理残留，fn_has_permission 活跃联结使其不再产生功能权限；目标未命中返回 0（调用方映射 NotFound），命中返回 1。EXECUTE 沿用 PostgreSQL 默认授权 PUBLIC，SET search_path = public';
