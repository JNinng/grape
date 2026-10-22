-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 身份域：sys_user（归属三元组 + @scope 守卫）、用户-角色/用户组映射、
-- sys_user_group。映射行无可变业务字段，仅保留最小审计列。
CREATE TABLE public.sys_user
(
    id                  int8                                                   NOT NULL,
    status              int2         DEFAULT 1                                 NOT NULL,
    "name"              varchar(256)                                           NOT NULL,
    nickname            varchar(256)                                           NOT NULL,
    "password"          text                                                   NOT NULL,
    email               varchar(256)                                           NULL,
    phone               varchar(32)                                            NULL,
    remark              varchar(512)                                           NULL,
    locale              varchar(10)  DEFAULT 'zh-CN'::character varying        NOT NULL,
    last_login_at       timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    failed_count        int4         DEFAULT 0                                 NOT NULL,
    locked_until        timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    created_by          int8                                                   NOT NULL,
    created_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by          int8                                                   NOT NULL,
    updated_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id           varchar(16)                                            NOT NULL,
    owner_user_id       int8                                                   NULL,
    owner_dept_id       int8                                                   NULL,
    owner_user_group_id int8                                                   NULL,
    CONSTRAINT sys_user_pkey PRIMARY KEY (id),
    CONSTRAINT sys_user_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_user_tenant_name_un ON public.sys_user USING btree (tenant_id, name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX sys_user_tenant_email_un ON public.sys_user USING btree (tenant_id, email) WHERE deleted_at IS NULL AND email IS NOT NULL;
CREATE UNIQUE INDEX sys_user_tenant_phone_un ON public.sys_user USING btree (tenant_id, phone) WHERE deleted_at IS NULL AND phone IS NOT NULL;
COMMENT ON TABLE public.sys_user IS '用户表 [@scope: owner_user_id in_any fn_accessible_user_ids(user_id:int) owner_dept_id in_any fn_accessible_dept_ids(user_id:int) owner_user_group_id in_any fn_accessible_group_ids(user_id:int) @default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_user.id IS '用户 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_user.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_user."name" IS '用户名 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_user.nickname IS '昵称 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_user."password" IS '密码 [@internal]';
COMMENT ON COLUMN public.sys_user.email IS '邮箱 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_user.phone IS '手机号 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_user.locale IS '用户语言偏好（BCP-47），如 zh-CN、en-US，影响界面展示语言和查询回退优先级 [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_user.last_login_at IS '最后登录时间 [@dynamic: range, is_null, not_null @sortable]';
COMMENT ON COLUMN public.sys_user.failed_count IS '连续密码错误次数，达到 sys_config 的 auth.lockout-threshold 后锁定，登录成功清零（不依赖扫日志统计） [@dynamic: exact, in, range @sortable]';
COMMENT ON COLUMN public.sys_user.locked_until IS '账号锁定截止时间，NULL 表示未锁定，登录时 CURRENT_TIMESTAMP >= locked_until 视为已解锁 [@dynamic: range, is_null, not_null]';
COMMENT ON COLUMN public.sys_user.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_user.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_user.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_user.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_user.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_user.owner_user_id IS '归属用户 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user.owner_dept_id IS '归属部门 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user.owner_user_group_id IS '归属用户组 ID [@dynamic: exact, in]';


-- public.sys_user_role_mapping 定义

CREATE TABLE public.sys_user_role_mapping
(
    user_id    int8                                   NOT NULL,
    role_id    int8                                   NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_user_role_mapping_pkey PRIMARY KEY (user_id, role_id)
);
CREATE INDEX idx_surm_tenant ON public.sys_user_role_mapping USING btree (tenant_id);
CREATE INDEX idx_surm_role_id ON public.sys_user_role_mapping USING btree (role_id);

-- Column comments

COMMENT ON TABLE public.sys_user_role_mapping IS '用户-角色映射表。映射行无可变业务字段，解除关联走物理删除，因此仅保留 created_by/created_at 最小审计字段（谁在何时授予），不含 updated_*/deleted_*';
COMMENT ON COLUMN public.sys_user_role_mapping.user_id IS '用户 ID';
COMMENT ON COLUMN public.sys_user_role_mapping.role_id IS '角色 ID';
COMMENT ON COLUMN public.sys_user_role_mapping.tenant_id IS '租户 ID';
COMMENT ON COLUMN public.sys_user_role_mapping.created_by IS '授予人（创建人），由应用层/ORM 写入';
COMMENT ON COLUMN public.sys_user_role_mapping.created_at IS '授予时间（创建时间）';


-- public.sys_user_group 定义

CREATE TABLE public.sys_user_group
(
    id            int8                                                   NOT NULL,
    status        int2         DEFAULT 1                                 NOT NULL,
    "name"        varchar(256)                                           NOT NULL,
    code          varchar(64)                                            NOT NULL,
    remark        varchar(512)                                           NULL,
    created_by    int8                                                   NOT NULL,
    created_at    timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by    int8                                                   NOT NULL,
    updated_at    timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at    timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id     varchar(16)                                            NOT NULL,
    owner_user_id int8                                                   NULL,
    CONSTRAINT sys_user_group_pkey PRIMARY KEY (id),
    CONSTRAINT sys_user_group_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_user_group_tenant_code_un ON public.sys_user_group USING btree (tenant_id, code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX sys_user_group_tenant_name_un ON public.sys_user_group USING btree (tenant_id, name) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_user_group IS '用户组表 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_user_group.id IS '用户组 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_user_group.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_user_group."name" IS '组名 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_user_group.code IS '组编码 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_user_group.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_user_group.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user_group.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_user_group.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_user_group.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_user_group.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_user_group.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_user_group.owner_user_id IS '组长/归属用户 [@dynamic: exact, in]';


-- public.sys_user_group_mapping 定义

CREATE TABLE public.sys_user_group_mapping
(
    group_id   int8                                   NOT NULL,
    user_id    int8                                   NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_user_group_mapping_pk PRIMARY KEY (group_id, user_id)
);
CREATE INDEX idx_sugm_tenant ON public.sys_user_group_mapping USING btree (tenant_id);
CREATE INDEX idx_sugm_user_id ON public.sys_user_group_mapping USING btree (user_id);

-- Column comments

COMMENT ON TABLE public.sys_user_group_mapping IS '用户组成员映射表。映射行无可变业务字段，移除成员走物理删除，因此仅保留 created_by/created_at 最小审计字段，不含 updated_*/deleted_*';
COMMENT ON COLUMN public.sys_user_group_mapping.group_id IS '用户组 ID';
COMMENT ON COLUMN public.sys_user_group_mapping.user_id IS '用户 ID';
COMMENT ON COLUMN public.sys_user_group_mapping.tenant_id IS '租户 ID';
COMMENT ON COLUMN public.sys_user_group_mapping.created_by IS '添加人（创建人），由应用层/ORM 写入';
COMMENT ON COLUMN public.sys_user_group_mapping.created_at IS '添加时间（创建时间）';
