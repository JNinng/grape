-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- Agent 检索留痕域：agent_retrieval_log。与
-- 95_agent_audit.sql 的业务审计表同一形态——追加写入（append-only），
-- 永不更新，因此省略 updated_by/updated_at/deleted_at，也不声明
-- @dynamic/@updatable 动态协议（动态更新面与追加写语义冲突，查询面由
-- 固定命名查询承担，见 queries/agent_retrieval_log.sql）；日志量较大，
-- bigserial 自增主键。
--
-- 留痕点：写入方是 data 层的 biz.VectorStore 留痕装饰器
-- （internal/data/pg_sqlc/retrieval_log.go，装配在向量仓储与框架适配器
-- 之间）——Search 记 query/top_k/命中/耗时（来源 tool），Add/Update/
-- DeleteByFilter 记摄取侧（来源 pipeline）。租户/用户/trace_id 从认证
-- ctx 提取，写入经 IdentityTx（每事务 set_config）。检索内容（查询文本）
-- 与过滤摘要在装饰器内经 pkg/redact 的 retrieval 类别脱敏并做长度兜底
-- 后落库；命中只存服务端产出的标识与数值（document_id/score/anchor），
-- 不含文档正文（正文反查经 agent.agent_document，验收链路：检索命中 →
-- 本表行 → document 反查）。
--
-- 保留期与归档策略：本表为证据面，保留与执行口径由根目录 operations.md
-- 「保留期与归档统一口径」统一裁定；当前不设清理、不设归档目标，
-- 以全量保留为默认语义。上限调整（列宽/截断口径）随应用层常量演进，
-- 不设数据库 CHECK——上限调整不该伴随存量行迁移。
--
-- 索引重建 / 过滤策略调整留痕：当前仓库无该管理面
-- （跨租户管理操作走 super 路径，仅留占位）；该管理面落地时以
-- source 新增取值（如 management）把索引重建、过滤策略调整写入本表，
-- 触发点即 super 管理路径的实施点，届时同步本注释与本表取值约束。
--
-- RLS 策略在本域文件内声明而非 rls.sql：rls.sql 是多域共享的文件，
-- 新域一律独立成文件、不改共享文件；策略形态与 rls.sql 的租户隔离完全
-- 一致（95 同口径）。grape_u 的 DML 授权不在此重复：role.sql 的默认
-- 权限（部署角色于 public schema 所建新表自动 GRANT）覆盖追加执行的
-- 本文件。
-- ============================================================================
-- Agent 检索留痕
--    向量检索与摄取写入的证据链（谁、哪个租户、traceId、查询文本、
--    top_k 请求值与生效值、过滤摘要、命中、耗时、来源、状态），供运维
--    复现视图按租户/会话/时间/来源查询，反查全部召回证据。
-- ============================================================================

-- agent_retrieval_log — 检索留痕
CREATE TABLE public.agent_retrieval_log
(
    id              bigserial                              NOT NULL,
    tenant_id       varchar(16)                            NOT NULL,
    user_id         int8                                   NOT NULL,
    trace_id        varchar(64)                            NULL,
    session_id      varchar(255)                           NULL,
    source          varchar(16)                            NOT NULL,
    query_text      text                                   NULL,
    requested_top_k int4                                   NULL,
    effective_top_k int4                                   NULL,
    filter_summary  jsonb                                  NULL,
    hits            jsonb                                  NULL,
    rerank_before   text                                   NULL,
    rerank_after    text                                   NULL,
    duration_ms     int4                                   NULL,
    status          varchar(16)                            NOT NULL,
    created_by      int8                                   NOT NULL,
    created_at      timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_retrieval_log_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE public.agent_retrieval_log IS '检索留痕表：记录每次向量检索与摄取写入的证据链（一次操作一行，append-only 不可变更）。保留期与归档策略由根目录 operations.md「保留期与归档统一口径」裁定，当前不设清理、不设归档目标，以全量保留为默认语义；索引重建/过滤策略调整属 super 管理面，该面落地时以新增 source 取值留痕于本表';

COMMENT ON COLUMN public.agent_retrieval_log.id IS '日志 ID，自增序列';
COMMENT ON COLUMN public.agent_retrieval_log.tenant_id IS '租户 ID，取写入事务的 app.tenant_id';
COMMENT ON COLUMN public.agent_retrieval_log.user_id IS '发起操作的用户 ID（认证身份）';
COMMENT ON COLUMN public.agent_retrieval_log.trace_id IS '链路 ID：从认证 ctx 的 SpanContext 取（链路中间件注入），无链路事实时为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.session_id IS 'Agent 会话 ID：检索工具链路经 ctx 事实载体从框架执行 ctx 提取（agent 层注入），不可得时为 NULL；摄取管线当前无会话事实，恒为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.source IS '检索来源：tool-检索工具链路（Search）、pipeline-摄取管线（Add/Update/DeleteByFilter）；管理面落地时扩展取值（见本文件头注释）';
COMMENT ON COLUMN public.agent_retrieval_log.query_text IS '检索查询文本：经 pkg/redact 的 retrieval 类别脱敏（标准注册表 512 rune 截断）+ 应用层兜底截断 4096 rune；tool 来源取 ctx 携带的查询事实（Embedder 调用文本），无事实时不伪造（NULL）；pipeline 来源为 NULL——摄取正文不重复留痕，经 hits 的 document_id 反查 agent_document';
COMMENT ON COLUMN public.agent_retrieval_log.requested_top_k IS '请求的 top_k：调用方过滤条件传入的 Limit，NULL 为未显式指定。框架适配器的 top_k 硬上限钳制（20）发生在本值上游（internal/agent/vectorstore.go），本值是存储契约实际收到的请求值；模型原始入参可对账 agent_tool_call_log 的参数摘要。非检索操作为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.effective_top_k IS '实际生效的 LIMIT：requested_top_k 非正（含 NULL）时取存储层默认页大小 10；非检索操作为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.filter_summary IS '过滤条件摘要（jsonb）：ids/metadata/min_score 形态，metadata 值经 retrieval 类别脱敏；脱敏截断破坏 JSON 结构时以 {"summary_truncated": …} 摘要形态落库（应用层兜底）；无条件为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.hits IS '命中数组（jsonb）：元素为 {"document_id","score","anchor"}，按相似度降序，上限 50 条（应用层截断，超限不再另行标记——检索上限 20 之下为防御直连契约的超大 Limit 而设）；仅存服务端产出的标识与数值，不含文档正文。检索行为记录命中；摄取行为复用本列记录作用文档（单元素，anchor 取切片元数据）';
COMMENT ON COLUMN public.agent_retrieval_log.rerank_before IS '预留：rerank 前的命中序（本仓未引入 reranker，检索为纯向量单路，暂无挂点恒 NULL）；引入重排时写入重排前的 document_id 序，届时同步列注释';
COMMENT ON COLUMN public.agent_retrieval_log.rerank_after IS '预留：rerank 后的命中序（同 rerank_before，本仓未引入 reranker 恒 NULL）';
COMMENT ON COLUMN public.agent_retrieval_log.duration_ms IS '操作耗时（毫秒）；未知为 NULL';
COMMENT ON COLUMN public.agent_retrieval_log.status IS '操作终态：success-成功、error-失败（留痕装饰器记录尝试与结果两侧，失败行不反噬业务主流程）';
COMMENT ON COLUMN public.agent_retrieval_log.created_by IS '写入者（认证用户，与 user_id 同值：留痕行由该用户的操作产生）';
COMMENT ON COLUMN public.agent_retrieval_log.created_at IS '写入时间';

-- 索引满足租户+时间 / 租户+会话 / 租户+来源三类查询面（按会话/时间
-- 分页、按来源过滤）；created_at DESC 与查询排序同向，id 为同秒 tie-break。
CREATE INDEX idx_agent_retrieval_log_tenant_time ON public.agent_retrieval_log USING btree (tenant_id, created_at DESC);
CREATE INDEX idx_agent_retrieval_log_tenant_session ON public.agent_retrieval_log USING btree (tenant_id, session_id, created_at DESC);
CREATE INDEX idx_agent_retrieval_log_tenant_source ON public.agent_retrieval_log USING btree (tenant_id, source, created_at DESC);


-- ============================================================
-- RLS 租户隔离：与 rls.sql / 95_agent_audit.sql 的租户表同一规则
-- （tenant_id = current_setting('app.tenant_id', true)，ENABLE + FORCE，
-- 表主不豁免；未设置 app.tenant_id 时比较恒为假 → 默认全拒）。策略
-- 落在本域文件而非 rls.sql 的理由见文件头注释。仓储读侧 WHERE 再显式
-- 带同源租户谓词（queries/agent_retrieval_log.sql），RLS 被关闭时读取
-- 仍不越租户（对齐「隔离正确性不押在 RLS 是否存在上」）。
-- ============================================================

CREATE POLICY agent_retrieval_log_tenant_isolation
    ON public.agent_retrieval_log
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE public.agent_retrieval_log
    ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_retrieval_log
    FORCE ROW LEVEL SECURITY;
