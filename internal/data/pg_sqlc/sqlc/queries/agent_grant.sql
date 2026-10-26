-- 写入一条工具授权；租户与身份取认证 ctx（IdentityTx 注入），同键活跃
-- 许可已存在时幂等跳过（部分唯一索引 agent_tool_grant_active_un，
-- ON CONFLICT 不带目标以覆盖表达式部分索引），返回实际写入行数
-- name: InsertAgentToolGrant :execrows
INSERT INTO public.agent_tool_grant (id, tenant_id, user_id, tool_name, session_id, created_by)
VALUES (sqlc.arg('id'), sqlc.arg('tenant_id'), sqlc.arg('user_id'), sqlc.arg('tool_name'),
        sqlc.narg('session_id'), sqlc.arg('created_by'))
ON CONFLICT DO NOTHING;

-- 会话级活跃许可：许可评估第一优先（会话级先于全局级，次序由用例层
-- 编排）；tenant_id 显式过滤是 RLS 之外的应用层兜底（决策 6/D1 口径）
-- name: GetActiveAgentToolGrantForSession :one
SELECT * FROM public.agent_tool_grant
WHERE tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND tool_name = sqlc.arg('tool_name')
  AND session_id = sqlc.arg('session_id')
  AND revoked_at IS NULL;

-- 全局级活跃许可：会话级未命中后的兜底；session_id IS NULL 即全局级
-- name: GetActiveAgentToolGrantGlobal :one
SELECT * FROM public.agent_tool_grant
WHERE tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND tool_name = sqlc.arg('tool_name')
  AND session_id IS NULL
  AND revoked_at IS NULL;

-- 按租户内 id 取授权行（含已吊销，供吊销前归属判定）；跨租户等同不存在
-- name: GetAgentToolGrantByID :one
SELECT * FROM public.agent_tool_grant
WHERE id = sqlc.arg('id')
  AND tenant_id = sqlc.arg('tenant_id');

-- 指定用户的活跃授权列表（本人视图），新授权在前；id 为同秒 tie-break
-- name: ListActiveAgentToolGrantsByUser :many
SELECT * FROM public.agent_tool_grant
WHERE tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND revoked_at IS NULL
ORDER BY created_at DESC, id DESC;

-- 租户内全部用户的活跃授权列表（管理视图，入口侧验 agent:grant:query）
-- name: ListActiveAgentToolGrantsByTenant :many
SELECT * FROM public.agent_tool_grant
WHERE tenant_id = sqlc.arg('tenant_id')
  AND revoked_at IS NULL
ORDER BY created_at DESC, id DESC;

-- 属主吊销：id + 归属人双定位（非本人不可经本查询触达），原子置
-- revoked_at；已吊销、不存在或非本人更新 0 行
-- name: RevokeAgentToolGrantSelf :execrows
UPDATE public.agent_tool_grant
SET revoked_at = CURRENT_TIMESTAMP,
    revoked_by = sqlc.arg('revoked_by')
WHERE id = sqlc.arg('id')
  AND tenant_id = sqlc.arg('tenant_id')
  AND user_id = sqlc.arg('user_id')
  AND revoked_at IS NULL;

-- 管理吊销：调用方（GrantUsecase.Revoke）已验 agent:grant:revoke 权限
-- 码；已吊销或不存在更新 0 行
-- name: RevokeAgentToolGrantManaged :execrows
UPDATE public.agent_tool_grant
SET revoked_at = CURRENT_TIMESTAMP,
    revoked_by = sqlc.arg('revoked_by')
WHERE id = sqlc.arg('id')
  AND tenant_id = sqlc.arg('tenant_id')
  AND revoked_at IS NULL;
