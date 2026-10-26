-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 组织域：sys_dept 组织树 + sys_dept_closure 闭包表；触发器随部门
-- 增删改自动维护闭包路径（校验父级/防环/软删等价删除）。
CREATE TABLE public.sys_dept
(
    id                  int8                                                   NOT NULL,
    status              int2         DEFAULT 1                                 NOT NULL,
    "name"              varchar(256)                                           NOT NULL,
    parent_id           int8                                                   NULL,
    created_by          int8                                                   NOT NULL,
    created_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by          int8                                                   NOT NULL,
    updated_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id           varchar(16)                                            NOT NULL,
    owner_user_id       int8                                                   NOT NULL,
    owner_user_group_id int8                                                   NULL,
    CONSTRAINT sys_dept_pkey PRIMARY KEY (id),
    CONSTRAINT sys_dept_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_dept_tenant_name_un ON public.sys_dept USING btree (tenant_id, name) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_dept IS '部门表 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_dept.id IS '部门 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_dept.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_dept."name" IS '部门名 [@dynamic: fuzzy, exact @sortable @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_dept.parent_id IS '父级部门 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dept.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dept.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_dept.updated_by IS '更新人 [@dynamic: exact, in @updatable: set]';
COMMENT ON COLUMN public.sys_dept.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_dept.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_dept.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_dept.owner_user_id IS '部门管理员用户 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dept.owner_user_group_id IS '归属用户组 ID [@dynamic: exact, in]';

-- 闭包表维护触发器函数，语义约定：
--   1. 输入校验：拒绝自身为父、父级不存在/已删除/跨租户、移入自身子树（环）
--   2. 软删除（UPDATE deleted_at -> NOT NULL）等价于 DELETE 分支：移除闭包路径；
--      恢复（UPDATE deleted_at -> NULL）等价于 INSERT 分支：重建闭包路径
--   3. 删除（软删/物理删）时若存在未删除子部门则拒绝，需自叶向根逐层删除
--   4. DELETE 清理范围为 {X 的祖先 ∪ X} × {X 的子树}，保留子树内部关系
CREATE
    OR REPLACE
    FUNCTION public.fn_maintain_dept_closure()
    RETURNS trigger
    LANGUAGE plpgsql
AS
$function$
DECLARE
    v_old_parent_id INT8;
    v_new_parent_id INT8;
    v_is_soft_del   BOOLEAN;
    v_is_restore    BOOLEAN;
BEGIN
    v_old_parent_id := OLD.parent_id;
    v_new_parent_id := NEW.parent_id;
    v_is_soft_del := (TG_OP = 'UPDATE' AND OLD.deleted_at IS NULL AND NEW.deleted_at IS NOT NULL);
    v_is_restore := (TG_OP = 'UPDATE' AND OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS NULL);

    -- ================= 0. 输入校验 =================
    -- 新增 / 恢复 / 移动 时校验父级合法性与环
    IF (TG_OP = 'INSERT' OR v_is_restore
        OR (TG_OP = 'UPDATE' AND NOT v_is_soft_del)) THEN
        IF (v_new_parent_id = NEW.id) THEN
            RAISE EXCEPTION '部门 % 不能以自身为父级', NEW.id;
        END IF;
        IF (v_new_parent_id IS NOT NULL AND NOT EXISTS (SELECT 1
                                                        FROM sys_dept
                                                        WHERE id = NEW.parent_id
                                                          AND tenant_id = NEW.tenant_id
                                                          AND deleted_at IS NULL)) THEN
            RAISE EXCEPTION '父部门 % 不存在、已删除或跨租户', NEW.parent_id;
        END IF;
    END IF;

    -- 移动时检测环：目标父级不能位于当前节点的子树内
    IF (TG_OP = 'UPDATE' AND NOT v_is_soft_del AND v_new_parent_id IS NOT NULL
        AND v_new_parent_id IS DISTINCT FROM v_old_parent_id
        AND EXISTS (SELECT 1
                    FROM sys_dept_closure
                    WHERE ancestor_id = OLD.id
                      AND descendant_id = NEW.parent_id)) THEN
        RAISE EXCEPTION '不能将部门 % 移入其自身子树（目标父级: %）', OLD.id, NEW.parent_id;
    END IF;

    -- ================= 1. 新增 / 恢复部门 =================
    IF (TG_OP = 'INSERT' OR v_is_restore) THEN
        -- 插入自身到自身的路径 (深度0)
        INSERT INTO sys_dept_closure (ancestor_id, descendant_id, depth, tenant_id)
        VALUES (NEW.id, NEW.id, 0, NEW.tenant_id);

        -- 如果有父节点，复制父节点的所有祖先关系，并指向新节点（深度+1）
        IF NEW.parent_id IS NOT NULL THEN
            INSERT INTO sys_dept_closure (ancestor_id, descendant_id, depth, tenant_id)
            SELECT c.ancestor_id, NEW.id, c.depth + 1, NEW.tenant_id
            FROM sys_dept_closure c
            WHERE c.descendant_id = NEW.parent_id;
        END IF;

        RETURN NEW;
    END IF;

    -- ================= 2. 删除部门（物理删除 / 软删除） =================
    IF (TG_OP = 'DELETE' OR v_is_soft_del) THEN
        -- 存在未删除的子部门则拒绝删除，需先处理子部门（自叶向根）
        IF EXISTS (SELECT 1
                   FROM sys_dept
                   WHERE parent_id = OLD.id
                     AND deleted_at IS NULL) THEN
            RAISE EXCEPTION '部门 % 存在未删除的子部门，禁止删除，请先处理子部门', OLD.id;
        END IF;

        -- 正确的清理范围：{X 的祖先 ∪ X} × {X 的子树}
        -- 只删除经过 X 的路径，保留子树内部关系，后续移动可正常重建
        DELETE
        FROM sys_dept_closure
        WHERE descendant_id IN (SELECT descendant_id
                                FROM sys_dept_closure
                                WHERE ancestor_id = OLD.id)
          AND ancestor_id IN (SELECT ancestor_id
                              FROM sys_dept_closure
                              WHERE descendant_id = OLD.id);
        RETURN OLD;
    END IF;

    -- ================= 3. 移动部门 (修改 parent_id) =================
    IF (TG_OP = 'UPDATE' AND v_new_parent_id IS DISTINCT FROM v_old_parent_id) THEN
        -- 步骤 A: 断开旧关系
        -- 删除 "旧祖先 -> 该节点及其子节点" 的路径
        DELETE
        FROM sys_dept_closure
        WHERE descendant_id IN (SELECT descendant_id FROM sys_dept_closure WHERE ancestor_id = OLD.id) -- 后代
          AND ancestor_id IN
              (SELECT ancestor_id FROM sys_dept_closure WHERE descendant_id = OLD.id AND ancestor_id != OLD.id); -- 旧祖先(排除自身)

        -- 步骤 B: 建立新关系
        -- 插入 "新祖先 -> 该节点及其子节点" 的路径
        IF NEW.parent_id IS NOT NULL THEN
            INSERT INTO sys_dept_closure (ancestor_id, descendant_id, depth, tenant_id)
            SELECT a.ancestor_id, d.descendant_id, a.depth + d.depth + 1, NEW.tenant_id
            FROM sys_dept_closure a -- 新的祖先路径
                     CROSS JOIN sys_dept_closure d -- 当前节点及其子节点的路径
            WHERE a.descendant_id = NEW.parent_id -- 新祖先路径的终点是新父节点
              AND d.ancestor_id = NEW.id          -- 子路径的起点是当前节点
              AND a.ancestor_id != d.descendant_id; -- 避免产生环（自己不能是自己的祖先）
        END IF;

        RETURN NEW;
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$function$
;

COMMENT ON FUNCTION public.fn_maintain_dept_closure() IS '闭包表维护：sys_dept 增删改后自动维护 sys_dept_closure。输入校验拒绝自身为父/父级不存在或已删除/跨租户/移入自身子树（环）；软删（deleted_at 置非空）等价 DELETE、恢复等价 INSERT；存在未删除子部门时拒绝删除（自叶向根逐层删）；DELETE 清理 {祖先∪自身}×子树，保留子树内部关系';

-- Table Triggers
-- 监听 deleted_at：软删除等价 DELETE 分支、恢复等价 INSERT 分支（见函数头注释）

DROP TRIGGER IF EXISTS trg_maintain_dept_closure ON public.sys_dept;

create trigger trg_maintain_dept_closure
    after
        insert
        or
        delete
        or
        update
            of parent_id, deleted_at
    on
        public.sys_dept
    for each row
execute function fn_maintain_dept_closure();


-- public.sys_dept_closure 定义

CREATE TABLE public.sys_dept_closure
(
    ancestor_id   int8        NOT NULL,
    descendant_id int8        NOT NULL,
    "depth"       int4        NOT NULL,
    tenant_id     varchar(16) NOT NULL,
    CONSTRAINT sys_dept_closure_pkey PRIMARY KEY (ancestor_id, descendant_id)
);
CREATE INDEX idx_closure_ancestor ON public.sys_dept_closure USING btree (ancestor_id);
CREATE INDEX idx_closure_descendant ON public.sys_dept_closure USING btree (descendant_id);
CREATE INDEX idx_closure_tenant ON public.sys_dept_closure USING btree (tenant_id);
COMMENT ON TABLE public.sys_dept_closure IS '部门闭包表。派生数据：由 trg_maintain_dept_closure 触发器随 sys_dept 增删改自动维护，可随时从部门树重建，因此不设审计字段（created_by 等），审计请看 sys_dept 本表';

-- Column comments

COMMENT ON COLUMN public.sys_dept_closure.ancestor_id IS '祖先节点ID';
COMMENT ON COLUMN public.sys_dept_closure.descendant_id IS '后代节点ID';
COMMENT ON COLUMN public.sys_dept_closure."depth" IS '深度/距离（0表示自身，1表示直接子节点，2表示孙子节点...）';
COMMENT ON COLUMN public.sys_dept_closure.tenant_id IS '租户 ID';
