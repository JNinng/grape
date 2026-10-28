-- ============================================================
-- 96_agent_grant.sql — Agent 工具授权存储
--
-- agent_tool_grant 表、租户 RLS 策略与运行角色授权。本文件进 sqlc
-- catalog（表为标准关系形态，命名查询见 queries/agent_grant.sql），
-- 由部署清单（deploy.go DeployFiles）以超级用户/表 owner 连接执行，
-- 排在 agent_vector.sql 之后（同尾部追加域，只依赖 role.sql 的
-- grape_u/test_u 存在，与 public 业务域、agent 设施域零耦合）。
--
-- RLS 落点口径：表在 public 业务域、带 tenant_id，租户隔离策略随本
-- 域文件声明而不进 rls.sql——与 expose.sql 承载 sys_perm_version 策略
-- 同一口径（域内对象策略随域文件走，rls.sql 只集纳基线租户表），避免
-- 各域在共享文件上互相牵动。ENABLE + FORCE 与基线表一致；缺
-- app.tenant_id 时 current_setting(...,true) 为 NULL，比较恒为假，
-- 默认全拒。
--
-- 应用层兜底口径（与向量层隔离同源）：仓储每条查询显式携带
-- tenant_id 参数（取自认证身份，不取请求侧输入），RLS 被关闭时跨租户
-- 仍不可见，隔离正确性不押在数据库策略是否存在上（集成测试以无 RLS
-- 场景断言）。
--
-- 行生命周期：仅「写入」与「吊销（置 revoked_at）」两种形态，无
-- updated_at/updated_by——吊销事实由 revoked_by/revoked_at 承载，行
-- 内容创建后不再变更；rls.sql 的 updated_at 兜底触发器按列扫描挂载，
-- 本表无该列，天然不在挂载清单内。TTL/过期清理的保留与执行口径见根目录
-- operations.md「保留期与归档统一口径」，不在本文件预置维护函数。
-- ============================================================

-- agent_tool_grant — Agent 工具授权（ack 许可存储）。查询键
-- (tenant_id, user_id, tool_name, session_id)：session_id 为 NULL 即
-- 全局级（对该用户全部会话生效），非空即会话级。未撤销行每键至多
-- 一条（部分唯一表达式索引，COALESCE 把全局级 NULL 归一为空串参与
-- 唯一性）；吊销不删行，同键可在吊销后重新授权。
CREATE TABLE public.agent_tool_grant
(
    id         int8                                   NOT NULL,
    tenant_id  varchar(16)                            NOT NULL,
    user_id    int8                                   NOT NULL,
    tool_name  varchar(128)                           NOT NULL,
    session_id varchar(128)                           NULL,
    created_by int8                                   NOT NULL,
    created_at timestamp(0) DEFAULT CURRENT_TIMESTAMP NOT NULL,
    revoked_by int8                                   NULL,
    revoked_at timestamp(0)                           NULL,
    CONSTRAINT agent_tool_grant_pkey PRIMARY KEY (id),
    CONSTRAINT agent_tool_grant_tool_name_chk CHECK (char_length(tool_name) > 0),
    CONSTRAINT agent_tool_grant_session_id_chk CHECK (session_id IS NULL OR char_length(session_id) > 0)
);

-- 未撤销唯一约束：同键（租户/用户/工具/作用域）至多一条活跃许可。
-- 表达式索引把全局级（session_id IS NULL）与会话级归一到同一比较域
-- ——NULL 在 PostgreSQL 唯一索引中彼此相异，直接对 session_id 建唯一
-- 索引放不住「全局级唯一」；空串哨兵由 COALESCE 在索引内承担，不落列。
CREATE UNIQUE INDEX agent_tool_grant_active_un
    ON public.agent_tool_grant USING btree (tenant_id, user_id, tool_name, COALESCE(session_id, ''))
    WHERE revoked_at IS NULL;

-- 管理列表扫描前缀：按租户（全租户视图）与租户+用户（本人视图）取
-- 活跃许可，排序键（created_at DESC, id DESC）与查询对齐。
CREATE INDEX idx_agent_tool_grant_tenant_user
    ON public.agent_tool_grant USING btree (tenant_id, user_id, created_at);

COMMENT ON TABLE public.agent_tool_grant IS 'Agent 工具授权（ack 许可存储）：用户对工具的预先授权，满足 confirm 层级的确认要求；查询键 (tenant_id, user_id, tool_name, session_id)，session_id 为 NULL 即全局级；未撤销行每键至多一条（部分唯一表达式索引）；吊销置 revoked_at 不删行。租户隔离 RLS + 应用层 tenant_id 参数双保险（与向量层隔离口径一致）';

COMMENT ON COLUMN public.agent_tool_grant.id IS '授权 ID（应用层发号）';
COMMENT ON COLUMN public.agent_tool_grant.tenant_id IS '租户 ID：仓储从认证 ctx 提取并显式过滤，不来自请求侧输入';
COMMENT ON COLUMN public.agent_tool_grant.user_id IS '被授权用户 ID（许可归属，即确认决定者本人）';
COMMENT ON COLUMN public.agent_tool_grant.tool_name IS '工具名（ack 分级表与工具集登记名）';
COMMENT ON COLUMN public.agent_tool_grant.session_id IS '绑定的会话（AG-UI threadId）；NULL 即全局级，对该用户全部会话生效';
COMMENT ON COLUMN public.agent_tool_grant.created_by IS '授权来源操作者（确认决定的 DecidedBy 解码，正常流即属主本人）';
COMMENT ON COLUMN public.agent_tool_grant.created_at IS '授权时间（数据库时钟）';
COMMENT ON COLUMN public.agent_tool_grant.revoked_by IS '吊销操作者：属主本人或持有 agent:grant:revoke 权限码的管理者';
COMMENT ON COLUMN public.agent_tool_grant.revoked_at IS '吊销时间；NULL 即许可有效，非 NULL 行保留作审计现场';

-- 租户隔离策略：与基线租户表同一条规则（口径说明见文件头）。
ALTER TABLE public.agent_tool_grant ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.agent_tool_grant FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_tool_grant_tenant_isolation
    ON public.agent_tool_grant
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- 运行角色授权：role.sql 的默认权限（ALTER DEFAULT PRIVILEGES）对本
-- 文件所建表同样生效，此处显式声明一份使授权面自文档化（与
-- agent_session.sql / agent_vector.sql 的收窄授权口径一致，不扩散到
-- 其他对象）。
GRANT SELECT, INSERT, UPDATE, DELETE ON public.agent_tool_grant TO grape_u, test_u;
