-- ============================================================
-- agent_session.sql — Agent 会话存储（trpc-agent-go session/postgres）
--
-- 来源基线：trpc-agent-go v1.11.2 session/postgres 的运行期建表模板
-- （internal/session/sqldb 表名 + session/postgres/init.go 的表/索引
-- DDL；包内 schema.sql 缺 session_track_events 表，属上游文档滞后，
-- 以 init.go 为准）。框架表共六张，经 WithTablePrefix("agent_") +
-- WithSchema("agent") 定位为 agent.agent_*；表结构须与框架
-- verifySchema 的期望逐列一致，勿擅自加列/改型。
--
-- 升级义务：框架版本升级时，以新版本 session/postgres 的建表模板
-- diff 本文件并同步修正（表、索引名规则 idx_{schema}_{前缀表}_{后缀}
-- 由框架内部 builder 计算，改名即断）；历史库的升级 diff 另行出迁移
-- 脚本，本文件只描述全新部署的终态。
--
-- 执行面与隔离模型：
--   - 超级用户执行面（CREATE SCHEMA + 建表授权），进 DeployFiles
--     单事务清单。幂等边界与向量文件（agent_vector.sql）同口径：
--     CREATE SCHEMA IF NOT EXISTS 幂等沿用，与本域另一建表文件谁先
--     执行无关、不因顺序互斥；表/索引沿用框架基线的 IF NOT EXISTS
--     文本以便 diff 对照，重复执行不报错、但也不修正已存在的结构，
--     框架升级的结构变更须按头注义务出迁移脚本，本文件只描述全新
--     部署的终态。
--   - 不加入 sqlc.yaml 输入：这是框架自管表，grape 侧无仓储与命名
--     查询（应用经 session.Service 接口读写）。
--   - 设施面例外（TODO 决策 1）：框架表无 RLS、无租户列，租户隔离
--     靠 userKey = tenant_id:user_id 的应用层编码实现；该 schema 不
--     存放 grape 租户业务数据，业务数据仍走 public + RLS。
--   - 授权只放 grape_u（应用运行角色）；test_u 与其他角色不自动放行，
--     需要时显式 GRANT（与 role.sql「其他角色所建表不自动放行」口径
--     一致）。
-- ============================================================

-- agent schema 是 agent 域设施的独立命名空间（向量检索表亦落此处）。
-- IF NOT EXISTS：schema 已存在即幂等沿用，本文件与 agent_vector.sql
-- 谁先执行无关；幂等边界仅及于此。
CREATE SCHEMA IF NOT EXISTS agent;

-- agent.agent_session_states — 会话元数据与状态
-- ack 的未决确认指针（pending map）持久化在 state JSONB 内。
CREATE TABLE IF NOT EXISTS agent.agent_session_states (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    user_id    VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    state      JSONB DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

-- 软删感知的唯一约束：同 app+user 只允许一个活跃同 ID 会话
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_agent_session_states_unique_active
ON agent.agent_session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;

-- TTL 清理索引：仅覆盖设置了过期时间的行
CREATE INDEX IF NOT EXISTS idx_agent_agent_session_states_expires
ON agent.agent_session_states(expires_at)
WHERE expires_at IS NOT NULL;

-- agent.agent_session_events — 会话事件流（压缩投影前的原文留档）
CREATE TABLE IF NOT EXISTS agent.agent_session_events (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    user_id    VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    event      JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_agent_session_events_lookup
ON agent.agent_session_events(app_name, user_id, session_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_agent_session_events_expires
ON agent.agent_session_events(expires_at)
WHERE expires_at IS NOT NULL;

-- agent.agent_session_track_events — AG-UI 协议事件（/history 快照流）
CREATE TABLE IF NOT EXISTS agent.agent_session_track_events (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    user_id    VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    track      VARCHAR(255) NOT NULL,
    event      JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_agent_session_track_events_lookup
ON agent.agent_session_track_events(app_name, user_id, session_id, track, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_agent_session_track_events_expires
ON agent.agent_session_track_events(expires_at)
WHERE expires_at IS NOT NULL;

-- agent.agent_session_summaries — 会话摘要（支持分支摘要；当前未启用，
-- 表仍须预建：框架服务启动校验要求六表齐全）
CREATE TABLE IF NOT EXISTS agent.agent_session_summaries (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    user_id    VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    filter_key VARCHAR(255) NOT NULL DEFAULT '',
    summary    JSONB DEFAULT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_agent_session_summaries_unique_active
ON agent.agent_session_summaries(app_name, user_id, session_id, filter_key)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_agent_session_summaries_expires
ON agent.agent_session_summaries(expires_at)
WHERE expires_at IS NOT NULL;

-- agent.agent_app_states — 应用级 state（grape 不存租户业务数据，
-- 仅框架内部机制；CreateSession 路径会无条件读该表，表必须存在）
CREATE TABLE IF NOT EXISTS agent.agent_app_states (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    key        VARCHAR(255) NOT NULL,
    value      TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_agent_app_states_unique_active
ON agent.agent_app_states(app_name, key)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_agent_app_states_expires
ON agent.agent_app_states(expires_at)
WHERE expires_at IS NOT NULL;

-- agent.agent_user_states — 用户级 state
CREATE TABLE IF NOT EXISTS agent.agent_user_states (
    id         BIGSERIAL PRIMARY KEY,
    app_name   VARCHAR(255) NOT NULL,
    user_id    VARCHAR(255) NOT NULL,
    key        VARCHAR(255) NOT NULL,
    value      TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP DEFAULT NULL,
    deleted_at TIMESTAMP DEFAULT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_agent_user_states_unique_active
ON agent.agent_user_states(app_name, user_id, key)
WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_agent_user_states_expires
ON agent.agent_user_states(expires_at)
WHERE expires_at IS NOT NULL;

-- 运行授权：grape_u 对 agent schema 的使用与全套 DML/序列权限
-- （框架运行期含 TTL 清理删除路径，DELETE 必须放行）
GRANT USAGE ON SCHEMA agent TO grape_u;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA agent TO grape_u;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA agent TO grape_u;
