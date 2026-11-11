-- Agent 模型调用留痕固定命名查询：表为追加写
-- （append-only），只有写入与读取，没有更新/删除面。租户边界双重声明：
-- RLS（schema/99_model_call_log.sql 的策略）为主，读侧 WHERE 再
-- 显式带同源的租户谓词作应用层兜底——RLS 被关闭（误操作/降级部署）时
-- 读取仍不越租户，对齐检索留痕「隔离正确性不押在 RLS 是否存在上」的
-- 口径。写入侧租户一律取会话 app.tenant_id（IdentityTx 注入），调用方
-- 无法指定或伪造归属。

-- 写入一条模型调用留痕；租户取会话 app.tenant_id，写入者与用户同值
-- name: InsertAgentModelCallLog :one
INSERT INTO public.agent_model_call_log (tenant_id, user_id, trace_id, session_id,
                                         scenario, model_key, endpoint, document_id,
                                         input_summary, output_summary,
                                         input_tokens, output_tokens, duration_ms,
                                         status, error, created_by)
VALUES (current_setting('app.tenant_id'), sqlc.arg('user_id'), sqlc.narg('trace_id'),
        sqlc.narg('session_id'), sqlc.arg('scenario'), sqlc.narg('model_key'),
        sqlc.narg('endpoint'), sqlc.narg('document_id'),
        sqlc.narg('input_summary'), sqlc.narg('output_summary'),
        sqlc.narg('input_tokens'), sqlc.narg('output_tokens'),
        sqlc.narg('duration_ms'), sqlc.arg('status'), sqlc.narg('error'),
        sqlc.arg('user_id'))
RETURNING *;

-- 按场景/模型键/状态/文档/时间范围过滤分页查询模型调用留痕（时间倒序）；
-- NULL 参数不过滤
-- name: ListAgentModelCallLogs :many
SELECT * FROM public.agent_model_call_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('scenario')::varchar IS NULL OR scenario = sqlc.narg('scenario'))
  AND (sqlc.narg('model_key')::varchar IS NULL OR model_key = sqlc.narg('model_key'))
  AND (sqlc.narg('status')::varchar IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('document_id')::bigint IS NULL OR document_id = sqlc.narg('document_id'))
  AND (sqlc.narg('created_from')::timestamp IS NULL OR created_at >= sqlc.narg('created_from'))
  AND (sqlc.narg('created_to')::timestamp IS NULL OR created_at <= sqlc.narg('created_to'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- 按场景/模型键/状态/文档/时间范围过滤统计模型调用留痕条数（与 List 同
-- 条件，供分页信封）
-- name: CountAgentModelCallLogs :one
SELECT count(*) FROM public.agent_model_call_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('scenario')::varchar IS NULL OR scenario = sqlc.narg('scenario'))
  AND (sqlc.narg('model_key')::varchar IS NULL OR model_key = sqlc.narg('model_key'))
  AND (sqlc.narg('status')::varchar IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('document_id')::bigint IS NULL OR document_id = sqlc.narg('document_id'))
  AND (sqlc.narg('created_from')::timestamp IS NULL OR created_at >= sqlc.narg('created_from'))
  AND (sqlc.narg('created_to')::timestamp IS NULL OR created_at <= sqlc.narg('created_to'));
