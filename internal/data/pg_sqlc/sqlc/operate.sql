-- ============================================================
-- operate.sql — 写数据前的权限判断函数
--
-- 在 schema/ 全部文件、rls.sql 与 expose.sql 之后，seed 包之前执行
--（fn_can_* 的函数体引用 rls.sql 的 fn_accessible_* 与 expose.sql 的
-- fn_has_permission，三文件依赖顺序 rls → expose → operate）。
-- 本文件同时列在 sqlc.yaml 的 schema 里：queries/ 下的命名查询要
-- 调用这些函数，sqlc 需要读到函数签名（sqlc 只看签名，不管函数体）。
--
-- 解决什么问题：
--   查询列表时 @scope 会自动过滤（守卫 fn_visible_role_ids 见下文
--   角色判定族）；但写入是单条语句，
--   没有自动过滤的时机，所以写入前要显式判断一次。约定：先判断、
--   再写入，两步放在同一个事务里，判断不通过就直接报错回滚，
--   不产生任何写入。
--
-- 角色「角色分发映射规则」判定族（读侧 fn_can_see_role 与写侧
-- fn_can_assign_role / fn_can_revoke_role 共用同一套 A AND G 口径，
-- 动态列表 @scope 守卫 fn_visible_role_ids 同源）：
--   fn_role_visibility_core 角色可见性核心（A AND G，只判口径本身）
--   fn_can_see_role         操作人能否看见某个角色（角色读面共用）
--   fn_visible_role_ids     能看到哪些角色（sys_role @scope 守卫，逐角色
--                           调 fn_can_see_role 过滤）
--   fn_can_assign_role      能否把某个角色分配给某个用户
--   fn_can_assign_roles     上面第一条的批量版
--   fn_can_revoke_role      能否撤销某条用户-角色映射（放宽状态校验）
-- 数据归属判定族：
--   fn_can_operate_row      能否修改/删除某条数据（按数据的归属判断）
--   fn_can_set_owner        新归属三元组是否存在、启用且在可见范围内
--   fn_can_transfer_owner   能否把某条数据的归属改给别人
--
-- 注释约定：每个函数按「参数 → 判断逻辑 → 返回值」三段写；
-- 修改函数时，同步更新这段注释和 ../README.md（SQL 对象索引）。
-- ============================================================


-- ------------------------------------------------------------
-- fn_role_visibility_core：角色可见性核心判定（fn_can_see_role /
-- fn_can_revoke_role 共用；不校验角色行状态——调用方自行加软删/启用
-- 过滤）。判定使用两个记号（含义见 fn_can_see_role 注释）：
--   M：目标角色 sys_role_allowed_dept 有效映射部门集合（映射行与映射
--      部门均启用未删除）；
--   S：目标角色 scope_dept_id；O_u/O_d/O_g：操作人 fn_accessible_*
--      三态集合（NULL=全租户、{}=无可见对象）。
--
-- 判断逻辑（A AND G 同时满足）：
--   A（分发映射命中）：M 为空恒真；M 非空时 O_d 为 NULL 或
--      M ∩ O_d ≠ ∅。命中口径是「操作人可见部门集合」而非「操作人
--      所属部门」，且 M 命中不能替代 G。
--   G（组织可见范围 + 数据范围档位）：
--      S 为 NULL → O_d 为 NULL（仅全租户操作人可见）；
--      S 非空 → O_d 为 NULL（全租户操作人恒可见，与 fn_visible_role_ids
--        的 data_scope=7 恒见全部口径一致），否则 S ∈ O_d 且目标角色
--        data_scope D 满足：
--        D=1/2：操作人存在有效角色 D≠1；
--        D=3：操作人存在有效角色 D∉{1,2}；
--        D=4/5/6：该角色 sys_role_id_scope 对应类型明细（用户/部门/
--          用户组）全部 ⊆ O_u/O_d/O_g（O_* 为 NULL 表示全租户，恒过）；
--        D=7：O_d 为 NULL。
--   操作人无效（不存在/禁用/删除）时 O_* 恒为 {}，G 的两个分支都不
--   成立，恒 false（无需单独判操作人）。
--
-- 返回：true = 通过可见性核心判定。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_role_visibility_core(p_operator_id int8, p_role_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
WITH role AS (
    SELECT r.id, r.data_scope, r.scope_dept_id
    FROM public.sys_role r
    WHERE r.id = p_role_id
),
operator_roles AS (
    SELECT r.data_scope
    FROM public.sys_role r
             JOIN public.sys_user_role_mapping m ON m.role_id = r.id
    WHERE m.user_id = p_operator_id
      AND m.deleted_at IS NULL
      AND r.status = 1
      AND r.deleted_at IS NULL
),
allowed AS (
    SELECT a.dept_id
    FROM public.sys_role_allowed_dept a
             JOIN public.sys_dept d ON d.id = a.dept_id
        AND d.status = 1
        AND d.deleted_at IS NULL
    WHERE a.role_id = p_role_id
      AND a.status = 1
      AND a.deleted_at IS NULL
)
SELECT EXISTS (SELECT 1 FROM role)
   AND (
        -- A：分发映射命中口径是「操作人可见部门集合」，命中不能替代 G
        NOT EXISTS (SELECT 1 FROM allowed)
        OR public.fn_accessible_dept_ids(p_operator_id) IS NULL
        OR EXISTS (SELECT 1
                   FROM allowed
                   WHERE dept_id = ANY (public.fn_accessible_dept_ids(p_operator_id)))
   )
   AND (
        CASE
            -- G：组织可见范围 NULL = 仅全租户操作人（O_d 为 NULL）可见
            WHEN (SELECT scope_dept_id FROM role) IS NULL THEN
                public.fn_accessible_dept_ids(p_operator_id) IS NULL
            ELSE
                -- O_d 为 NULL = 全租户操作人：组织可见范围恒成立（与
                -- fn_visible_role_ids 的 data_scope=7 恒见全部口径一致）
                public.fn_accessible_dept_ids(p_operator_id) IS NULL
                OR (
                    (SELECT scope_dept_id FROM role) = ANY (public.fn_accessible_dept_ids(p_operator_id))
                    AND CASE (SELECT data_scope FROM role)
                        -- D=1/2：操作人存在有效角色 D≠1
                        WHEN 1 THEN EXISTS (SELECT 1 FROM operator_roles WHERE data_scope <> 1)
                        WHEN 2 THEN EXISTS (SELECT 1 FROM operator_roles WHERE data_scope <> 1)
                        -- D=3：操作人存在有效角色 D∉{1,2}
                        WHEN 3 THEN EXISTS (SELECT 1 FROM operator_roles WHERE data_scope NOT IN (1, 2))
                        -- D=4/5/6：目标角色 ID 范围明细全部落在操作人可见集合内
                        WHEN 4 THEN (
                            public.fn_accessible_user_ids(p_operator_id) IS NULL
                            OR NOT EXISTS (
                                SELECT 1
                                FROM public.sys_role_id_scope s
                                WHERE s.role_id = p_role_id
                                  AND s."type" = 1
                                  AND s.status = 1
                                  AND s.deleted_at IS NULL
                                  AND NOT (s.data_id = ANY (public.fn_accessible_user_ids(p_operator_id)))
                            )
                        )
                        WHEN 5 THEN (
                            public.fn_accessible_dept_ids(p_operator_id) IS NULL
                            OR NOT EXISTS (
                                SELECT 1
                                FROM public.sys_role_id_scope s
                                WHERE s.role_id = p_role_id
                                  AND s."type" = 2
                                  AND s.status = 1
                                  AND s.deleted_at IS NULL
                                  AND NOT (s.data_id = ANY (public.fn_accessible_dept_ids(p_operator_id)))
                            )
                        )
                        WHEN 6 THEN (
                            public.fn_accessible_group_ids(p_operator_id) IS NULL
                            OR NOT EXISTS (
                                SELECT 1
                                FROM public.sys_role_id_scope s
                                WHERE s.role_id = p_role_id
                                  AND s."type" = 3
                                  AND s.status = 1
                                  AND s.deleted_at IS NULL
                                  AND NOT (s.data_id = ANY (public.fn_accessible_group_ids(p_operator_id)))
                            )
                        )
                        -- D=7：仅全租户操作人
                        ELSE public.fn_accessible_dept_ids(p_operator_id) IS NULL
                    END
                )
        END
   )
$$;

COMMENT ON FUNCTION public.fn_role_visibility_core(int8, int8) IS '内部核心：角色可见性 A AND G 判定（fn_can_see_role / fn_can_revoke_role 共用）。A=分发映射命中操作人可见部门集合（M 为空恒真，命中不替代 G）；G=组织可见范围（S 为 NULL 仅全租户操作人；S 非空时全租户操作人恒可见，受限操作人须 S∈O_d 且目标角色 data_scope 档位满足对应条件）。不校验角色行状态，由调用方按读/写面自行过滤。';


-- ------------------------------------------------------------
-- fn_can_see_role：操作人能否看见某个角色（角色读面共用判定）
--
-- 参数：
--   p_operator_id  操作人
--   p_role_id      目标角色
--
-- 判断逻辑（同时满足才返回 true）：
--   1. 操作人存在且有效（启用、未删除）；
--   2. 目标角色存在且未软删（不按启用状态过滤：角色管理面需要看到
--      停用角色；候选接口自行叠加 status=1）；
--   3. fn_role_visibility_core（A AND G）通过。
--
-- 返回：true = 可见；false = 不可见（对读面等同不存在，不泄露存在性）。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_see_role(p_operator_id int8, p_role_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
SELECT EXISTS (SELECT 1
               FROM public.sys_user u
               WHERE u.id = p_operator_id
                 AND u.status = 1
                 AND u.deleted_at IS NULL)
   AND EXISTS (SELECT 1
               FROM public.sys_role r
               WHERE r.id = p_role_id
                 AND r.deleted_at IS NULL)
   AND public.fn_role_visibility_core(p_operator_id, p_role_id)
$$;

COMMENT ON FUNCTION public.fn_can_see_role(int8, int8) IS '读前判断：操作人能否看见某个角色（ListRoles/GetRole/GetRoleDataScope/ListRoleMenus 共用，与动态列表 fn_visible_role_ids 同源）。角色未软删且 A AND G 通过即可见，不按启用状态过滤；操作人无效恒 false。';


-- ------------------------------------------------------------
-- fn_visible_role_ids：当前用户能看到哪些角色（sys_role 动态列表
-- @scope 守卫）
--
-- 角色可见性口径与 fn_can_see_role（本文件上文，A AND G）共用同一判定：
-- 每个未软删角色逐个调用 fn_can_see_role 过滤。三态返回：
--   NULL   操作人持有全租户角色（data_scope=7）：对该口径恒见全部角色，
--          不过滤（NULL 只允许在这个分支返回）
--   '{}'   操作人无效或没有可见角色
--   数组   可见角色 ID 列表
--
-- 归属本文件（角色判定族）而非 rls.sql：LANGUAGE sql 的标准函数体在
-- 创建期即解析校验引用，本函数依赖 fn_can_see_role，定义必须先于引用
-- 所在文件部署（部署顺序 rls.sql → expose.sql → operate.sql，sqlc 只读
-- 签名）。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_visible_role_ids(p_user_id int8)
    RETURNS int8[]
    STABLE
    LANGUAGE sql
AS $$
WITH me AS (
    SELECT u.id
    FROM public.sys_user u
    WHERE u.id = p_user_id
      AND u.status = 1
      AND u.deleted_at IS NULL
),
my_roles AS (
    SELECT r.data_scope
    FROM public.sys_role r
             JOIN public.sys_user_role_mapping m ON m.role_id = r.id
    WHERE m.user_id = p_user_id
      AND m.deleted_at IS NULL
      AND r.status = 1
      AND r.deleted_at IS NULL
)
SELECT CASE
           -- 操作人无效：什么都看不到
           WHEN NOT EXISTS (SELECT 1 FROM me) THEN '{}'::int8[]
           -- 全租户操作人对角色口径恒见全部（A/G 各分支全部满足）
           WHEN EXISTS (SELECT 1 FROM my_roles WHERE data_scope = 7) THEN NULL::int8[]
           -- 逐角色判定取并集；没有可见角色时为空数组
           ELSE COALESCE((SELECT array_agg(DISTINCT r.id)
                          FROM public.sys_role r
                          WHERE r.deleted_at IS NULL
                            AND public.fn_can_see_role(p_user_id, r.id)), '{}'::int8[])
       END
$$;

COMMENT ON FUNCTION public.fn_visible_role_ids(int8) IS '权限过滤：返回当前用户能看到哪些角色（sys_role 动态列表 @scope 守卫；与 fn_can_see_role 共用同一 A AND G 判定）。NULL=有全租户角色，不过滤；{}=操作人无效或没有可见角色；数组=可见角色 ID 列表（未软删角色，不按启用状态过滤——角色管理面需要看到停用角色）。需要会话已设置 app.tenant_id。';


-- ------------------------------------------------------------
-- fn_can_assign_role：当前用户能否把某个角色分配给另一个用户
--
-- 参数：
--   p_operator_id     发起分配的用户
--   p_role_id         要分配的角色
--   p_target_user_id  被分配的用户
--
-- 判断逻辑（同时满足才返回 true）：
--   1. 操作人、角色、目标用户都存在且有效（启用、未删除）。
--      RLS 已把查询限制在当前租户内，跨租户的对象等同于不存在。
--   2. 角色对操作人可见：fn_can_see_role（A AND G）。
--   3. 操作人持有分配权限码 sys:user:assign-role（功能码是分配面的
--      功能权限，可见性判定是数据层，两者不互替）。
--   4. 目标部门同时满足全部配置范围（取交集）：
--      a. 角色在 sys_role_allowed_dept 配了映射（M 非空）：目标部门
--         必须在映射集合内；映射部门与映射行均须有效；
--      b. 角色配了 scope_dept_id（S 非 NULL）：目标部门必须在 S 的
--         部门子树内（sys_dept_closure，含自身）；S 本身须有效；
--      c. 目标用户无部门（owner_dept_id 为 NULL）仅在「无 M 且
--         S 为 NULL」时可分配。
--      「可见」指角色可见性，不要求目标用户对操作人可见。
--
-- 返回：true = 允许分配，可以写入映射表；false = 拒绝，调用方报错
--       返回，不写入。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_assign_role(p_operator_id int8, p_role_id int8, p_target_user_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
WITH operator AS (
    SELECT u.id
    FROM public.sys_user u
    WHERE u.id = p_operator_id
      AND u.status = 1
      AND u.deleted_at IS NULL
),
role AS (
    SELECT r.id, r.scope_dept_id,
           -- S 非 NULL 时须指向有效部门：组织可见范围失效即不可分配
           EXISTS (SELECT 1
                   FROM public.sys_dept d
                   WHERE r.scope_dept_id IS NOT NULL
                     AND d.id = r.scope_dept_id
                     AND d.status = 1
                     AND d.deleted_at IS NULL) AS scope_ok
    FROM public.sys_role r
    WHERE r.id = p_role_id
      AND r.status = 1
      AND r.deleted_at IS NULL
),
target AS (
    SELECT u.id, u.owner_dept_id,
           EXISTS (SELECT 1
                   FROM public.sys_dept d
                   WHERE u.owner_dept_id IS NOT NULL
                     AND d.id = u.owner_dept_id
                     AND d.status = 1
                     AND d.deleted_at IS NULL) AS dept_ok
    FROM public.sys_user u
    WHERE u.id = p_target_user_id
      AND u.status = 1
      AND u.deleted_at IS NULL
),
allowed AS (
    SELECT a.dept_id
    FROM public.sys_role_allowed_dept a
             JOIN public.sys_dept d ON d.id = a.dept_id
        AND d.status = 1
        AND d.deleted_at IS NULL
    WHERE a.role_id = p_role_id
      AND a.status = 1
      AND a.deleted_at IS NULL
),
subtree AS (
    -- scope_dept_id 的部门子树（闭包表含自身）；S 失效时为空集
    SELECT c.descendant_id AS dept_id
    FROM role r
             JOIN public.sys_dept_closure c ON c.ancestor_id = r.scope_dept_id
    WHERE r.scope_dept_id IS NOT NULL
      AND r.scope_ok
)
SELECT EXISTS (SELECT 1 FROM operator)
   AND EXISTS (SELECT 1 FROM role)
   AND EXISTS (SELECT 1 FROM target)
   AND public.fn_can_see_role(p_operator_id, p_role_id)
   AND public.fn_has_permission(p_operator_id, 'sys:user:assign-role')
   AND EXISTS (
    SELECT 1
    FROM target t
    WHERE CASE
              -- 无部门目标：仅「无 M 且 S NULL」可分配
              WHEN t.owner_dept_id IS NULL THEN
                      NOT EXISTS (SELECT 1 FROM allowed)
                      AND (SELECT scope_dept_id FROM role) IS NULL
              -- 有部门目标：须有效，且同时落入 M（若配置）与 subtree(S)（若配置）
              ELSE t.dept_ok
                      AND (NOT EXISTS (SELECT 1 FROM allowed)
                           OR t.owner_dept_id IN (SELECT dept_id FROM allowed))
                      AND ((SELECT scope_dept_id FROM role) IS NULL
                           OR t.owner_dept_id IN (SELECT dept_id FROM subtree))
          END
)
$$;

COMMENT ON FUNCTION public.fn_can_assign_role(int8, int8, int8) IS '写入前判断：operator 能否把 role 分配给 target。前置：操作人/角色/目标有效、fn_can_see_role（A AND G）可见、持有 sys:user:assign-role。目标部门同时满足 sys_role_allowed_dept 映射与 scope_dept_id 子树（均配置时取交集）；无部门目标仅「无映射且无组织范围」可分配；不要求目标用户对操作人可见。';


-- ------------------------------------------------------------
-- fn_can_assign_roles：批量版分配判断
-- 一次判断"把多个角色配给同一个用户"，逐个角色给出结论。
--
-- 参数：
--   p_operator_id     发起分配的用户
--   p_role_ids        要分配的角色列表（int8 数组）
--   p_target_user_id  被分配的用户
--
-- 判断逻辑：对列表里的每个角色独立调用 fn_can_assign_role，
-- 规则完全相同，角色之间互不影响。
--
-- 返回：每个角色一行（role_id, ok）。全部 ok=true 才允许批量写入；
-- 有任一 false 时调用方应整体报错回滚（不写入任何一个）。
-- 空数组返回空结果（没有要判断的角色）。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_assign_roles(p_operator_id int8, p_role_ids int8[], p_target_user_id int8)
    RETURNS TABLE(role_id int8, ok boolean)
    STABLE
    LANGUAGE sql
AS $$
SELECT r AS role_id,
       public.fn_can_assign_role(p_operator_id, r, p_target_user_id) AS ok
FROM unnest(p_role_ids) AS r
$$;

COMMENT ON FUNCTION public.fn_can_assign_roles(int8, int8[], int8) IS '写入前判断（批量）：逐个角色调用 fn_can_assign_role，返回每个角色的 (role_id, ok)。全部通过才批量写入；任一不通过整体回滚。';


-- ------------------------------------------------------------
-- fn_can_revoke_role：操作人能否撤销某条用户-角色映射
--
-- 撤销沿用可见性 + 目标范围判定，但放宽角色/目标状态校验：禁用/软删
-- 的角色、禁用/软删的目标用户也可以撤销——解决「角色停用或目标禁用
-- 后映射撤不掉」的问题。
--
-- 参数：
--   p_operator_id     发起撤销的用户
--   p_role_id         被撤销映射的角色
--   p_target_user_id  被撤销映射的用户
--
-- 判断逻辑（同时满足才返回 true）：
--   1. 操作人有效；角色与目标行存在（不校验状态/软删，RLS 限本租户）；
--   2. fn_role_visibility_core（A AND G）通过——沿用可见性口径；
--   3. 操作人持有分配权限码 sys:user:assign-role；
--   4. 目标部门约束与分配同口径（映射 M 与子树 subtree(S) 全部满足；
--      无部门目标仅「无 M 且 S NULL」可撤）。
--
-- 返回：true = 允许撤销（映射软删）；false = 拒绝。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_revoke_role(p_operator_id int8, p_role_id int8, p_target_user_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
WITH operator AS (
    SELECT u.id
    FROM public.sys_user u
    WHERE u.id = p_operator_id
      AND u.status = 1
      AND u.deleted_at IS NULL
),
role AS (
    SELECT r.id, r.scope_dept_id,
           EXISTS (SELECT 1
                   FROM public.sys_dept d
                   WHERE r.scope_dept_id IS NOT NULL
                     AND d.id = r.scope_dept_id
                     AND d.status = 1
                     AND d.deleted_at IS NULL) AS scope_ok
    FROM public.sys_role r
    WHERE r.id = p_role_id
),
target AS (
    SELECT u.id, u.owner_dept_id,
           EXISTS (SELECT 1
                   FROM public.sys_dept d
                   WHERE u.owner_dept_id IS NOT NULL
                     AND d.id = u.owner_dept_id
                     AND d.status = 1
                     AND d.deleted_at IS NULL) AS dept_ok
    FROM public.sys_user u
    WHERE u.id = p_target_user_id
),
allowed AS (
    SELECT a.dept_id
    FROM public.sys_role_allowed_dept a
             JOIN public.sys_dept d ON d.id = a.dept_id
        AND d.status = 1
        AND d.deleted_at IS NULL
    WHERE a.role_id = p_role_id
      AND a.status = 1
      AND a.deleted_at IS NULL
),
subtree AS (
    SELECT c.descendant_id AS dept_id
    FROM role r
             JOIN public.sys_dept_closure c ON c.ancestor_id = r.scope_dept_id
    WHERE r.scope_dept_id IS NOT NULL
      AND r.scope_ok
)
SELECT EXISTS (SELECT 1 FROM operator)
   AND EXISTS (SELECT 1 FROM role)
   AND EXISTS (SELECT 1 FROM target)
   AND public.fn_role_visibility_core(p_operator_id, p_role_id)
   AND public.fn_has_permission(p_operator_id, 'sys:user:assign-role')
   AND EXISTS (
    SELECT 1
    FROM target t
    WHERE CASE
              WHEN t.owner_dept_id IS NULL THEN
                      NOT EXISTS (SELECT 1 FROM allowed)
                      AND (SELECT scope_dept_id FROM role) IS NULL
              ELSE t.dept_ok
                      AND (NOT EXISTS (SELECT 1 FROM allowed)
                           OR t.owner_dept_id IN (SELECT dept_id FROM allowed))
                      AND ((SELECT scope_dept_id FROM role) IS NULL
                           OR t.owner_dept_id IN (SELECT dept_id FROM subtree))
          END
)
$$;

COMMENT ON FUNCTION public.fn_can_revoke_role(int8, int8, int8) IS '撤销前判断：operator 能否撤销 target 的 role 映射。沿用可见性（fn_role_visibility_core，A AND G）与目标范围约束并持有 sys:user:assign-role；放宽角色/目标状态校验——禁用/软删角色、禁用/软删用户也可撤销。';


-- ------------------------------------------------------------
-- fn_can_operate_row：当前用户能否修改/删除某条数据
--
-- 判断标准很简单：能看见这条数据，就能操作它（读写同权）。
-- "能看见"指数据的归属字段命中该用户的可见范围，和动态查询
-- 列表时的过滤规则一致。
--
-- 参数：
--   p_user_id              当前用户
--   p_owner_user_id        这条数据的归属人（NULL = 没有归属人）
--   p_owner_dept_id        这条数据的归属部门（NULL = 没有归属部门）
--   p_owner_user_group_id  这条数据的归属用户组（NULL = 没有归属组）
--   后三个参数传这条数据当前的值：调用方先 SELECT ... FOR UPDATE
--   锁行取值再传入，避免并发修改归属后按旧值判断。
--
-- 判断逻辑（满足其一即 true）：
--   1. 当前用户有全租户角色（data_scope=7），直接通过；
--   2. 某个归属字段非空，且命中对应的可见范围：
--      归属人在可见用户列表内 / 归属部门在可见部门列表内 /
--      归属组在可见用户组列表内。
--   三个归属字段全为 NULL 的数据只有全租户角色能操作；
--   当前用户无效（不存在/禁用/删除）时永远不通过。
--
-- 返回：true = 允许操作；false = 拒绝。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_operate_row(p_user_id int8, p_owner_user_id int8, p_owner_dept_id int8, p_owner_user_group_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
SELECT public.fn_accessible_user_ids(p_user_id) IS NULL
    OR public.fn_accessible_dept_ids(p_user_id) IS NULL
    OR public.fn_accessible_group_ids(p_user_id) IS NULL
    OR (p_owner_user_id IS NOT NULL AND p_owner_user_id = ANY (public.fn_accessible_user_ids(p_user_id)))
    OR (p_owner_dept_id IS NOT NULL AND p_owner_dept_id = ANY (public.fn_accessible_dept_ids(p_user_id)))
    OR (p_owner_user_group_id IS NOT NULL AND p_owner_user_group_id = ANY (public.fn_accessible_group_ids(p_user_id)))
$$;

COMMENT ON FUNCTION public.fn_can_operate_row(int8, int8, int8, int8) IS '写入前判断：当前用户能否操作一条数据（读写同权：能看见就能操作）。传入该数据当前的归属人/归属部门/归属用户组（NULL=无该归属）。有全租户角色直接通过；否则任一非空归属命中可见范围即通过；三个归属全空的数据只有全租户能操作；用户无效永远不通过。';


-- ------------------------------------------------------------
-- fn_can_set_owner：当前用户能否把一条新数据（或归属变更）挂上给定的
-- 归属三元组
--
-- 只校验新归属：每个非空归属对象必须存在、启用且在当前用户的可见
-- 范围内；NULL 表示没有该归属，直接通过。创建路径（CreateUser、
-- 部门/用户组创建）与 fn_can_transfer_owner 的新归属段共用本判定，
-- 三个维度同口径（用户/部门/用户组都校验存在与启用）。
--
-- 参数：
--   p_user_id              当前用户
--   p_owner_user_id        归属人（NULL = 无）
--   p_owner_dept_id        归属部门（NULL = 无）
--   p_owner_user_group_id  归属用户组（NULL = 无）
--
-- 判断逻辑（每个非空归属维度同时满足）：
--   1. 归属对象在当前租户内存在、启用且未删除（跨租户等同不存在）；
--   2. 命中操作人对应维度的可见集合（fn_accessible_* 为 NULL 表示
--      全租户角色，恒过；'{}' 表示该维度什么都不可达）。
--   当前用户无效时可见集合恒为 '{}'，非空归属永远不通过。
--
-- 返回：true = 允许写入该归属三元组；false = 拒绝。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_set_owner(p_user_id int8, p_owner_user_id int8, p_owner_dept_id int8, p_owner_user_group_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
SELECT (p_owner_user_id IS NULL
        OR (EXISTS (SELECT 1
                    FROM public.sys_user u
                    WHERE u.id = p_owner_user_id
                      AND u.status = 1
                      AND u.deleted_at IS NULL)
             AND (public.fn_accessible_user_ids(p_user_id) IS NULL
                  OR p_owner_user_id = ANY (public.fn_accessible_user_ids(p_user_id)))))
   AND (p_owner_dept_id IS NULL
        OR (EXISTS (SELECT 1
                    FROM public.sys_dept d
                    WHERE d.id = p_owner_dept_id
                      AND d.status = 1
                      AND d.deleted_at IS NULL)
             AND (public.fn_accessible_dept_ids(p_user_id) IS NULL
                  OR p_owner_dept_id = ANY (public.fn_accessible_dept_ids(p_user_id)))))
   AND (p_owner_user_group_id IS NULL
        OR (EXISTS (SELECT 1
                    FROM public.sys_user_group g
                    WHERE g.id = p_owner_user_group_id
                      AND g.status = 1
                      AND g.deleted_at IS NULL)
             AND (public.fn_accessible_group_ids(p_user_id) IS NULL
                  OR p_owner_user_group_id = ANY (public.fn_accessible_group_ids(p_user_id)))))
$$;

COMMENT ON FUNCTION public.fn_can_set_owner(int8, int8, int8, int8) IS '写入前判断：给定归属三元组的每个非空归属是否可挂（存在、启用、未删除且在当前用户可见范围内；NULL=无该归属直接通过）。创建路径与 fn_can_transfer_owner 的新归属段共用，用户/部门/用户组三维度同口径。';


-- ------------------------------------------------------------
-- fn_can_transfer_owner：当前用户能否把某条数据的归属改给别人
--
-- 改归属要同时过两道检查：
--   1. 这条数据本身你有权操作（按它的旧归属判断）；
--   2. 新归属的人/部门/组存在、启用且在你的可见范围内
--     （fn_can_set_owner，与创建路径同口径）。
-- 第二道是防止把数据转给自己根本看不见的人，造成数据外流。
--
-- 参数：
--   p_user_id              当前用户
--   p_old_owner_user_id    这条数据现在的归属人（NULL = 没有）
--   p_old_owner_dept_id    这条数据现在的归属部门（NULL = 没有）
--   p_old_owner_group_id   这条数据现在的归属用户组（NULL = 没有）
--   p_new_owner_user_id    要改成的归属人（NULL = 改完后没有）
--   p_new_owner_dept_id    要改成的归属部门（NULL = 改完后没有）
--   p_new_owner_group_id   要改成的归属用户组（NULL = 改完后没有）
--
-- 判断逻辑（同时满足才返回 true）：
--   1. 旧归属通过 fn_can_operate_row 的判断；
--   2. 新归属通过 fn_can_set_owner 的判断。
--
-- 返回：true = 允许修改归属；false = 拒绝。
-- ------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.fn_can_transfer_owner(p_user_id int8,
                                                        p_old_owner_user_id int8,
                                                        p_old_owner_dept_id int8,
                                                        p_old_owner_group_id int8,
                                                        p_new_owner_user_id int8,
                                                        p_new_owner_dept_id int8,
                                                        p_new_owner_group_id int8)
    RETURNS boolean
    STABLE
    LANGUAGE sql
AS $$
SELECT public.fn_can_operate_row(p_user_id, p_old_owner_user_id, p_old_owner_dept_id, p_old_owner_group_id)
   AND public.fn_can_set_owner(p_user_id, p_new_owner_user_id, p_new_owner_dept_id, p_new_owner_group_id)
$$;

COMMENT ON FUNCTION public.fn_can_transfer_owner(int8, int8, int8, int8, int8, int8, int8) IS '写入前判断：能否把一条数据的归属从旧归属改为新归属。两道检查同时通过才允许：这条数据按旧归属判断你有权操作（fn_can_operate_row），且新归属的每个非空字段（人/部门/组）存在、启用且在可见范围内（fn_can_set_owner）。防止把数据转给自己看不见的人。';
