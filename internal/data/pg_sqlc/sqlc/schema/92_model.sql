-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 模型目录域：sys_model 单表双态。tenant_id = '' 为平台行（所有租户可见），
-- 非空为租户行（覆盖同 model_key 的平台行）。解析顺序租户 > 平台 > 全局
-- 配置 > conf。平台行只能经受控 SECURITY DEFINER 函数写入（属主绕过
-- RLS），租户行走普通 DML。
--
-- 两列密文/环境变量：api_key_cipher 存 AES-256-GCM 密文（只写不读，
-- 动态投影与接口响应都不返回），api_key_env 存环境变量名。

-- sys_model — 模型目录（平台/租户两级）
CREATE TABLE public.sys_model
(
    id             int8                                                   NOT NULL,
    tenant_id      varchar(16)  DEFAULT ''                                NOT NULL,
    status         int2         DEFAULT 1                                 NOT NULL,
    kind           varchar(16)                                            NOT NULL,
    "name"         varchar(128)                                           NOT NULL,
    model_key      varchar(128)                                           NOT NULL,
    provider       varchar(32)  DEFAULT 'openai'                          NOT NULL,
    base_url       varchar(512)                                           NULL,
    model          varchar(128)                                           NOT NULL,
    dimension      int4                                                   NULL,
    timeout_ms     int4                                                   NULL,
    api_key_cipher text                                                   NULL,
    api_key_env    varchar(128)                                           NULL,
    remark         varchar(512)                                           NULL,
    created_by     int8                                                   NOT NULL,
    created_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by     int8                                                   NOT NULL,
    updated_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at     timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    CONSTRAINT sys_model_pkey PRIMARY KEY (id),
    CONSTRAINT sys_model_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT sys_model_kind_chk CHECK (kind IN ('text', 'embedding', 'rerank')),
    CONSTRAINT sys_model_dim_chk CHECK (kind <> 'embedding' OR dimension IS NOT NULL),
    CONSTRAINT sys_model_dim_pos_chk CHECK (dimension IS NULL OR dimension > 0),
    CONSTRAINT sys_model_timeout_chk CHECK (timeout_ms IS NULL OR timeout_ms > 0)
);
CREATE UNIQUE INDEX sys_model_tenant_key_un ON public.sys_model USING btree (tenant_id, model_key) WHERE deleted_at IS NULL;
CREATE INDEX sys_model_kind_status_idx ON public.sys_model USING btree (kind, status);
COMMENT ON TABLE public.sys_model IS '模型目录表：tenant_id='''' 为平台行（所有租户可见），非空为租户行并覆盖同 model_key 平台行；密钥只存密文/环境变量名，接口只返回是否已配置 [@default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';

COMMENT ON COLUMN public.sys_model.id IS '模型 ID [@dynamic: exact, membership]';
COMMENT ON COLUMN public.sys_model.tenant_id IS '租户 ID，空串为平台行 [@internal]';
COMMENT ON COLUMN public.sys_model.status IS '数据状态，0：禁用、1：启用；停用不参与解析（fail-closed） [@dynamic: exact, membership, sort @updatable: set]';
COMMENT ON COLUMN public.sys_model.kind IS '模型类别：text / embedding / rerank（rerank 供检索重排，不携带维度） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.sys_model."name" IS '模型展示名 [@dynamic: fuzzy, exact, sort @updatable: set]';
COMMENT ON COLUMN public.sys_model.model_key IS '选择键与向量空间键（租户内未删除行间唯一），字符白名单同 vectorSpaceKeyPattern [@dynamic: exact, prefix]';
COMMENT ON COLUMN public.sys_model.provider IS 'provider 变体（openai/deepseek/qwen/glm/minimax/kimi/hunyuan 等） [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.sys_model.base_url IS 'OpenAI 兼容端点根地址 [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.sys_model.model IS '发给 provider 的模型名，可与 model_key 不同 [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.sys_model.dimension IS '嵌入维度；kind=embedding 时本期强制 1024 [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.sys_model.timeout_ms IS '单次调用超时（毫秒），空值回落 conf 默认 [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.sys_model.api_key_cipher IS 'API key 密文（v1:base64(nonce||ciphertext)），只写不读 [@internal]';
COMMENT ON COLUMN public.sys_model.api_key_env IS 'API key 环境变量名（密文缺失时回退） [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.sys_model.remark IS '备注 [@dynamic: fuzzy @updatable: set]';
COMMENT ON COLUMN public.sys_model.created_by IS '创建人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.sys_model.created_at IS '创建时间 [@dynamic: exact, membership, range, sort]';
COMMENT ON COLUMN public.sys_model.updated_by IS '更新人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.sys_model.updated_at IS '更新时间 [@dynamic: exact, membership, range]';
COMMENT ON COLUMN public.sys_model.deleted_at IS '删除时间 [@internal]';
