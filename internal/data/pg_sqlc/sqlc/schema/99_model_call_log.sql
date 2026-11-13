-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- Agent 模型调用留痕域：agent_model_call_log。与
-- 97_agent_retrieval_log.sql 的检索留痕同一形态——追加写入（append-only），
-- 永不更新，因此省略 updated_by/updated_at/deleted_at，也不声明
-- @updatable 动态更新面（动态更新与追加写语义冲突）。查询面走动态查询
-- 协议：列注释 DSL 声明过滤/排序能力（系统设置「模型调用」管理页消费），
-- 写入面由固定命名查询承担（queries/agent_model_call_log.sql）；日志量
-- 较大，bigserial 自增主键。
--
-- 留痕点：写入侧是管线侧模型/类模型调用面的 ModelCallObserver 观察
-- 面（openaicompleter 文本生成、openaiembedder 向量化、openaireranker
-- 检索重排、docparse 外部解析——场景词表见 biz/model_call_log.go），
-- 观察面按「解析出端点事实之后、调用终态（含 error）」记一次调用一行；
-- 失败行同样留痕（status=error + error 摘要），空产出记 output 为空。
-- 租户/用户/trace_id 从认证 ctx 提取，写入经 IdentityTx（每事务
-- set_config）。输入/输出摘要在观察面内经 pkg/redact 的 model_call 类别
-- 脱敏（rune 截断 4096）后落库；向量产物与原件字节不落留痕（embedder
-- 记维度说明、docparse 记 size/sha256、reranker 记分数条数说明）。
-- agent 对话 LLM 不在本表：会话事件流与 /agui/session-usage 已承载该面
-- （见 replay.md），避免双写。
--
-- scenario 词表由应用层把门（biz 观察面校验），不设数据库 CHECK——
-- 词表扩展不伴随存量行迁移；摘要与错误列的上限同口径在应用层执行。
-- 错误摘要列声明 `@dynamic: fuzzy`（模糊检索），与登录日志
-- `failure_reason` 同口径。
--
-- 保留期与归档策略：本表为证据面，保留与执行口径由根目录 operations.md
-- 「保留期与归档统一口径」统一裁定（与审计、检索留痕同一口径）。
--
-- RLS 策略在本域文件内声明而非 rls.sql：rls.sql 是多域共享的文件，
-- 新域一律独立成文件、不改共享文件；策略形态与 rls.sql 的租户隔离完全
-- 一致（95 / 97 同口径）。grape_u 的 DML 授权不在此重复：role.sql 的默认
-- 权限（部署角色于 public schema 所建新表自动 GRANT）覆盖追加执行的
-- 本文件。
-- ============================================================================
-- Agent 模型调用留痕
--    管线侧模型/类模型调用的证据链（谁、哪个租户、traceId、场景、模型
--    键、端点、作用文档、输入/输出摘要、token 用量、耗时、状态与错误摘
--    要），供系统设置「模型调用」管理页按租户/场景/文档/时间等动态条件
--    查询，对账摄取与解析的外部调用事实。
-- ============================================================================

-- agent_model_call_log — 模型调用留痕
CREATE TABLE public.agent_model_call_log
(
    id             bigserial                              NOT NULL,
    tenant_id      varchar(16)                            NOT NULL,
    user_id        int8                                   NOT NULL,
    trace_id       varchar(32)                            NULL,
    session_id     varchar(255)                           NULL,
    scenario       varchar(32)                            NOT NULL,
    model_key      varchar(128)                           NULL,
    endpoint       varchar(255)                           NULL,
    document_id    int8                                   NULL,
    input_summary  text                                   NULL,
    output_summary text                                   NULL,
    input_tokens   int4                                   NULL,
    output_tokens  int4                                   NULL,
    duration_ms    int4                                   NULL,
    status         varchar(16)                            NOT NULL,
    error          varchar(512)                           NULL,
    created_by     int8                                   NOT NULL,
    created_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_model_call_log_pkey PRIMARY KEY (id)
);

COMMENT ON TABLE public.agent_model_call_log IS '模型调用留痕表：记录管线侧每次模型/类模型调用（文本生成、向量化、检索重排、外部文档解析）的证据链（一次调用一行，append-only 不可变更）。scenario 词表由应用层把门（biz/model_call_log.go），无数据库 CHECK；保留期与归档策略由根目录 operations.md「保留期与归档统一口径」裁定，当前不设清理、不设归档目标，以全量保留为默认语义 [@default_sort: created_at DESC @pk: id]';

COMMENT ON COLUMN public.agent_model_call_log.id IS '日志 ID，自增序列 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.agent_model_call_log.tenant_id IS '租户 ID，取写入事务的 app.tenant_id [@internal]';
COMMENT ON COLUMN public.agent_model_call_log.user_id IS '发起操作的用户 ID（认证身份） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.agent_model_call_log.trace_id IS '链路 ID：从认证 ctx 的 SpanContext 取（链路中间件注入），无链路事实时为 NULL [@dynamic: exact, prefix]';
COMMENT ON COLUMN public.agent_model_call_log.session_id IS 'Agent 会话 ID：经 ctx 事实载体从执行现场提取（biz.WithSessionID），管线侧调用无会话事实，恒为 NULL；保留列以对齐留痕面形态，框架侧模型调用不经本表 [@internal]';
COMMENT ON COLUMN public.agent_model_call_log.scenario IS '调用场景：ingest_summary-摄取文档摘要、ingest_prefix-摄取块前缀、embed_document-摄取向量化、embed_query-检索查询向量化、parse_document-外部文档解析（类模型 OCR）、rerank-检索重排；场景由调用链路经 ctx 事实载体声明（biz.WithModelCallScene），缺失时按实现声明的缺省回落（embed→embed_document、completer→ingest_prefix、reranker→rerank；docparse 无回落，缺失放弃该行），不伪造其他值；词表扩展在 biz 常量与列注释同步 [@dynamic: exact, membership]';
COMMENT ON COLUMN public.agent_model_call_log.model_key IS '模型键（模型目录 model_key / 嵌入空间键）：本次调用的目标模型按现场解析原样留痕，不做注册表对账——失败尝试的原始声明正是排查换模型事故的证据；无模型事实（解析失败前的行不存在）为 NULL [@dynamic: exact, prefix]';
COMMENT ON COLUMN public.agent_model_call_log.endpoint IS '调用端点（协议路径完整 URL，如 {base_url}/chat/completions）：按解析现场留痕；端点事实缺失为 NULL';
COMMENT ON COLUMN public.agent_model_call_log.document_id IS '作用文档 ID（kb_document.id）：摄取与解析链路经 ctx 事实载体声明（biz.WithModelCallDocument），无文档事实（如检索查询向量化）为 NULL [@dynamic: exact, membership]';
COMMENT ON COLUMN public.agent_model_call_log.input_summary IS '输入摘要：completer 记 prompt、embedder 记向量化文本、reranker 记查询文本、docparse 记原件事实（mime/filename/size/sha256——原件字节不落留痕）；经 pkg/redact 的 model_call 类别脱敏（rune 截断 4096）+ 应用层兜底截断同上限';
COMMENT ON COLUMN public.agent_model_call_log.output_summary IS '输出摘要：completer 记生成文本、embedder 记维度说明（向量不落库）、reranker 记分数条数说明（分数不落库）、docparse 记 markdown 产物截断；空产出记空串（落库 NULL）';
COMMENT ON COLUMN public.agent_model_call_log.input_tokens IS '输入 token 用量（端点 usage.prompt_tokens）：端点未上报为 NULL；调用失败不必然置空——失败行端点已上报的用量如实保留（如维度漂移失败行）';
COMMENT ON COLUMN public.agent_model_call_log.output_tokens IS '输出 token 用量（端点 usage.completion_tokens）：端点未上报为 NULL；调用失败不必然置空——失败行端点已上报的用量如实保留';
COMMENT ON COLUMN public.agent_model_call_log.duration_ms IS '调用耗时（毫秒）；未知为 NULL [@dynamic: exact, range, sort]';
COMMENT ON COLUMN public.agent_model_call_log.status IS '调用终态：success-成功、error-失败（观察面记录尝试与终态两侧，失败行不反噬业务主流程） [@dynamic: exact, membership]';
COMMENT ON COLUMN public.agent_model_call_log.error IS '失败错误摘要（截断 512，经 model_call 类别脱敏）：成功行为 NULL [@dynamic: fuzzy]';
COMMENT ON COLUMN public.agent_model_call_log.created_by IS '写入者（认证用户，与 user_id 同值：留痕行由该用户的操作产生）';
COMMENT ON COLUMN public.agent_model_call_log.created_at IS '写入时间 [@dynamic: exact, membership, range, sort]';

-- 索引满足租户+时间 / 租户+场景 / 租户+文档三类查询面（按时间分页、
-- 按场景过滤、按文档反查摄取与解析证据）；created_at DESC 与查询排序
-- 同向，id 为同秒 tie-break。
CREATE INDEX idx_agent_model_call_log_tenant_time ON public.agent_model_call_log USING btree (tenant_id, created_at DESC);
CREATE INDEX idx_agent_model_call_log_tenant_scenario ON public.agent_model_call_log USING btree (tenant_id, scenario, created_at DESC);
CREATE INDEX idx_agent_model_call_log_tenant_document ON public.agent_model_call_log USING btree (tenant_id, document_id);


-- ============================================================
-- RLS 租户隔离：与 rls.sql / 95_agent_audit.sql / 97_agent_retrieval_log.sql
-- 的租户表同一规则（tenant_id = current_setting('app.tenant_id', true)，
-- ENABLE + FORCE，表主不豁免；未设置 app.tenant_id 时比较恒为假 →
-- 默认全拒）。策略落在本域文件而非 rls.sql 的理由见文件头注释。仓储
-- 读侧 WHERE 再显式带同源租户谓词（queries/agent_model_call_log.sql），
-- RLS 被关闭时读取仍不越租户（对齐「隔离正确性不押在 RLS 是否存在上」）。
-- grape_u 的 DML 授权不在此重复：role.sql 的默认权限（部署角色于
-- public schema 所建新表自动 GRANT）覆盖追加执行的本文件。
-- ============================================================

CREATE POLICY agent_model_call_log_tenant_isolation
    ON public.agent_model_call_log
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE public.agent_model_call_log
    ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_model_call_log
    FORCE ROW LEVEL SECURITY;
