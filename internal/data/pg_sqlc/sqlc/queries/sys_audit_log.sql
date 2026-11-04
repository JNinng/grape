-- 审计日志固定点查（sys_login_log / sys_operation_log）。两表 append-only
-- 只读：无写入路径（日志行由业务现场写入）、无更新/删除面，动态查询能力
-- 由 schema/70_audit.sql 的列注释 DSL 声明（ParseSysLoginLogQuery 等顶层
-- 函数为生成物），本文件只写管理面的按 id 点查。两表是租户内公共资源：
-- 表无 @scope，租户边界由 RLS 承担，行级无范围。

-- 按 id 读取单条登录日志（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysLoginLogByID :one
SELECT * FROM public.sys_login_log
WHERE id = sqlc.arg('id');

-- 按 id 读取单条操作日志（含操作前后数据快照；租户边界由 RLS 承担）
-- name: GetSysOperationLogByID :one
SELECT * FROM public.sys_operation_log
WHERE id = sqlc.arg('id');
