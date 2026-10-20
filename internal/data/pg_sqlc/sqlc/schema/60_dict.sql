-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 字典域：sys_dict_type + sys_dict_data（租户内公共数据）。
CREATE TABLE public.sys_dict_type
(
    id         int8                                                   NOT NULL,
    status     int2         DEFAULT 1                                 NOT NULL,
    "name"     varchar(256)                                           NOT NULL,
    code       varchar(64)                                            NOT NULL,
    remark     varchar(512)                                           NULL,
    created_by int8                                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by int8                                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id  varchar(16)                                            NOT NULL,
    CONSTRAINT sys_dict_type_pkey PRIMARY KEY (id),
    CONSTRAINT sys_dict_type_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_dict_type_tenant_code_un ON public.sys_dict_type USING btree (tenant_id, code) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_dict_type IS '字典类型表 [@default_sort: created_at DESC @pk: id @page: 20/400]';

-- Column comments

COMMENT ON COLUMN public.sys_dict_type.id IS '字典类型 ID [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_type.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_type."name" IS '字典类型名 [@dynamic: fuzzy, exact @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_type.code IS '字典类型编码 [@dynamic: exact, prefix @selectable]';
COMMENT ON COLUMN public.sys_dict_type.remark IS '备注 [@dynamic: fuzzy @selectable]';
COMMENT ON COLUMN public.sys_dict_type.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dict_type.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_type.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dict_type.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_dict_type.deleted_at IS '删除时间 [@hidden]';
COMMENT ON COLUMN public.sys_dict_type.tenant_id IS '租户 ID [@dynamic: exact @selectable]';


-- public.sys_dict_data 定义

CREATE TABLE public.sys_dict_data
(
    id           int8                                                   NOT NULL,
    status       int2         DEFAULT 1                                 NOT NULL,
    dict_type_id int8                                                   NOT NULL,
    label        varchar(256)                                           NOT NULL,
    value        varchar(256)                                           NOT NULL,
    value_type   varchar(32)  DEFAULT 'string'                          NOT NULL,
    sort_order   int4         DEFAULT 0                                 NOT NULL,
    remark       varchar(512)                                           NULL,
    created_by   int8                                                   NOT NULL,
    created_at   timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by   int8                                                   NOT NULL,
    updated_at   timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at   timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id    varchar(16)                                            NOT NULL,
    CONSTRAINT sys_dict_data_pkey PRIMARY KEY (id),
    CONSTRAINT sys_dict_data_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_dict_data_type_value_un ON public.sys_dict_data USING btree (tenant_id, dict_type_id, value) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_dict_data IS '字典数据表 [@default_sort: created_at DESC @pk: id @page: 20/400]';

-- Column comments

COMMENT ON COLUMN public.sys_dict_data.id IS '字典数据 ID [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_data.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_data.dict_type_id IS '字典类型 ID [@dynamic: exact, in @selectable]';
COMMENT ON COLUMN public.sys_dict_data.label IS '字典标签 [@dynamic: fuzzy, exact @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_data.value IS '字典值 [@dynamic: exact, prefix @selectable]';
COMMENT ON COLUMN public.sys_dict_data.value_type IS '值类型：string/integer/boolean/float/json，系统获取值时根据此类型做预处理 [@dynamic: exact @selectable]';
COMMENT ON COLUMN public.sys_dict_data.sort_order IS '排序号 [@dynamic: exact, range @sortable]';
COMMENT ON COLUMN public.sys_dict_data.remark IS '备注 [@dynamic: fuzzy @selectable]';
COMMENT ON COLUMN public.sys_dict_data.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dict_data.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @selectable]';
COMMENT ON COLUMN public.sys_dict_data.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_dict_data.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_dict_data.deleted_at IS '删除时间 [@hidden]';
COMMENT ON COLUMN public.sys_dict_data.tenant_id IS '租户 ID [@dynamic: exact @selectable]';
