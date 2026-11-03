-- 按 id 读取未软删的部门（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysDeptByID :one
SELECT * FROM public.sys_dept
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的部门（FOR UPDATE，写路径先锁后写；行级无 @scope，
-- 租户边界由 RLS 承担）
-- name: LockSysDeptByID :one
SELECT * FROM public.sys_dept
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 读取租户内全部未软删部门（组织树装配与环检测的专用列表，租户规模内
-- 全量返回；created_at ASC、id ASC 稳定排序）
-- name: ListSysDeptAll :many
SELECT * FROM public.sys_dept
WHERE deleted_at IS NULL
ORDER BY created_at, id;

-- 统计部门未软删的直接子部门数（删除前置校验；触发器兜底同口径）
-- name: CountSysDeptActiveChildren :one
SELECT count(*) AS count
FROM public.sys_dept
WHERE parent_id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 插入部门记录；租户取会话 app.tenant_id。闭包路径由
-- trg_maintain_dept_closure 在同事务内自动建立（父级合法性一并由触发器
-- 校验兜底）
-- name: InsertSysDept :one
INSERT INTO public.sys_dept (id, tenant_id, status, "name", parent_id, owner_user_id,
                             owner_user_group_id, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.narg('parent_id'), sqlc.arg('owner_user_id'), sqlc.narg('owner_user_group_id'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status；不含 parent_id 与 owner_*——结构与归属变更
-- 走专项口径）；updated_at 由触发器刷新
-- name: UpdateSysDept :one
UPDATE public.sys_dept
SET "name"     = sqlc.arg('name'),
    status     = sqlc.arg('status'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 移动部门到新父级；parent_id 为 NULL 表示移为根部门。闭包路径由
-- trg_maintain_dept_closure 在同事务内重写（父级合法性/防环由触发器
-- 校验兜底，调用方已在用例层预检）
-- name: MoveSysDept :one
UPDATE public.sys_dept
SET parent_id   = sqlc.narg('parent_id'),
    updated_by  = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除部门；存在未软删直接子部门时由触发器拒绝
--（仓储先经 CountSysDeptActiveChildren 前置校验映射为类型化错误）
-- name: SoftDeleteSysDept :execrows
UPDATE public.sys_dept
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;
