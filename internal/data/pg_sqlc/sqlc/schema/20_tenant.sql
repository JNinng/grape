-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- sys_tenant — 租户表：RLS 额外允许命中自身 id（子租户挂在父租户名下
-- 时仍可读写自身行）。
CREATE TABLE public.sys_tenant
(
    id             varchar(16)                                            NOT NULL,
    status         int2         DEFAULT 1                                 NOT NULL,
    code           varchar(32)                                            NOT NULL,
    "name"         varchar(256)                                           NOT NULL,
    parent_id      varchar(16)                                            NULL,
    contact_name   varchar(64)                                            NULL,
    contact_phone  varchar(32)                                            NULL,
    expired_at     timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    max_user_count int4                                                   NULL,
    default_locale varchar(10)  DEFAULT 'zh-CN'::character varying        NOT NULL,
    created_by     int8                                                   NOT NULL,
    created_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by     int8                                                   NOT NULL,
    updated_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at     timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id      varchar(16)                                            NOT NULL,
    CONSTRAINT sys_tenant_pk PRIMARY KEY (id),
    CONSTRAINT sys_tenant_status_chk CHECK (status IN (0, 1))
);
-- 软删除后名称/编码可复用：用部分唯一索引替代表级 UNIQUE 约束（与其他表模式一致）
CREATE UNIQUE INDEX sys_tenant_name_un ON public.sys_tenant USING btree (name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX sys_tenant_code_un ON public.sys_tenant USING btree (code) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_tenant IS '租户表 [@default_sort: created_at DESC @pk: id @page: 20/400 @filter: deleted_at is_null]';

-- Column comments

COMMENT ON COLUMN public.sys_tenant.id IS '租户 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.code IS '租户编码 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_tenant."name" IS '租户名 [@dynamic: fuzzy, exact @sortable @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.parent_id IS '父级租户 ID [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_tenant.contact_name IS '联系人 [@dynamic: fuzzy @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.contact_phone IS '联系电话 [@dynamic: exact, prefix @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.expired_at IS '到期时间 [@dynamic: range, is_null, not_null @sortable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.max_user_count IS '最大用户数 [@dynamic: exact, in, range @sortable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.default_locale IS '租户默认语言（BCP-47），如 zh-CN、en-US，新建用户时继承此值 [@dynamic: exact @hideable @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_tenant.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant.updated_by IS '更新人 [@dynamic: exact, in @updatable: set]';
COMMENT ON COLUMN public.sys_tenant.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_tenant.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_tenant.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
