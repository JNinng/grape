-- Agent 检索留痕（TODO 流 D 的 D3）固定命名查询：表为追加写
-- （append-only），只有写入与读取，没有更新/删除面。租户边界双重声明：
-- RLS（schema/97_agent_retrieval_log.sql 的策略）为主，读侧 WHERE 再
-- 显式带同源的租户谓词作应用层兜底——RLS 被关闭（误操作/降级部署）时
-- 读取仍不越租户，对齐 D1「隔离正确性不押在 RLS 是否存在上」的验收
-- 口径。写入侧租户一律取会话 app.tenant_id（IdentityTx 注入），调用方
-- 无法指定或伪造归属。

-- 写入一条检索留痕；租户取会话 app.tenant_id，写入者与用户同值
-- name: InsertAgentRetrievalLog :one
INSERT INTO public.agent_retrieval_log (tenant_id, user_id, trace_id, session_id,
                                        source, query_text, requested_top_k,
                                        effective_top_k, filter_summary, hits,
                                        rerank_before, rerank_after, duration_ms,
                                        status, created_by)
VALUES (current_setting('app.tenant_id'), sqlc.arg('user_id'), sqlc.narg('trace_id'),
        sqlc.narg('session_id'), sqlc.arg('source'), sqlc.narg('query_text'),
        sqlc.narg('requested_top_k'), sqlc.narg('effective_top_k'),
        sqlc.narg('filter_summary'), sqlc.narg('hits'), sqlc.narg('rerank_before'),
        sqlc.narg('rerank_after'), sqlc.narg('duration_ms'), sqlc.arg('status'),
        sqlc.arg('user_id'))
RETURNING *;

-- 按会话/来源过滤分页查询检索留痕（时间倒序）；NULL 参数不过滤
-- name: ListAgentRetrievalLogs :many
SELECT * FROM public.agent_retrieval_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'))
  AND (sqlc.narg('source')::varchar IS NULL OR source = sqlc.narg('source'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- 按会话/来源过滤统计检索留痕条数（与 List 同条件，供分页信封）
-- name: CountAgentRetrievalLogs :one
SELECT count(*) FROM public.agent_retrieval_log
WHERE tenant_id = current_setting('app.tenant_id', true)
  AND (sqlc.narg('session_id')::varchar IS NULL OR session_id = sqlc.narg('session_id'))
  AND (sqlc.narg('source')::varchar IS NULL OR source = sqlc.narg('source'));
