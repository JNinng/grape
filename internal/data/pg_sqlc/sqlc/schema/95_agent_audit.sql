-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- Agent 业务审计域：agent_tool_call_log 与
-- agent_confirmation_log。与 70_audit.sql 的系统日志同一形态——追加写入
-- （append-only），永不更新，因此省略 updated_by/updated_at/deleted_at，
-- 也不声明 @dynamic/@updatable 动态协议（动态更新面与追加写语义冲突，
-- 动态 List 协议由查询 API 的固定命名查询替代，见 queries/agent_audit.sql）；
-- 日志量较大，bigserial 自增主键。
--
-- 与 70_audit.sql 的系统日志的差异：这两张表的写入由 Agent 运行面
-- （internal/agent 的工具执行与 ack 决定路径，经 biz 审计用例）在业务
-- 事务内完成，租户取会话 app.tenant_id；摘要列在落库前已经 pkg/redact
-- 注册表脱敏（tool_param / retrieval 类别）并按应用层上限截断，长度
-- 约束在列注释声明（应用层保证，不设数据库 CHECK——上限调整不该伴随
-- 存量行迁移）。RLS 策略在本域文件内声明而非 rls.sql：rls.sql 是多任务
-- 流共触的共享文件，新域一律独立成文件、不改他人域
-- 文件；策略形态与 rls.sql 的租户隔离完全一致。
-- ============================================================================
-- Agent 业务审计
--    工具调用审计（谁、哪个租户、traceId、参数/结果摘要、重试、耗时、
--    状态）与授权确认审计（confirmation_id、决定、remember、路径
--    live|recovery、操作者），供运维复现视图按租户/时间/会话查询。
-- ============================================================================

-- agent_tool_call_log — 工具调用审计
CREATE TABLE public.agent_tool_call_log
(
    id                bigserial                              NOT NULL,
    tenant_id         varchar(16)                            NOT NULL,
    user_id           int8                                   NOT NULL,
    trace_id          varchar(64)                            NULL,
    session_id        varchar(255)                           NULL,
    tool_call_id      varchar(128)                           NOT NULL,
    tool_name         varchar(128)                           NOT NULL,
    arguments_summary text                                   NULL,
    result_summary    text                                   NULL,
    retry_count       int4         DEFAULT 0                 NOT NULL,
    duration_ms       int4                                   NULL,
    status            varchar(16)                            NOT NULL,
    called_at         timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    created_by        int8                                   NOT NULL,
    created_at        timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_tool_call_log_pkey PRIMARY KEY (id)
);
COMMENT ON TABLE public.agent_tool_call_log IS '工具调用审计表：记录 Agent 运行面每次工具调用的终态（一次调用一行，重试记最终态次数），append-only 不可变更';

COMMENT ON COLUMN public.agent_tool_call_log.id IS '日志 ID，自增序列';
COMMENT ON COLUMN public.agent_tool_call_log.tenant_id IS '租户 ID，取写入事务的 app.tenant_id';
COMMENT ON COLUMN public.agent_tool_call_log.user_id IS '发起运行的用户 ID（认证身份）';
COMMENT ON COLUMN public.agent_tool_call_log.trace_id IS '链路 ID：从认证 ctx 的 SpanContext 取（链路中间件注入），无链路事实时为 NULL';
COMMENT ON COLUMN public.agent_tool_call_log.session_id IS 'Agent 会话 ID（框架 session 键的会话段），按会话回放用';
COMMENT ON COLUMN public.agent_tool_call_log.tool_call_id IS '工具调用 ID（模型生成的 function call ID），租户内唯一';
COMMENT ON COLUMN public.agent_tool_call_log.tool_name IS '工具名';
COMMENT ON COLUMN public.agent_tool_call_log.arguments_summary IS '参数摘要：落库前经 pkg/redact 的 tool_param 类别脱敏；应用层截断上限 4096 rune（列宽不设 CHECK，见本文件头注释）';
COMMENT ON COLUMN public.agent_tool_call_log.result_summary IS '结果摘要：落库前经 pkg/redact 的 retrieval 类别脱敏（标准注册表 512 rune 截断），仓储层兜底截断上限同 arguments_summary';
COMMENT ON COLUMN public.agent_tool_call_log.retry_count IS '重试次数：框架工具重试可采集时记实际值，不可采集时记 0（框架不暴露重试计数，最终口径见根目录 operations.md）';
COMMENT ON COLUMN public.agent_tool_call_log.duration_ms IS '单次工具调用耗时（毫秒，最终一次执行的耗时），未知为 NULL';
COMMENT ON COLUMN public.agent_tool_call_log.status IS '调用终态：success-成功、error-失败';
COMMENT ON COLUMN public.agent_tool_call_log.called_at IS '调用发生时间';
COMMENT ON COLUMN public.agent_tool_call_log.created_by IS '写入者（认证用户，与 user_id 同值：审计行由该用户的运行产生）';
COMMENT ON COLUMN public.agent_tool_call_log.created_at IS '写入时间';

CREATE UNIQUE INDEX agent_tool_call_log_tenant_call_un ON public.agent_tool_call_log USING btree (tenant_id, tool_call_id);
CREATE INDEX idx_agent_tool_call_log_tenant_time ON public.agent_tool_call_log USING btree (tenant_id, called_at DESC);
CREATE INDEX idx_agent_tool_call_log_tenant_session ON public.agent_tool_call_log USING btree (tenant_id, session_id, called_at DESC);

-- agent_confirmation_log — 授权确认审计
CREATE TABLE public.agent_confirmation_log
(
    id              bigserial                              NOT NULL,
    tenant_id       varchar(16)                            NOT NULL,
    user_id         int8                                   NOT NULL,
    trace_id        varchar(64)                            NULL,
    confirmation_id varchar(128)                           NOT NULL,
    session_id      varchar(255)                           NOT NULL,
    tool_call_id    varchar(128)                           NULL,
    tool_name       varchar(128)                           NOT NULL,
    decision        varchar(16)                            NOT NULL,
    remember        varchar(16)                            NULL,
    path            varchar(16)                            NOT NULL,
    decided_by      varchar(128)                           NOT NULL,
    decided_at      timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    created_by      int8                                   NOT NULL,
    created_at      timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT agent_confirmation_log_pkey PRIMARY KEY (id)
);
COMMENT ON TABLE public.agent_confirmation_log IS '授权确认审计表：记录 ack 工具确认的人工决定（仅终态，一确认一行），append-only 不可变更';

COMMENT ON COLUMN public.agent_confirmation_log.id IS '日志 ID，自增序列';
COMMENT ON COLUMN public.agent_confirmation_log.tenant_id IS '租户 ID，取写入事务的 app.tenant_id';
COMMENT ON COLUMN public.agent_confirmation_log.user_id IS '确认所属会话的发起用户 ID（认证身份）';
COMMENT ON COLUMN public.agent_confirmation_log.trace_id IS '链路 ID：决定请求经链路中间件时从 SpanContext 取，无链路事实时为 NULL';
COMMENT ON COLUMN public.agent_confirmation_log.confirmation_id IS '确认 ID（ack Payload 的 confirmationId，与工具调用 ID 同源），租户内唯一：一确认只有一次终态决定';
COMMENT ON COLUMN public.agent_confirmation_log.session_id IS '确认所属的 Agent 会话 ID';
COMMENT ON COLUMN public.agent_confirmation_log.tool_call_id IS '关联的工具调用 ID（ack Payload 的 toolCallId），按会话回放时与工具调用审计对账';
COMMENT ON COLUMN public.agent_confirmation_log.tool_name IS '被确认的工具名';
COMMENT ON COLUMN public.agent_confirmation_log.decision IS '决定：approved-同意、denied-拒绝（pending 是决定前状态，不入审计）';
COMMENT ON COLUMN public.agent_confirmation_log.remember IS '决定随附的记住作用域：NULL-未记住、session-会话级、global-全局级（拒绝时恒为 NULL）';
COMMENT ON COLUMN public.agent_confirmation_log.path IS '决定生效路径：live-原运行存活直接唤醒、recovery-孤儿确认由网关代执行/合成拒绝后续跑';
COMMENT ON COLUMN public.agent_confirmation_log.decided_by IS '决定人：ack 网关的身份字符串（userKey 编码，与 B2/A1 同一编码）';
COMMENT ON COLUMN public.agent_confirmation_log.decided_at IS '决定时间';
COMMENT ON COLUMN public.agent_confirmation_log.created_by IS '写入者（认证用户：live 路径为决定人，recovery 路径为触发续跑的调用方）';
COMMENT ON COLUMN public.agent_confirmation_log.created_at IS '写入时间';

CREATE UNIQUE INDEX agent_confirmation_log_tenant_conf_un ON public.agent_confirmation_log USING btree (tenant_id, confirmation_id);
CREATE INDEX idx_agent_confirmation_log_tenant_time ON public.agent_confirmation_log USING btree (tenant_id, decided_at DESC);
CREATE INDEX idx_agent_confirmation_log_tenant_session ON public.agent_confirmation_log USING btree (tenant_id, session_id, decided_at DESC);


-- ============================================================
-- RLS 租户隔离：与 rls.sql 的租户表同一规则（tenant_id =
-- current_setting('app.tenant_id', true)，ENABLE + FORCE，表主不豁免；
-- 未设置 app.tenant_id 时比较恒为假 → 默认全拒）。策略落在本域文件
-- 而非 rls.sql 的理由见文件头注释（并行冲突纪律 + 新域独立成文件）。
-- grape_u 的 DML 授权不在此重复：role.sql 的默认权限（部署角色于
-- public schema 所建新表自动 GRANT）覆盖追加执行的本文件。
-- ============================================================

CREATE POLICY agent_tool_call_log_tenant_isolation
    ON public.agent_tool_call_log
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE POLICY agent_confirmation_log_tenant_isolation
    ON public.agent_confirmation_log
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE public.agent_tool_call_log
    ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_tool_call_log
    FORCE ROW LEVEL SECURITY;
ALTER TABLE public.agent_confirmation_log
    ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_confirmation_log
    FORCE ROW LEVEL SECURITY;
