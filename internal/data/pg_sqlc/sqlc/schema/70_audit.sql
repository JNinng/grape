-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 审计域：登录/操作日志。追加写入（append-only），永不更新，因此
-- 省略 updated_by/updated_at/deleted_at；日志量较大，bigserial 自增主键。
-- ============================================================================
-- 2. 审计日志
--    日志表为追加写入（append-only），永不更新，因此省略 updated_by/updated_at/deleted_at。
--    日志量较大，使用 bigserial 自增主键。
-- ============================================================================

-- 2a. sys_login_log — 登录日志
CREATE TABLE public.sys_login_log
(
    id             bigserial                              NOT NULL,
    user_id        int8                                   NOT NULL,
    username       varchar(256)                           NOT NULL,
    tenant_id      varchar(16)                            NOT NULL,
    ip_address     varchar(64)                            NULL,
    user_agent     varchar(512)                           NULL,
    login_type     varchar(32)  DEFAULT 'password'        NOT NULL,
    is_success     boolean      DEFAULT true              NOT NULL,
    failure_reason varchar(256)                           NULL,
    login_at       timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    created_by     int8                                   NOT NULL,
    created_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_login_log_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_login_log_tenant_user ON public.sys_login_log USING btree (tenant_id, user_id);
CREATE INDEX idx_login_log_tenant_time ON public.sys_login_log USING btree (tenant_id, login_at DESC);
COMMENT ON TABLE public.sys_login_log IS '登录日志表，记录用户登录尝试（含失败记录），追加写入不可变更 [@default_sort: login_at DESC @pk: id @page: 20/200]';

COMMENT ON COLUMN public.sys_login_log.id IS '日志 ID，自增序列 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_login_log.user_id IS '用户 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_login_log.username IS '用户名（冗余，便于快速查看） [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_login_log.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_login_log.ip_address IS '登录 IP 地址 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_login_log.user_agent IS '浏览器/客户端 User-Agent';
COMMENT ON COLUMN public.sys_login_log.login_type IS '登录方式：password-密码登录 sms-短信验证码 oauth-第三方授权 refresh_token-刷新令牌 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_login_log.is_success IS '是否登录成功 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_login_log.failure_reason IS '失败原因：如密码错误、账号已禁用、租户已过期、验证码错误 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_login_log.login_at IS '登录时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_login_log.created_by IS '创建人/操作用户 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_login_log.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';

-- 2b. sys_operation_log — 操作日志
CREATE TABLE public.sys_operation_log
(
    id             bigserial                              NOT NULL,
    user_id        int8                                   NOT NULL,
    username       varchar(256)                           NOT NULL,
    tenant_id      varchar(16)                            NOT NULL,
    operation_type varchar(64)                            NOT NULL,
    target_table   varchar(64)                            NULL,
    target_id      varchar(64)                            NULL,
    old_value      jsonb                                  NULL,
    new_value      jsonb                                  NULL,
    ip_address     varchar(64)                            NULL,
    user_agent     varchar(512)                           NULL,
    api_path       varchar(512)                           NULL,
    http_method    varchar(16)                            NULL,
    duration_ms    int4                                   NULL,
    result_code    varchar(32)                            NULL,
    result_msg     varchar(512)                           NULL,
    request_id     varchar(64)                            NULL,
    operated_at    timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    created_by     int8                                   NOT NULL,
    created_at     timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_operation_log_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_operation_log_tenant_user ON public.sys_operation_log USING btree (tenant_id, user_id);
CREATE INDEX idx_operation_log_tenant_time ON public.sys_operation_log USING btree (tenant_id, operated_at DESC);
CREATE INDEX idx_operation_log_type ON public.sys_operation_log USING btree (tenant_id, operation_type);
COMMENT ON TABLE public.sys_operation_log IS '操作日志表，记录用户对系统数据的操作行为，追加写入不可变更 [@default_sort: operated_at DESC @pk: id @page: 20/200]';

COMMENT ON COLUMN public.sys_operation_log.id IS '日志 ID，自增序列 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_operation_log.user_id IS '操作用户 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_operation_log.username IS '用户名（冗余） [@dynamic: fuzzy, exact @sortable @hideable]';
COMMENT ON COLUMN public.sys_operation_log.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_operation_log.operation_type IS '操作类型：CREATE/UPDATE/DELETE/LOGIN/EXPORT/IMPORT/BATCH_DELETE 等 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_operation_log.target_table IS '操作目标表名 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_operation_log.target_id IS '操作目标记录 ID [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_operation_log.old_value IS '操作前数据（jsonb，写入时即校验 JSON 合法性，可索引）';
COMMENT ON COLUMN public.sys_operation_log.new_value IS '操作后数据（jsonb，可索引）';
COMMENT ON COLUMN public.sys_operation_log.ip_address IS '请求 IP 地址 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_operation_log.user_agent IS '浏览器/客户端 User-Agent';
COMMENT ON COLUMN public.sys_operation_log.api_path IS 'API 路径';
COMMENT ON COLUMN public.sys_operation_log.http_method IS 'HTTP 方法：GET/POST/PUT/DELETE/PATCH [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_operation_log.duration_ms IS '请求耗时（毫秒） [@dynamic: exact, range @sortable]';
COMMENT ON COLUMN public.sys_operation_log.result_code IS '结果码：200/400/403/404/500 等 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_operation_log.result_msg IS '结果消息 [@dynamic: fuzzy @hideable]';
COMMENT ON COLUMN public.sys_operation_log.request_id IS '请求追踪 ID，用于关联同一请求的多个操作 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_operation_log.operated_at IS '操作时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_operation_log.created_by IS '创建人/操作用户 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_operation_log.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
