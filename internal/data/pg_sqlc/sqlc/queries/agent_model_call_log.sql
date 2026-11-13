-- Agent 模型调用留痕固定命名查询：表为追加写
-- （append-only），只有写入，没有更新/删除面。租户边界双重声明：
-- RLS（schema/99_model_call_log.sql 的策略）为主，写入侧租户一律取会话
-- app.tenant_id（IdentityTx 注入），调用方无法指定或伪造归属。动态查询
-- 能力由 schema/99_model_call_log.sql 的列注释 DSL 声明（系统设置
-- 「模型调用」管理页消费；ParseAgentModelCallLogQuery 等顶层函数为
-- 生成物）。

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
