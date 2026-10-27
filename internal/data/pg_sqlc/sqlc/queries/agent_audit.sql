-- Agent 业务审计固定命名查询：两表均为追加写
-- （append-only），只有写入与读取，没有更新/删除面。租户边界双重声明：
-- RLS（schema/95_agent_audit.sql 的策略）为主，读侧 WHERE 再显式带上
-- 同源的租户谓词作应用层兜底——RLS 被关闭（误操作/降级部署）时读取
-- 仍不越租户，对齐向量层「隔离正确性不押在 RLS 是否存在上」的口径。
-- 写入侧租户一律取会话 app.tenant_id（IdentityTx 注入），调用方无法
-- 指定或伪造归属。

-- 写入一条工具调用审计；租户取会话 app.tenant_id
-- name: InsertAgentToolCallLog :one
INSERT INTO public.agent_tool_call_log (tenant_id, user_id, trace_id, session_id,
                                        tool_call_id, tool_name, arguments_summary,
                                        result_summary, retry_count, duration_ms,
                                        status, created_by)
VALUES (current_setting('app.tenant_id'), sqlc.arg('user_id'), sqlc.narg('trace_id'),
        sqlc.narg('session_id'), sqlc.arg('tool_call_id'), sqlc.arg('tool_name'),
        sqlc.narg('arguments_summary'), sqlc.narg('result_summary'),
        sqlc.arg('retry_count'), sqlc.narg('duration_ms'), sqlc.arg('status'),
        sqlc.arg('user_id'))
RETURNING *;

-- 写入一条授权确认审计；租户取会话 app.tenant_id，写入者与用户同值
-- name: InsertAgentConfirmationLog :one
INSERT INTO public.agent_confirmation_log (tenant_id, user_id, trace_id, confirmation_id,
                                           session_id, tool_call_id, tool_name, decision,
                                           remember, path, decided_by, created_by)
VALUES (current_setting('app.tenant_id'), sqlc.arg('user_id'), sqlc.narg('trace_id'),
        sqlc.arg('confirmation_id'), sqlc.arg('session_id'), sqlc.narg('tool_call_id'),
        sqlc.arg('tool_name'), sqlc.arg('decision'), sqlc.narg('remember'),
        sqlc.arg('path'), sqlc.arg('decided_by'), sqlc.arg('user_id'))
RETURNING *;

-- 按会话过滤分页查询工具调用审计（时间倒序）；session_id 为 NULL 不过滤
-- name: ListAgentToolCallLogs :many
SELECT * FROM public.agent_tool_call_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'))
ORDER BY called_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- 按会话过滤统计工具调用审计条数（与 List 同条件，供分页信封）
-- name: CountAgentToolCallLogs :one
SELECT count(*) FROM public.agent_tool_call_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'));

-- 按会话过滤分页查询授权确认审计（时间倒序）；session_id 为 NULL 不过滤
-- name: ListAgentConfirmationLogs :many
SELECT * FROM public.agent_confirmation_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'))
ORDER BY decided_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- 按会话过滤统计授权确认审计条数（与 List 同条件，供分页信封）
-- name: CountAgentConfirmationLogs :one
SELECT count(*) FROM public.agent_confirmation_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'));

-- 按工具调用 ID 点查（租户谓词同源兜底，见文件头）；无行即不存在或跨租户
-- name: GetAgentToolCallLogByToolCallID :one
SELECT * FROM public.agent_tool_call_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND tool_call_id = sqlc.arg('tool_call_id');

-- 按确认 ID 点查（租户谓词同源兜底，见文件头）；无行即不存在或跨租户
-- name: GetAgentConfirmationLogByConfirmationID :one
SELECT * FROM public.agent_confirmation_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND confirmation_id = sqlc.arg('confirmation_id');
