-- ============================================================
-- agent_vector.sql — 向量检索设施（pgvector，超级用户面）
--
-- 向量层的声明资产：扩展、agent schema、agent_document
-- 向量表、每维度表达式部分 HNSW 索引与运行角色授权。本文件不进
-- sqlc.yaml 输入：pgvector 的类型与相似度算子超出 sqlc catalog 的类型
-- 映射支持面，且向量存取没有动态查询协议需求，存取 SQL 以手写参数化
-- 常量落在 internal/data/pg_sqlc/vector/vector_repo.go（治理口径见
-- sqlc/README.md）。本文件只由部署清单（deploy.go DeployFiles）以超级
-- 用户/表 owner 连接执行，排在 role.sql 之后（授权语句引用
-- grape_u/test_u）。
--
-- 空间模型（多模型与版本迭代）：一表承载多嵌入空间。embedding 列是裸
-- vector（无 typmod，维度随行），embedder 列逐行声明空间键（模型[@版本]，
-- 即 conf.vector.embedders 派生的身份），embed_dim 列声明维度并由
-- CHECK 与实际向量长度绑定（vector_dims）——身份与维度在库层不可拆
-- 伪造。余弦相似只在同一空间内可比，检索以空间键等值 + 维度谓词命中
-- 对应维度的表达式部分 HNSW 索引，跨空间混排检索不存在。
--
-- 新维度上线流程：conf.vector.embedders 加条目 + 本文件加对应维度的
-- 表达式部分索引，二者装配期探针逐维度对账（缺索引启动即失败）。
-- 维度上限：vector 类型存储上限 16000 维；HNSW 的 vector_cosine_ops
-- 可索引上限 2000 维，更高维度需换 halfvec 表达式索引变体（查询侧
-- cast 同步调整），当前未实现、探针会以缺索引拒绝启动。
--
-- 迁移口径：本文件建表/建索引是非幂等结构 DDL，重复执行按结构 DDL
-- 口径报错（全量重建先重建库）；存量单维度表升级到多空间形状同样走
-- 全量重建（deploy_db.md）。
--
-- 租户隔离模型：应用层适配器是强制约束——所有读写从
-- 认证 ctx 取 tenant_id/user_id、身份同时写入 metadata 列并在每条 SQL
-- 强制租户过滤，缺身份直接拒绝。本表不启用 RLS（可选加强语句见文件
-- 尾注释），隔离正确性不得依赖 RLS 是否存在。
-- ============================================================

-- pgvector 扩展：vector 类型、vector_dims 函数与余弦距离算子等。
-- IF NOT EXISTS：扩展属库级安装，重复部署沿用已装实例。
CREATE EXTENSION IF NOT EXISTS vector;

-- agent schema 是 agent 域设施的独立命名空间（框架会话表亦落此处）。
-- 本文件的 CREATE SCHEMA IF NOT EXISTS 与会话流建表文件谁先执行无关：
-- schema 已存在即幂等沿用，两文件不因顺序互斥；幂等边界仅及于此——
-- agent_document 的建表/建索引仍是非幂等结构 DDL，重复执行按结构 DDL
-- 口径报错（全量重建先重建库）。
CREATE SCHEMA IF NOT EXISTS agent;

COMMENT ON SCHEMA agent IS 'Agent 域设施 schema：框架会话存储与向量检索表，无租户 RLS，租户隔离由应用层强制';

-- agent_document — 租户向量文档（多嵌入空间）。id 由调用方（摄取管线/
-- 框架侧）生成，varchar 承载框架 document.Document 的字符串 ID（UUID
-- 等），租户内唯一；tenant_id/user_id 由仓储从认证 ctx 提取，同时冗余
-- 进 metadata（框架侧 metadata 过滤语义可用），列为本体、每条 SQL 强制
-- 过滤。embedder/embed_dim 与 embedding 三方由 CHECK 绑定，应用侧注册
-- 表（conf.vector.embedders）对账在先，库层 CHECK 兜底。
CREATE TABLE agent.agent_document
(
    id         varchar(128)                           NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    user_id    int8                                   NOT NULL,
    embedder   varchar(128)                           NOT NULL,
    embed_dim  int4                                   NOT NULL,
    name       varchar(512)                           NULL,
    content    text                                   NOT NULL,
    embedding  vector                                 NOT NULL,
    metadata   jsonb        DEFAULT '{}'              NOT NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_by int8                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_document_pkey PRIMARY KEY (tenant_id, id),
    CONSTRAINT agent_document_dim_chk CHECK (embed_dim = vector_dims(embedding))
);

COMMENT ON TABLE agent.agent_document IS '租户向量文档（RAG 检索，多嵌入空间）：租户隔离由应用层适配器强制，RLS 仅作可选加强；embedder/embed_dim 与 embedding 由 CHECK 绑定，空间注册表见 conf.vector.embedders，每维度索引见本文件表达式部分 HNSW';
COMMENT ON COLUMN agent.agent_document.id IS '文档 ID（调用方生成，租户内唯一，承载框架字符串 ID）';
COMMENT ON COLUMN agent.agent_document.tenant_id IS '租户 ID：仓储从认证 ctx 提取并强制过滤，不来自请求侧输入';
COMMENT ON COLUMN agent.agent_document.user_id IS '写入用户 ID：仓储从认证 ctx 提取，同时冗余进 metadata';
COMMENT ON COLUMN agent.agent_document.embedder IS '嵌入空间键（模型[@版本]）：conf.vector.embedders 派生的身份，检索与清理以等值过滤；存储层只做等值比对、从不解析，键语义由注册表声明';
COMMENT ON COLUMN agent.agent_document.embed_dim IS '该行向量维度：应用侧写入注册表声明值，CHECK 与 embedding 实际维度绑定，检索谓词命中同维度表达式索引';
COMMENT ON COLUMN agent.agent_document.name IS '文档名/标题（可空）';
COMMENT ON COLUMN agent.agent_document.content IS '文档正文（检索命中后回传给模型的原文）';
COMMENT ON COLUMN agent.agent_document.embedding IS '文档向量（pgvector，float4）：裸 vector 无固定维度，维度随行（embed_dim），余弦距离只在同空间内可比';
COMMENT ON COLUMN agent.agent_document.metadata IS '文档元数据（jsonb）：含仓储注入的 tenant_id/user_id/embedder/embed_dim 与调用方自定义键值，过滤经容器运算符整值下发';
COMMENT ON COLUMN agent.agent_document.created_by IS '创建人（认证 ctx 的用户 ID）';
COMMENT ON COLUMN agent.agent_document.created_at IS '创建时间';
COMMENT ON COLUMN agent.agent_document.updated_by IS '更新人（认证 ctx 的用户 ID）';
COMMENT ON COLUMN agent.agent_document.updated_at IS '更新时间';

-- 表达式部分 HNSW 索引（每维度一个，余弦距离 <=>）：
--   - 表达式 (embedding::vector(N)) 把裸 vector 收敛到声明维度——pgvector
--     对无 typmod 列只支持表达式索引，且查询侧必须重复同一 cast 表达式
--     才会命中（vector_repo.go 的检索 SQL 与此逐字一致，维度来自校验后
--     的注册表值内联）；部分谓词 WHERE embed_dim = N 把索引范围收敛到
--     该维度行，同维度多空间共享一个索引（空间键在检索 WHERE 内等值
--     过滤）。
--   - HNSW 而非 IVFFlat：IVFFlat 建索引依赖表内已有数据训练聚类中心，
--     空表建索引后召回随插入劣化、lists 难定；HNSW 无训练步骤，增量
--     插入召回稳定，中小规模（单租户百万级以下）延迟与召回折中更优。
--   - m=16 / ef_construction=64 为 pgvector 默认起点：m 越大图越稠密、
--     召回越高但构建与内存开销越大；检索侧精度由会话级 hnsw.ef_search
--     调节（默认 40），不在索引内固化，留给检索侧配置。
--   - 索引随部署事务同建，在线重建属 super 管理口径（只留占位，
--     不暴露普通入口）。新维度 = 新增同形态索引（见文件头注释）。
CREATE INDEX agent_document_embedding_1024_hnsw
    ON agent.agent_document USING hnsw ((embedding::vector(1024)) vector_cosine_ops) WITH (m = 16, ef_construction = 64)
    WHERE embed_dim = 1024;

-- 租户内按写入时间排序（GetMetadata 分页）与租户边界扫描前缀。
CREATE INDEX agent_document_tenant_created_idx
    ON agent.agent_document USING btree (tenant_id, created_at);

-- 运行角色授权（grape_u/test_u 由 role.sql 创建，故本文件排在其后）：
-- agent schema 使用权与向量表 DML。授权收窄到本文件自建的表，不用
-- ALL TABLES/默认权限覆盖整个 schema——agent schema 还承载框架会话
-- 表（agent_session.sql），其授权面由该文件单独声明，不得被顺带放宽。
GRANT USAGE ON SCHEMA agent TO grape_u, test_u;
GRANT SELECT, INSERT, UPDATE, DELETE ON agent.agent_document TO grape_u, test_u;

-- ============================================================
-- 可选加强（默认不启用，不作为验收前提）：若需数据库层兜底租户边界
-- （第二道防线），以表属主另行执行——
--
--   ALTER TABLE agent.agent_document ENABLE ROW LEVEL SECURITY;
--   ALTER TABLE agent.agent_document FORCE ROW LEVEL SECURITY;
--   CREATE POLICY agent_document_tenant_isolation ON agent.agent_document
--       USING (tenant_id = current_setting('app.tenant_id'))
--       WITH CHECK (tenant_id = current_setting('app.tenant_id'));
--
-- 启用后普通读写路径（IdentityTx 事务，app.tenant_id 已注入）行为
-- 不变；租户隔离的强制执行点仍是应用层适配器，RLS 缺失时
-- 隔离不得失效（集成测试以无 RLS 场景断言跨租户不可召回）。
-- ============================================================
