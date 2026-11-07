-- 审计日志点查与追加写入（sys_login_log / sys_operation_log）。两表
-- append-only：只有 INSERT 与按 id 点查，无更新/删除面；动态查询能力由
-- schema/70_audit.sql 的列注释 DSL 声明（ParseSysLoginLogQuery 等顶层函数
-- 为生成物）。两表是租户内公共资源：表无 @scope，租户边界由 RLS 承担，
-- 行级无范围。写入口径：登录日志由登录用例在身份成立前经 TenantTx 写入
--（审计列显式给 actor），操作日志由入口中间件在身份成立后经 IdentityTx
-- 写入（created_by 取认证身份）。

-- 按 id 读取单条登录日志（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysLoginLogByID :one
SELECT * FROM public.sys_login_log
WHERE id = sqlc.arg('id');

-- 追加一条登录日志。user_id/created_by 允许为 0（登录失败且目标用户
-- 不存在/不可定位时无归属）；login_at/created_at 走库默认值。
-- name: InsertSysLoginLog :exec
INSERT INTO public.sys_login_log
    (user_id, username, tenant_id, ip_address, user_agent, login_type, is_success, failure_reason, created_by)
VALUES (sqlc.arg('user_id'), sqlc.arg('username'), sqlc.arg('tenant_id'), sqlc.narg('ip_address'),
        sqlc.narg('user_agent'), sqlc.arg('login_type'), sqlc.arg('is_success'), sqlc.narg('failure_reason'),
        sqlc.arg('created_by'));

-- 按 id 读取单条操作日志（含操作前后数据快照；租户边界由 RLS 承担）
-- name: GetSysOperationLogByID :one
SELECT * FROM public.sys_operation_log
WHERE id = sqlc.arg('id');

-- 追加一条操作日志。快照列可空（写入方未采集即 NULL）；operated_at/
-- created_at 走库默认值。
-- name: InsertSysOperationLog :exec
INSERT INTO public.sys_operation_log
    (user_id, username, tenant_id, operation_type, target_table, target_id, old_value, new_value,
     ip_address, user_agent, api_path, http_method, duration_ms, result_code, result_msg, request_id, created_by)
VALUES (sqlc.arg('user_id'), sqlc.arg('username'), sqlc.arg('tenant_id'), sqlc.arg('operation_type'),
        sqlc.narg('target_table'), sqlc.narg('target_id'), sqlc.narg('old_value'), sqlc.narg('new_value'),
        sqlc.narg('ip_address'), sqlc.narg('user_agent'), sqlc.narg('api_path'), sqlc.narg('http_method'),
        sqlc.narg('duration_ms'), sqlc.narg('result_code'), sqlc.narg('result_msg'), sqlc.narg('request_id'),
        sqlc.arg('created_by'));
