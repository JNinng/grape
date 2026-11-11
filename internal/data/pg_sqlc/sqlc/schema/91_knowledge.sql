-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 知识库域：kb_library（知识库）、kb_document（知识条目）与 kb_file
-- （文件介质）。条目正文经摄取管线切分、向量化后落入
-- agent.agent_document（一个 chunk 一行，不建独立的 chunk 管理表）；
-- 本域只承载目录与管理面事实。kb_document 不存二进制：kind=file 的条目
-- 原件落内嵌 OSS（object_key 指向），本域 kb_file 行只存介质元数据，
-- 留原件是为了解析器升级后能重解析。
-- 三表均为租户表：RLS ENABLE + FORCE，业务写路径走普通 DML。
--
-- 列注释 DSL 口径同其他业务表：tenant_id/deleted_at 为 [@internal]，
-- 动态查询能力按列声明。kb_library 带归属三元组与 @scope 守卫（租户内的
-- 库级可见性），kb_document / kb_file 无归属——条目随库的管理面走，检索
-- 侧的可见性由所属库决定，介质行只随宿主条目读写。

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
    chunk_delimiter     text         DEFAULT E'\n\n'                           NOT NULL,
    contextual_chunking boolean      DEFAULT false                             NOT NULL,
    valid_from          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    valid_to            timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    created_by          int8                                                   NOT NULL,
    created_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by          int8                                                   NOT NULL,
    updated_at          timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at          timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id           varchar(16)                                            NOT NULL,
    owner_user_id       int8                                                   NULL,
    owner_dept_id       int8                                                   NULL,
    owner_user_group_id int8                                                   NULL,
    CONSTRAINT kb_library_pkey PRIMARY KEY (id),
    CONSTRAINT kb_library_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT kb_library_chunk_chk CHECK (chunk_size > 0 AND chunk_overlap >= 0 AND chunk_overlap < chunk_size),
    CONSTRAINT kb_library_chunk_delimiter_chk CHECK (char_length(chunk_delimiter) <= 32),
    CONSTRAINT kb_library_valid_chk CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from)
);
CREATE UNIQUE INDEX kb_library_tenant_name_un ON public.kb_library USING btree (tenant_id, "name") WHERE deleted_at IS NULL;
CREATE INDEX kb_library_tenant_status_idx ON public.kb_library USING btree (tenant_id, status);
-- 归属三元组索引：data_scope 行级过滤与可见库枚举的高频路径
CREATE INDEX kb_library_owner_user_idx ON public.kb_library USING btree (owner_user_id);
CREATE INDEX kb_library_owner_dept_idx ON public.kb_library USING btree (owner_dept_id);
CREATE INDEX kb_library_owner_group_idx ON public.kb_library USING btree (owner_user_group_id);
COMMENT ON TABLE public.kb_library IS '知识库表：租户内知识条目的归类与管理面，条目正文切分后的向量落 agent.agent_document [@scope: owner_user_id in_any fn_accessible_user_ids(user_id:int) owner_dept_id in_any fn_accessible_dept_ids(user_id:int) owner_user_group_id in_any fn_accessible_group_ids(user_id:int) @default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';

COMMENT ON COLUMN public.kb_library.id IS '知识库 ID [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, membership, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library."name" IS '知识库名（租户内未删除行间唯一） [@dynamic: fuzzy, exact, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.remark IS '备注 [@dynamic: fuzzy @updatable: set]';
COMMENT ON COLUMN public.kb_library.embedding_model_key IS '嵌入模型键；为空表示跟随租户/全局默认解析，非空必须是可见的 embedding 模型 model_key [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.kb_library.chunk_size IS '切分单块 rune 上限，缺省 800 [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.kb_library.chunk_overlap IS '相邻块重叠 rune 数，缺省 100，须小于 chunk_size [@dynamic: exact, range @updatable: set]';
COMMENT ON COLUMN public.kb_library.chunk_delimiter IS '切分区分串：非空时先按该字符串切出段落块（区分串不入块），超过 chunk_size 的段落块再按固定窗口切分；空串表示不按区分串切分（纯固定窗口）。新建库缺省为两个换行（段落空行），至多 32 字符；历史库升级回填空串保持原行为 [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.kb_library.contextual_chunking IS '上下文检索开关：开启后摄取在切分之后、向量化之前为每块生成上下文前缀（本文出处 + 本块要旨），前缀只拼入向量输入，不改变切片正文与锚点；前缀生成失败按质量增强降级为无前缀继续摄取 [@dynamic: exact @updatable: set]';
COMMENT ON COLUMN public.kb_library.valid_from IS '生效时间，NULL 表示立即生效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.valid_to IS '失效时间，NULL 表示长期有效 [@dynamic: exact, range, sort @updatable: set]';
COMMENT ON COLUMN public.kb_library.created_by IS '创建人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.created_at IS '创建时间 [@dynamic: exact, membership, range, sort]';
COMMENT ON COLUMN public.kb_library.updated_by IS '更新人 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.updated_at IS '更新时间 [@dynamic: exact, membership, range]';
COMMENT ON COLUMN public.kb_library.deleted_at IS '删除时间 [@internal]';
COMMENT ON COLUMN public.kb_library.tenant_id IS '租户 ID [@internal]';
COMMENT ON COLUMN public.kb_library.owner_user_id IS '归属用户 ID（创建人缺省归属，NULL=无该归属） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.owner_dept_id IS '归属部门 ID（NULL=无该归属） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_library.owner_user_group_id IS '归属用户组 ID（NULL=无该归属） [@dynamic: exact, membership]';


-- kb_document — 知识条目（kind 分派切分策略与摄取形态：text / markdown /
-- file；file 的正文是解析产物 markdown，原件元数据在 file_* 与 file_id）
CREATE TABLE public.kb_document
(
    id               int8                                                   NOT NULL,
    library_id       int8                                                   NOT NULL,
    kind             varchar(16)  DEFAULT 'text'                            NOT NULL,
    title            varchar(256)                                           NOT NULL,
    content          text                                                   NULL,
    source_name      varchar(512)                                           NULL,
    content_hash     varchar(64)                                            NULL,
    file_id          int8                                                   NULL,
    file_name        varchar(256)                                           NULL,
    mime_type        varchar(128)                                           NULL,
    file_size        int8                                                   NULL,
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
    CONSTRAINT kb_document_kind_chk CHECK (kind IN ('text', 'markdown', 'file')),
    CONSTRAINT kb_document_text_chk CHECK (kind = 'file' OR (content IS NOT NULL AND btrim(content) <> '')),
    CONSTRAINT kb_document_file_chk CHECK (kind <> 'file' OR (file_name IS NOT NULL AND mime_type IS NOT NULL)),
    CONSTRAINT kb_document_status_chk CHECK (status IN (0, 1)),
    CONSTRAINT kb_document_valid_chk CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to > valid_from)
);
CREATE INDEX kb_document_tenant_library_idx ON public.kb_document USING btree (tenant_id, library_id, deleted_at);
CREATE INDEX kb_document_tenant_status_idx ON public.kb_document USING btree (tenant_id, status);
COMMENT ON TABLE public.kb_document IS '知识条目表：正文经切分、向量化后落 agent.agent_document；状态与有效期同步进 chunk metadata 供检索过滤 [@default_sort: updated_at DESC @pk: id @filter: deleted_at is_null]';

COMMENT ON COLUMN public.kb_document.id IS '条目 ID [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.library_id IS '所属知识库 ID（库存在且同租户由用例校验） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.kb_document.kind IS '条目类型：text（纯文本，区分串切分）/ markdown（Markdown，标题切分）/ file（文件解析条目，原件经解析产 markdown 后走标题切分） [@dynamic: exact]';
COMMENT ON COLUMN public.kb_document.title IS '条目标题 [@dynamic: fuzzy, exact, sort @updatable: set]';
COMMENT ON COLUMN public.kb_document.content IS '条目正文（text / markdown 必填非空白；file 解析成功前可空，解析产物入库后为 markdown；向量化来源）';
COMMENT ON COLUMN public.kb_document.source_name IS '来源名，缺省取标题 [@dynamic: fuzzy, exact @updatable: set]';
COMMENT ON COLUMN public.kb_document.content_hash IS '正文摘要（变更检测与重嵌判断，不参与检索） [@internal]';
COMMENT ON COLUMN public.kb_document.file_id IS '原件介质行 ID（kb_file，本租户内由用例校验；仅 kind=file 非空） [@dynamic: exact]';
COMMENT ON COLUMN public.kb_document.file_name IS '原件文件名（保留上传原名，与嗅探 MIME 不一致时以 MIME 为准；仅 kind=file 非空） [@dynamic: fuzzy, exact]';
COMMENT ON COLUMN public.kb_document.mime_type IS '原件 MIME（内容嗅探结果，不信任客户端声明；仅 kind=file 非空） [@dynamic: exact]';
COMMENT ON COLUMN public.kb_document.file_size IS '原件字节数（≤ 20MB，与 oss_object 上限一致；仅 kind=file 非空） [@dynamic: exact, range]';
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


-- kb_file — 文件介质表（kind=file 条目的原件介质元数据；对象字节落内嵌
-- OSS，本表只存元数据，将来切真实 OSS 语义不变）。留原件是为了解析器
-- 升级后能重解析：kb_document 行持有 file_id 引用，重解析按 object_key
-- 现场取回字节。租户表，RLS 在 rls.sql；读写面只有固定 CRUD
-- （queries/kb_file.sql），不参与动态查询协议。file_id 指向本租户介质行
-- 由用例校验（单行读在租户事务内判定，跨租户不可见即拒绝）。
CREATE TABLE public.kb_file
(
    id          int8                                                   NOT NULL,
    object_key  text                                                   NOT NULL,
    etag        varchar(64)                                            NOT NULL,
    sha256      varchar(64)                                            NOT NULL,
    mime_type   varchar(128)                                           NOT NULL,
    file_size   int8                                                   NOT NULL,
    created_by  int8                                                   NOT NULL,
    created_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    updated_by  int8                                                   NOT NULL,
    updated_at  timestamp(0) DEFAULT CURRENT_TIMESTAMP                 NOT NULL,
    deleted_at  timestamp(0) DEFAULT NULL::timestamp without time zone NULL,
    tenant_id   varchar(16)                                            NOT NULL,
    CONSTRAINT kb_file_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE public.kb_file IS '文件介质表：kind=file 知识条目的原件介质元数据（对象字节落内嵌 OSS，object_key 指向），租户表（RLS 在 rls.sql）；留原件是为了解析器升级后能重解析';

COMMENT ON COLUMN public.kb_file.id IS '介质行 ID（kb_document.file_id 引用）';
COMMENT ON COLUMN public.kb_file.object_key IS '对象键（内嵌 OSS 的 S3 key，形如 kb-file/<发号 ID>，URL 安全 ASCII；表只存元数据，将来切真实 OSS 语义不变）';
COMMENT ON COLUMN public.kb_file.etag IS '对象内容 MD5 十六进制（S3 ETag 语义，写入 OSS 时的返回值）';
COMMENT ON COLUMN public.kb_file.sha256 IS '原件内容 sha256 十六进制（介质行的内容身份，与 etag 的 MD5 互为冗余校验）';
COMMENT ON COLUMN public.kb_file.mime_type IS '原件 MIME（内容嗅探结果，不信任客户端声明）';
COMMENT ON COLUMN public.kb_file.file_size IS '原件字节数（≤ 20MB，与 oss_object 上限一致，由应用层限流保证）';
COMMENT ON COLUMN public.kb_file.created_by IS '创建人（上传操作者）';
COMMENT ON COLUMN public.kb_file.created_at IS '创建时间（上传时间）';
COMMENT ON COLUMN public.kb_file.updated_by IS '更新人';
COMMENT ON COLUMN public.kb_file.updated_at IS '更新时间';
COMMENT ON COLUMN public.kb_file.deleted_at IS '删除时间（宿主条目删除时同批软删，OSS 对象尽力清理） [@internal]';
COMMENT ON COLUMN public.kb_file.tenant_id IS '租户 ID [@internal]';
