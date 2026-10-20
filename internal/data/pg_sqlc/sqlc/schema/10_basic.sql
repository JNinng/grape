-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- basic — 业务表范式示例：tenant_id + 审计列 + 归属三元组，表注释挂
-- @scope 守卫与分页/排序声明；业务表照此模式建表。
CREATE TABLE public.basic
(
    id                  int8                                                   NOT NULL,
    status              int2         DEFAULT 1                                 NOT NULL,
    created_by          int8                                                   NOT NULL,
    created_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by          int8                                                   NOT NULL,
    updated_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id           varchar(16)                                            NOT NULL,
    owner_user_id       int8                                                   NULL,
    owner_dept_id       int8                                                   NULL,
    owner_user_group_id int8                                                   NULL,
    CONSTRAINT sys_basic_pkey PRIMARY KEY (id),
    CONSTRAINT basic_status_chk CHECK (status IN (0, 1))
);
-- owner_* 归属索引：data_scope = 2/3/5/6 的行级过滤高频路径
CREATE INDEX idx_basic_owner_user ON public.basic USING btree (owner_user_id);
CREATE INDEX idx_basic_owner_dept ON public.basic USING btree (owner_dept_id);
CREATE INDEX idx_basic_owner_user_group ON public.basic USING btree (owner_user_group_id);
COMMENT ON TABLE public.basic IS '基础字段示例表 [@scope: owner_user_id in_any fn_accessible_user_ids(user_id:int) owner_dept_id in_any fn_accessible_dept_ids(user_id:int) owner_user_group_id in_any fn_accessible_group_ids(user_id:int) @default_sort: created_at DESC @pk: id @page: 20/400]';

-- Column comments

COMMENT ON COLUMN public.basic.id IS '主键 ID [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.basic.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.basic.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.basic.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @selectable]';
COMMENT ON COLUMN public.basic.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.basic.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.basic.deleted_at IS '删除时间 [@hidden]';
COMMENT ON COLUMN public.basic.tenant_id IS '租户 ID [@dynamic: exact @selectable]';
COMMENT ON COLUMN public.basic.owner_user_id IS '归属用户 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.basic.owner_dept_id IS '归属部门 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.basic.owner_user_group_id IS '归属用户组 ID [@dynamic: exact, in]';
