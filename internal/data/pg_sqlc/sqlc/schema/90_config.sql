-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 配置域：sys_config（全局 key-value，不绑定租户、无 RLS）、
-- sys_tenant_config（租户配置）、sys_feature_flag（功能开关）、
-- sys_translation（通用翻译，BCP-47 locale）。
-- ============================================================================
-- 4. 系统全局配置
--    全局 key-value 配置，不绑定租户，无 RLS。
--    由平台超级管理员维护，应用层启动时读取。
-- ============================================================================

-- sys_config — 系统全局配置
CREATE TABLE public.sys_config
(
    id         int8                                                   NOT NULL,
    status     int2         DEFAULT 1                                 NOT NULL,
    "name"     varchar(128)                                           NOT NULL,
    config_key varchar(128)                                           NOT NULL,
    value      text                                                   NULL,
    value_type varchar(32)  DEFAULT 'string'                          NOT NULL,
    remark     varchar(512)                                           NULL,
    created_by int8                                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by int8                                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    CONSTRAINT sys_config_pkey PRIMARY KEY (id),
    CONSTRAINT sys_config_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_config_key_un ON public.sys_config USING btree (config_key) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_config IS '系统全局配置表，存储平台级系统参数，不绑定租户，所有租户共享 [@default_sort: created_at DESC @pk: id @page: 20/200 @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_config.id IS '配置 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_config.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_config."name" IS '配置名称 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_config.config_key IS '配置键（唯一），如 smtp.host、jwt.secret、session.timeout [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_config.value IS '配置值';
COMMENT ON COLUMN public.sys_config.value_type IS '值类型：string/integer/boolean/float/json [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_config.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_config.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_config.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_config.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_config.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_config.deleted_at IS '删除时间 [@internal]';


-- ============================================================================
-- 5. 租户配置
--    每个租户的自定义配置参数，有 RLS 租户隔离。
--    用于存储租户级别的个性化设置，覆盖全局配置的默认值。
-- ============================================================================

-- sys_tenant_config — 租户配置
CREATE TABLE public.sys_tenant_config
(
    id         int8                                                   NOT NULL,
    status     int2         DEFAULT 1                                 NOT NULL,
    "name"     varchar(128)                                           NOT NULL,
    config_key varchar(128)                                           NOT NULL,
    value      text                                                   NULL,
    value_type varchar(32)  DEFAULT 'string'                          NOT NULL,
    remark     varchar(512)                                           NULL,
    created_by int8                                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by int8                                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id  varchar(16)                                            NOT NULL,
    CONSTRAINT sys_tenant_config_pkey PRIMARY KEY (id),
    CONSTRAINT sys_tenant_config_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_tenant_config_key_un ON public.sys_tenant_config USING btree (tenant_id, config_key) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_tenant_config IS '租户配置表，存储各租户的自定义配置参数，覆盖系统全局配置的默认值 [@default_sort: created_at DESC @pk: id @page: 20/200 @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_tenant_config.id IS '配置 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant_config."name" IS '配置名称 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.config_key IS '配置键（租户内唯一） [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.value IS '配置值';
COMMENT ON COLUMN public.sys_tenant_config.value_type IS '值类型：string/integer/boolean/float/json [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_tenant_config.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_tenant_config.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_tenant_config.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_tenant_config.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_tenant_config.tenant_id IS '租户 ID [@dynamic: exact @hideable]';


-- ============================================================================
-- 6. 功能特性
--    租户级别的功能开关，控制哪些功能对特定租户开放。
--    支持灰度发布、按租户控制功能权限（如导出、SSO、API 访问等）。
-- ============================================================================

-- sys_feature_flag — 功能特性
CREATE TABLE public.sys_feature_flag
(
    id          int8                                                   NOT NULL,
    status      int2         DEFAULT 1                                 NOT NULL,
    "name"      varchar(128)                                           NOT NULL,
    flag_key    varchar(128)                                           NOT NULL,
    description varchar(512)                                           NULL,
    is_enabled  boolean      DEFAULT false                             NOT NULL,
    remark      varchar(512)                                           NULL,
    created_by  int8                                                   NOT NULL,
    created_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by  int8                                                   NOT NULL,
    updated_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at  timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id   varchar(16)                                            NOT NULL,
    CONSTRAINT sys_feature_flag_pkey PRIMARY KEY (id),
    CONSTRAINT sys_feature_flag_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_feature_flag_tenant_key_un ON public.sys_feature_flag USING btree (tenant_id, flag_key) WHERE deleted_at IS NULL;
COMMENT ON TABLE public.sys_feature_flag IS '功能特性表，控制各租户的功能开关，支持灰度发布和按租户授权 [@default_sort: created_at DESC @pk: id @page: 20/200 @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_feature_flag.id IS '特性 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_feature_flag."name" IS '特性名称 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.flag_key IS '特性键（租户内唯一），如 export_enabled、sso_enabled、api_access、audit_log [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.description IS '特性描述，说明该功能的作用和使用场景 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.is_enabled IS '是否启用：true-开启功能 false-关闭功能 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_feature_flag.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_feature_flag.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_feature_flag.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_feature_flag.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_feature_flag.tenant_id IS '租户 ID [@dynamic: exact @hideable]';


-- ============================================================================
-- 7. 国际适配 — 通用翻译表
--    为 sys_menu.name、sys_dict_data.label、sys_dept.name 等可翻译字段
--    提供多语言支持。一张表覆盖所有实体，通过 (table_name, row_id, column_name, locale)
--    唯一确定一条翻译记录。
-- ============================================================================

-- sys_translation — 通用翻译表
-- locale 格式：BCP-47 语言标记，如 zh-CN、en-US、ja-JP、ko-KR
CREATE TABLE public.sys_translation
(
    id          int8                                                   NOT NULL,
    status      int2         DEFAULT 1                                 NOT NULL,
    table_name  varchar(64)                                            NOT NULL,
    row_id      int8                                                   NOT NULL,
    column_name varchar(64)                                            NOT NULL,
    locale      varchar(10)                                            NOT NULL,
    value       text                                                   NOT NULL,
    remark      varchar(512)                                           NULL,
    created_by  int8                                                   NOT NULL,
    created_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by  int8                                                   NOT NULL,
    updated_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at  timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id   varchar(16)                                            NOT NULL,
    CONSTRAINT sys_translation_pkey PRIMARY KEY (id),
    CONSTRAINT sys_translation_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_translation_tenant_un ON public.sys_translation USING btree (table_name, row_id, column_name, locale, tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_translation_lookup ON public.sys_translation USING btree (table_name, row_id, column_name, locale) WHERE deleted_at IS NULL AND status = 1;
COMMENT ON TABLE public.sys_translation IS '通用翻译表，为业务表字段提供多语言翻译能力，支持按实体-字段-语言粒度管理 [@default_sort: created_at DESC @pk: id @page: 20/200 @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_translation.id IS '翻译 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_translation.status IS '数据状态，0：禁用、1：启用（可暂未翻译时禁用，前端回退原始值） [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_translation.table_name IS '来源表名，如 sys_menu、sys_dict_data、sys_dept [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_translation.row_id IS '来源行主键 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_translation.column_name IS '来源列名，如 name、label [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_translation.locale IS '语言代码（BCP-47），如 zh-CN、en-US、ja-JP [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_translation.value IS '翻译文本，对应语言的展示值 [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_translation.remark IS '备注 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_translation.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_translation.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_translation.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_translation.updated_at IS '更新时间 [@dynamic: range @sortable]';
COMMENT ON COLUMN public.sys_translation.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.sys_translation.tenant_id IS '租户 ID [@dynamic: exact @hideable]';