-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 授权域：sys_role（data_scope 1-7）、sys_role_id_scope（指定人/部门/
-- 用户组明细）、sys_role_allowed_dept（分发部门映射）、sys_menu +
-- sys_role_menu（功能权限码树，运行用户只读）。
CREATE TABLE public.sys_role
(
    id            int8                                                   NOT NULL,
    status        int2         DEFAULT 1                                 NOT NULL,
    "name"        varchar(256)                                           NOT NULL,
    code          varchar(64)                                            NOT NULL,
    role_type     int2         DEFAULT 2                                 NOT NULL,
    data_scope    int2         DEFAULT 1                                 NOT NULL,
    scope_dept_id int8                                                   NULL,
    remark        varchar(512)                                           NULL,
    created_by    int8                                                   NOT NULL,
    created_at    timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by    int8                                                   NOT NULL,
    updated_at    timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at    timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id     varchar(16)                                            NOT NULL,
    CONSTRAINT sys_role_pkey PRIMARY KEY (id),
    CONSTRAINT sys_role_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT sys_role_type_chk CHECK (role_type IN (1, 2)),
    CONSTRAINT sys_role_data_scope_chk CHECK (data_scope BETWEEN 1 AND 7)
);
CREATE UNIQUE INDEX sys_role_tenant_code_un ON public.sys_role USING btree (tenant_id, code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX sys_role_tenant_name_un ON public.sys_role USING btree (tenant_id, name) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_role IS '角色表 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_role.id IS '主键 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role."name" IS '角色名 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_role.code IS '权限编码 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_role.role_type IS '角色类型：1-系统预置(不可删除，核心权限不可剥离) 2-租户自定义 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role.data_scope IS '数据范围规则：1-仅本人 2-本部门 3-本部门及子部门 4-指定人 5-指定部门 6-用户组 7-全租户 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role.scope_dept_id IS '组织可见范围：限定角色可分配的部门层级。NULL=全租户可分配，具体部门ID=仅该部门及子部门可分配 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_role.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_role.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_role.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_role.tenant_id IS '租户 ID [@dynamic: exact @hideable]';


-- public.sys_role_id_scope 定义

CREATE TABLE public.sys_role_id_scope
(
    id         int8                                                   NOT NULL,
    status     int2         DEFAULT 1                                 NOT NULL,
    "type"     int2         DEFAULT 1                                 NOT NULL,
    role_id    int8                                                   NOT NULL,
    data_id    int8                                                   NOT NULL,
    created_by int8                                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by int8                                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id  varchar(16)                                            NOT NULL,
    CONSTRAINT sys_role_id_scope_pkey PRIMARY KEY (id),
    CONSTRAINT sys_role_id_scope_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT sys_role_id_scope_type_chk CHECK ("type" BETWEEN 1 AND 3)
);
CREATE UNIQUE INDEX sys_role_id_scope_tenant_role_type_data_un ON public.sys_role_id_scope USING btree (tenant_id, role_id, type, data_id) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_role_id_scope IS '角色数据 ID 范围表 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_role_id_scope.id IS '主键 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope."type" IS '数据 ID 类型，1：用户ID、2：部门ID、3：用户组ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope.role_id IS '角色 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope.data_id IS '数据 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role_id_scope.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_id_scope.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role_id_scope.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_role_id_scope.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_role_id_scope.tenant_id IS '租户 ID [@dynamic: exact @hideable]';


-- public.sys_role_allowed_dept 定义

CREATE TABLE public.sys_role_allowed_dept
(
    id         int8                                                   NOT NULL,
    status     int2         DEFAULT 1                                 NOT NULL,
    role_id    int8                                                   NOT NULL,
    dept_id    int8                                                   NOT NULL,
    created_by int8                                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by int8                                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id  varchar(16)                                            NOT NULL,
    CONSTRAINT sys_role_allowed_dept_pkey PRIMARY KEY (id),
    CONSTRAINT sys_role_allowed_dept_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_role_allowed_dept_tenant_role_dept_un ON public.sys_role_allowed_dept USING btree (tenant_id, role_id, dept_id) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_role_allowed_dept IS '角色分发部门映射表（角色的分发部门集合）。判定规则：角色无映射记录=遵循通用逻辑（scope_dept_id 子树 + 能看见目标用户）；有映射=映射分发角色，显式映射优先——分配时命中映射的操作者豁免 data_scope 覆盖校验，但目标用户所属部门必须存在于映射集合中；角色列表按映射对部门管理员可见属宿主应用层的呈现策略；用户最终数据访问边界仍由角色 data_scope 决定，与分配逻辑解耦 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_role_allowed_dept.id IS '主键 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.role_id IS '角色 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.dept_id IS '允许分配该角色的部门 ID（映射分发范围） [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role_allowed_dept.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_role_allowed_dept.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_role_allowed_dept.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_role_allowed_dept.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
-- 1a. sys_menu — 菜单/权限树
-- menu_type: 1-目录 2-菜单 3-按钮（操作权限点）
CREATE TABLE public.sys_menu
(
    id              int8                                                   NOT NULL,
    status          int2         DEFAULT 1                                 NOT NULL,
    "name"          varchar(64)                                            NOT NULL,
    permission_code varchar(128)                                           NOT NULL,
    parent_id       int8                                                   NULL,
    menu_type       int2         DEFAULT 1                                 NOT NULL,
    icon            varchar(128)                                           NULL,
    route_path      varchar(256)                                           NULL,
    component       varchar(256)                                           NULL,
    permission_expr varchar(256)                                           NULL,
    sort_order      int4         DEFAULT 0                                 NOT NULL,
    remark          varchar(512)                                           NULL,
    created_by      int8                                                   NOT NULL,
    created_at      timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by      int8                                                   NOT NULL,
    updated_at      timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at      timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id       varchar(16)                                            NOT NULL,
    CONSTRAINT sys_menu_pkey PRIMARY KEY (id),
    CONSTRAINT sys_menu_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT sys_menu_type_chk CHECK (menu_type BETWEEN 1 AND 3)
);
CREATE UNIQUE INDEX sys_menu_tenant_code_un ON public.sys_menu USING btree (tenant_id, permission_code) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_menu IS '菜单/权限表，定义系统功能菜单和操作权限的树形结构，通过 sys_role_menu 与角色关联 [@default_sort: sort_order ASC @pk: id @page: 20/200 @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_menu.id IS '菜单 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_menu.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_menu."name" IS '菜单/权限名称 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_menu.permission_code IS '权限编码（租户内唯一），应用层通过此编码匹配权限，命名如 sys:user:create、sys:user:delete [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_menu.parent_id IS '父级菜单 ID，NULL 为根菜单/顶级权限分组 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_menu.menu_type IS '菜单类型：1-目录 2-菜单 3-按钮（操作权限点） [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_menu.icon IS '菜单图标';
COMMENT ON COLUMN public.sys_menu.route_path IS '前端路由路径';
COMMENT ON COLUMN public.sys_menu.component IS '前端组件路径';
COMMENT ON COLUMN public.sys_menu.permission_expr IS '权限表达式，应用层 SPEL 匹配用';
COMMENT ON COLUMN public.sys_menu.sort_order IS '排序号 [@dynamic: exact, range @sortable]';
COMMENT ON COLUMN public.sys_menu.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_menu.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_menu.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_menu.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_menu.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_menu.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_menu.tenant_id IS '租户 ID [@dynamic: exact @hideable]';

-- 1b. sys_role_menu — 角色-菜单映射
--     映射行无可变业务字段，解除授权走物理删除，
--     因此仅保留 created_by/created_at 最小审计字段（谁在何时授权），不含 updated_*/deleted_*。
CREATE TABLE public.sys_role_menu
(
    role_id    int8                                   NOT NULL,
    menu_id    int8                                   NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_role_menu_pkey PRIMARY KEY (role_id, menu_id)
);
CREATE INDEX idx_srm_tenant ON public.sys_role_menu USING btree (tenant_id);
CREATE INDEX idx_srm_menu_id ON public.sys_role_menu USING btree (menu_id);
COMMENT ON TABLE public.sys_role_menu IS '角色-菜单/权限映射表，多对多关联角色与菜单权限点。映射行无可变业务字段，解除授权走物理删除，因此仅保留 created_by/created_at 最小审计字段，不含 updated_*/deleted_*';

COMMENT ON COLUMN public.sys_role_menu.role_id IS '角色 ID，关联 sys_role.id';
COMMENT ON COLUMN public.sys_role_menu.menu_id IS '菜单/权限 ID，关联 sys_menu.id';
COMMENT ON COLUMN public.sys_role_menu.tenant_id IS '租户 ID';
COMMENT ON COLUMN public.sys_role_menu.created_by IS '授权人（创建人），由应用层/ORM 写入';
COMMENT ON COLUMN public.sys_role_menu.created_at IS '授权时间（创建时间）';
