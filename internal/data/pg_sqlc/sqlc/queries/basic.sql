-- basic 业务表固定操作：归属转移链路的锁行读取与落库。
-- 业务表的固定 CRUD 一资源一文件（如 queries/sys_user.sql），随资源
-- 纵切补入；动态 List 由 sqlc_gen_dynamic 逐表生成，无需手写。
-- 按 id 锁定并读取一行 basic（FOR UPDATE）：改归属前先拿到它
-- 现在的归属值，防止并发修改后按旧值判断
-- name: GetBasicForUpdate :one
SELECT *
FROM public.basic
WHERE id = sqlc.arg('id')
FOR UPDATE;

-- 修改一行 basic 的归属（判断通过后调用）。归属参数传 NULL 表示
-- 清掉该归属；updated_at 由触发器自动刷新，不用手动设置
-- name: UpdateBasicOwner :exec
UPDATE public.basic
SET owner_user_id       = sqlc.narg('owner_user'),
    owner_dept_id       = sqlc.narg('owner_dept'),
    owner_user_group_id = sqlc.narg('owner_group'),
    updated_by          = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id');
