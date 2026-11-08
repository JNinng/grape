-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 知识库域：kb_library（知识库）与 kb_document（知识条目）。条目正文
-- 经摄取管线切分、向量化后落入 agent.agent_document（一个 chunk 一行，
-- 不建独立的 chunk 管理表）；本域只承载目录与管理面事实。
-- 两表均为租户表：RLS ENABLE + FORCE，业务写路径走普通 DML。
--
-- 列注释 DSL 口径同其他业务表：tenant_id/deleted_at 为 [@internal]，
-- 动态查询能力按列声明；当前无归属三元组（表无 @scope，租户内全可见）。

-- kb_library — 知识库
CREATE TABLE public.kb_library
(
    id                  int8                                                   NOT NULL,
    status              int2         DEFAULT 1                                 NOT NULL,
    "name"              varchar(128)                                           NOT NULL,
    remark              varchar(512)                                           NULL,
    embedding_model_key varchar(128)                                           NULL,
    chunk_size          int4         DEFAULT 800                               NOT NULL,
    chunk_overlap       int4         DEFAULT 100                               NOT NULL,
    valid_from          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    valid_to            timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    created_by          int8                                                   NOT NULL,
    created_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by          int8                                                   NOT NULL,
    updated_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id           varchar(16)                                            NOT NULL,
    CONSTRAINT kb_library_pkey PRIMARY KEY (id),
    CONSTRAINT kb_library_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT kb_library_chunk_chk CHECK (chunk_size > 0 AND chunk_overlap >= 0 AND chunk_overlap < chunk_size),
    CONSTRAINT kb_library_valid_chk CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from)
);
CREATE UNIQUE INDEX kb_library_tenant_name_un ON public.kb_library USING btree (tenant_id, "name") WHERE deleted_at IS NULL;
CREATE INDEX kb_library_tenant_status_idx ON public.kb_library USING btree (tenant_id, status);
COMMENT ON TABLE public.kb_library IS '知识库表：租户内知识条目的归类与管理面，条目正文切分后的向量落 agent.agent_document [@default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';

COMMENT ON COLUMN public.kb_library.id IS '知识库 ID [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, membership, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library."name" IS '知识库名（租户内未删除行间唯一） [@dynamic: fuzzy, exact, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.remark IS '备注 [@dynamic: fuzzy @updatable: set]';
COMMENT ON COLUMN public.kb_library.embedding_model_key IS '嵌入模型键；为空表示跟随租户/全局默认解析，非空必须是可见的 embedding 模型 model_key [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.kb_library.chunk_size IS '切分单块 rune 上限，缺省 800 [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.kb_library.chunk_overlap IS '相邻块重叠 rune 数，缺省 100，须小于 chunk_size [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.kb_library.valid_from IS '生效时间，NULL 表示立即生效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.valid_to IS '失效时间，NULL 表示长期有效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.created_by IS '创建人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.created_at IS '创建时间 [@dynamic: exact, membership, range, sort]';
COMMENT ON COLUMN public.kb_library.updated_by IS '更新人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.updated_at IS '更新时间 [@dynamic: exact, membership, range]';
COMMENT ON COLUMN public.kb_library.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.kb_library.tenant_id IS '租户 ID [@internal]';


-- kb_document — 知识条目（本期文本；file 只留字段，接口拒绝创建）
CREATE TABLE public.kb_document
(
    id               int8                                                   NOT NULL,
    library_id       int8                                                   NOT NULL,
    kind             varchar(16)  DEFAULT 'text'                            NOT NULL,
    title            varchar(256)                                           NOT NULL,
    content          text                                                   NULL,
    source_name      varchar(512)                                           NULL,
    content_hash     varchar(64)                                            NULL,
    status           int2         DEFAULT 1                                 NOT NULL,
    valid_from       timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    valid_to         timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    indexed_at       timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    indexed_embedder varchar(128)                                           NULL,
    created_by       int8                                                   NOT NULL,
    created_at       timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by       int8                                                   NOT NULL,
    updated_at       timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at       timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id        varchar(16)                                            NOT NULL,
    CONSTRAINT kb_document_pkey PRIMARY KEY (id),
    CONSTRAINT kb_document_kind_chk CHECK (kind IN ('text', 'file')),
    CONSTRAINT kb_document_text_chk CHECK (kind <> 'text' OR (content IS NOT NULL AND btrim(content) <> '')),
    CONSTRAINT kb_document_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT kb_document_valid_chk CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from)
);
CREATE INDEX kb_document_tenant_library_idx ON public.kb_document USING btree (tenant_id, library_id, deleted_at);
CREATE INDEX kb_document_tenant_status_idx ON public.kb_document USING btree (tenant_id, status);
COMMENT ON TABLE public.kb_document IS '知识条目表：正文经切分、向量化后落 agent.agent_document；状态与有效期同步进 chunk metadata 供检索过滤 [@default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';

COMMENT ON COLUMN public.kb_document.id IS '条目 ID [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.library_id IS '所属知识库 ID（库存在且同租户由用例校验） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.kind IS '条目类型：text（本期可用）/file（预留，创建接口拒绝） [@dynamic: exact]';
COMMENT ON COLUMN public.kb_document.title IS '条目标题 [@dynamic: fuzzy, exact, sort @updatable: set]';
COMMENT ON COLUMN public.kb_document.content IS '条目正文（text 类型必填非空白；向量化来源）';
COMMENT ON COLUMN public.kb_document.source_name IS '来源名，缺省取标题 [@dynamic: fuzzy, exact @updatable: set]';
COMMENT ON COLUMN public.kb_document.content_hash IS '正文摘要（变更检测与重嵌判断，不参与检索） [@internal]';
COMMENT ON COLUMN public.kb_document.status IS '数据状态，0：禁用、1：启用；与所属库状态取与参与检索 [@dynamic: exact, membership, sort @updatable: set]';
COMMENT ON COLUMN public.kb_document.valid_from IS '生效时间，NULL 表示立即生效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_document.valid_to IS '失效时间，NULL 表示长期有效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_document.indexed_at IS '最近一次成功摄取时间（管理页展示与重嵌判断） [@dynamic: exact, range, sort]';
COMMENT ON COLUMN public.kb_document.indexed_embedder IS '最近一次成功摄取的空间键 [@dynamic: exact]';
COMMENT ON COLUMN public.kb_document.created_by IS '创建人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.created_at IS '创建时间 [@dynamic: exact, membership, range, sort]';
COMMENT ON COLUMN public.kb_document.updated_by IS '更新人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.updated_at IS '更新时间 [@dynamic: exact, membership, range, sort]';
COMMENT ON COLUMN public.kb_document.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.kb_document.tenant_id IS '租户 ID [@internal]';
