-- grape 数据权限体系 schema（sqlc 输入，单一事实源；按序号分域拆分，
-- sqlc.yaml 全量按序加载，部署执行顺序见 ../README.md）。
--
-- 会话域：sys_session。令牌字段仅存哈希值，不存明文，支持服务端撤销。
-- ============================================================================
-- 3. 会话管理
--    存储访问令牌和刷新令牌的哈希，支持服务端会话撤销。
--    令牌字段仅存哈希值，不存明文令牌，降低泄露风险。
-- ============================================================================

-- sys_session — 用户会话/令牌
CREATE TABLE public.sys_session
(
    id                 int8                                   NOT NULL,
    status             int2         DEFAULT 1                 NOT NULL,
    user_id            int8                                   NOT NULL,
    tenant_id          varchar(16)                            NOT NULL,
    token_hash         varchar(128)                           NOT NULL,
    refresh_token_hash varchar(128)                           NULL,
    expires_at         timestamp(0)                           NOT NULL,
    refresh_expires_at timestamp(0)                           NULL,
    last_activity_at   timestamp(0)                           NULL,
    device_info        varchar(512)                           NULL,
    ip_address         varchar(64)                            NULL,
    is_revoked         boolean      DEFAULT false             NOT NULL,
    created_by         int8                                   NOT NULL,
    created_at         timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_by         int8                                   NOT NULL,
    updated_at         timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT sys_session_pkey PRIMARY KEY (id),
    CONSTRAINT sys_session_status_chk CHECK (status IN (0, 1))
);
CREATE UNIQUE INDEX sys_session_token_hash_un ON public.sys_session USING btree (token_hash);
CREATE UNIQUE INDEX sys_session_refresh_token_hash_un ON public.sys_session USING btree (refresh_token_hash) WHERE refresh_token_hash IS NOT NULL;
CREATE INDEX idx_session_tenant_user ON public.sys_session USING btree (tenant_id, user_id);
COMMENT ON TABLE public.sys_session IS '用户会话/令牌表，管理登录访问令牌和刷新令牌，支持服务端主动撤销 [@default_sort: created_at DESC @pk: id @page: 20/200]';

COMMENT ON COLUMN public.sys_session.id IS '会话 ID [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_session.status IS '数据状态，0：禁用、1：启用 [@dynamic: exact, in @sortable @hideable]';
COMMENT ON COLUMN public.sys_session.user_id IS '用户 ID [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_session.tenant_id IS '租户 ID [@dynamic: exact @hideable]';
COMMENT ON COLUMN public.sys_session.token_hash IS '访问令牌哈希（SHA-256），用于查找和验证令牌，不存明文 [@internal]';
COMMENT ON COLUMN public.sys_session.refresh_token_hash IS '刷新令牌哈希，用于令牌刷新轮换 [@internal]';
COMMENT ON COLUMN public.sys_session.expires_at IS '访问令牌过期时间 [@dynamic: exact, range @sortable]';
COMMENT ON COLUMN public.sys_session.refresh_expires_at IS '刷新令牌过期时间 [@dynamic: range, is_null, not_null]';
COMMENT ON COLUMN public.sys_session.last_activity_at IS '最近一次签发/刷新时间；校验为只读点查不刷新此列，僵尸会话检测需另行采样 [@dynamic: range, is_null, not_null @sortable]';
COMMENT ON COLUMN public.sys_session.device_info IS '设备信息：操作系统、浏览器、设备型号等';
COMMENT ON COLUMN public.sys_session.ip_address IS '登录时 IP 地址 [@dynamic: exact, prefix @hideable]';
COMMENT ON COLUMN public.sys_session.is_revoked IS '是否已主动撤销：true 后即使令牌未过期也不可用 [@dynamic: exact, in @hideable]';
COMMENT ON COLUMN public.sys_session.created_by IS '创建人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_session.created_at IS '创建时间 [@dynamic: exact, in, range @sortable @hideable]';
COMMENT ON COLUMN public.sys_session.updated_by IS '更新人 [@dynamic: exact, in]';
COMMENT ON COLUMN public.sys_session.updated_at IS '更新时间 [@dynamic: range @sortable]';
