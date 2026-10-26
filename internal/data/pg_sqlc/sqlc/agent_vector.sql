-- ============================================================
-- agent_vector.sql — 向量检索设施（pgvector，超级用户面）
--
-- 向量层（TODO 流 D）的声明资产：扩展、agent schema、agent_document
-- 向量表、HNSW 索引与运行角色授权。本文件不进 sqlc.yaml 输入：
-- pgvector 的类型与相似度算子超出 sqlc catalog 的类型映射支持面，且
-- 向量存取没有动态查询协议需求，存取 SQL 以手写参数化常量落在
-- internal/data/pg_sqlc/vector_repo.go（治理口径见 sqlc/README.md）。
-- 本文件只由部署清单（deploy.go DeployFiles）以超级用户/表 owner 连接
-- 执行，排在 role.sql 之后（授权语句引用 grape_u/test_u）。
--
-- 租户隔离模型（TODO 决策 6）：应用层适配器是强制约束——所有读写从
-- 认证 ctx 取 tenant_id/user_id、身份同时写入 metadata 列并在每条 SQL
-- 强制租户过滤，缺身份直接拒绝。本表不启用 RLS（可选加强语句见文件
-- 尾注释），隔离正确性不得依赖 RLS 是否存在。
-- ============================================================

-- pgvector 扩展：vector 类型与余弦距离算子等。IF NOT EXISTS：扩展属
-- 库级安装，重复部署沿用已装实例。
CREATE EXTENSION IF NOT EXISTS vector;

-- agent schema 是 agent 域设施的独立命名空间（框架会话表 B2 亦落此处）。
-- IF NOT EXISTS 与并行流共存：任一侧先到先建。
CREATE SCHEMA IF NOT EXISTS agent;

COMMENT ON SCHEMA agent IS 'Agent 域设施 schema：框架会话存储与向量检索表，无租户 RLS，租户隔离由应用层强制（TODO 决策 6）';

-- agent_document — 租户向量文档。id 由调用方（摄取管线/框架侧）生成，
-- varchar 承载框架 document.Document 的字符串 ID（UUID 等），租户内唯一；
-- tenant_id/user_id 由仓储从认证 ctx 提取，同时冗余进 metadata（框架侧
-- metadata 过滤语义可用），列为本体、每条 SQL 强制过滤。embedding 维度
-- 与 conf.vector.dimension 一致，装配期探针校验不一致即拒绝启动。
CREATE TABLE agent.agent_document
(
    id         varchar(128)                           NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    user_id    int8                                   NOT NULL,
    name       varchar(512)                           NULL,
    content    text                                   NOT NULL,
    embedding  vector(1024)                           NOT NULL,
    metadata   jsonb        DEFAULT '{}'              NOT NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_by int8                                   NOT NULL,
    updated_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_document_pkey PRIMARY KEY (tenant_id, id)
);

COMMENT ON TABLE agent.agent_document IS '租户向量文档（RAG 检索）：租户隔离由应用层适配器强制（决策 6），RLS 仅作可选加强；embedding 维度 1024 须与 conf.vector.dimension 一致';
COMMENT ON COLUMN agent.agent_document.id IS '文档 ID（调用方生成，租户内唯一，承载框架字符串 ID）';
COMMENT ON COLUMN agent.agent_document.tenant_id IS '租户 ID：仓储从认证 ctx 提取并强制过滤，不来自请求侧输入';
COMMENT ON COLUMN agent.agent_document.user_id IS '写入用户 ID：仓储从认证 ctx 提取，同时冗余进 metadata';
COMMENT ON COLUMN agent.agent_document.name IS '文档名/标题（可空）';
COMMENT ON COLUMN agent.agent_document.content IS '文档正文（检索命中后回传给模型的原文）';
COMMENT ON COLUMN agent.agent_document.embedding IS '文档向量（pgvector，float4），维度固定 1024，余弦距离度量';
COMMENT ON COLUMN agent.agent_document.metadata IS '文档元数据（jsonb）：含仓储注入的 tenant_id/user_id 与调用方自定义键值，过滤经容器运算符整值下发';
COMMENT ON COLUMN agent.agent_document.created_by IS '创建人（认证 ctx 的用户 ID）';
COMMENT ON COLUMN agent.agent_document.created_at IS '创建时间';
COMMENT ON COLUMN agent.agent_document.updated_by IS '更新人（认证 ctx 的用户 ID）';
COMMENT ON COLUMN agent.agent_document.updated_at IS '更新时间';

-- HNSW 索引（余弦距离 <=>），选型说明：
--   - HNSW 而非 IVFFlat：IVFFlat 建索引依赖表内已有数据训练聚类中心，
--     空表建索引后召回随插入劣化、lists 难定；HNSW 无训练步骤，增量
--     插入召回稳定，中小规模（单租户百万级以下）延迟与召回折中更优。
--   - m=16 / ef_construction=64 为 pgvector 默认起点：m 越大图越稠密、
--     召回越高但构建与内存开销越大；检索侧精度由会话级 hnsw.ef_search
--     调节（默认 40），不在索引内固化，留给 D2 检索配置。
--   - vector_cosine_ops 与仓储侧归一化分数同源：score = 1 - 距离/2，
--     与框架 pgvector 实现同语义（[0,1]，越大越相似）。
--   - 索引随部署事务同建，在线重建属 super 管理口径（TODO D1 只留
--     占位，不暴露普通入口）。
CREATE INDEX agent_document_embedding_hnsw
    ON agent.agent_document USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- 租户内按写入时间排序（GetMetadata 分页）与租户边界扫描前缀。
CREATE INDEX agent_document_tenant_created_idx
    ON agent.agent_document USING btree (tenant_id, created_at);

-- 运行角色授权（grape_u/test_u 由 role.sql 创建，故本文件排在其后）：
-- agent schema 使用权与表 DML。默认权限覆盖部署方后续在 agent schema
-- 内补建的表（与 role.sql 对 public 的处理同风格）。
GRANT USAGE ON SCHEMA agent TO grape_u, test_u;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA agent TO grape_u, test_u;
ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA agent
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO grape_u, test_u;

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
-- 不变；租户隔离的强制执行点仍是应用层适配器（决策 6），RLS 缺失时
-- 隔离不得失效（集成测试以无 RLS 场景断言跨租户不可召回）。
-- ============================================================
