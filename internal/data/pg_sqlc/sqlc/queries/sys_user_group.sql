-- 用户组固定 CRUD（sys_user_group）与成员映射读写（sys_user_group_mapping）。
-- 用户组是租户内资源：表无 @scope，租户边界由 RLS 承担，行级无范围；
-- 动态查询能力由 schema/30_user.sql 的列注释 DSL 声明，本文件只写固定
-- 命名查询。成员映射不单独立资源，随宿主用户组的管理面维护：映射行无可
-- 变业务字段，移除成员走物理删除；映射表无外键级联，组软删后映射行物理
-- 残留，消费侧（权限推导函数与成员列表）都以组表/用户表活跃过滤联结，
-- 残留行不产生可见副作用。

-- 按 id 读取未软删的用户组（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysUserGroupByID :one
SELECT * FROM public.sys_user_group
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 按 id 锁定未软删的用户组（FOR UPDATE，写路径先锁后写；行级无 @scope，
-- 租户边界由 RLS 承担）
-- name: LockSysUserGroupByID :one
SELECT * FROM public.sys_user_group
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
FOR UPDATE;

-- 插入用户组记录；租户取会话 app.tenant_id
-- name: InsertSysUserGroup :one
INSERT INTO public.sys_user_group (id, tenant_id, status, "name", code, remark,
                                   owner_user_id, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('status'), sqlc.arg('name'),
        sqlc.arg('code'), sqlc.narg('remark'), sqlc.narg('owner_user_id'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 更新可变字段（name/status/remark；不含 code 与 owner_user_id——身份与
-- 归属列不经本路径变更）；updated_at 由触发器刷新
-- name: UpdateSysUserGroup :one
UPDATE public.sys_user_group
SET "name"     = sqlc.arg('name'),
    status     = sqlc.arg('status'),
    remark     = sqlc.narg('remark'),
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL
RETURNING *;

-- 置位 deleted_at 软删除用户组（成员映射行物理残留，消费侧活跃过滤排除）
-- name: SoftDeleteSysUserGroup :execrows
UPDATE public.sys_user_group
SET deleted_at = CURRENT_TIMESTAMP, updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 分页返回组成员（联结用户表展示字段；按添加时间升序、user_id 升序稳定
-- 排序）。已软删用户不出现在结果中（残留映射行被联结过滤）
-- name: ListSysUserGroupMembers :many
SELECT m.user_id,
       u."name",
       u.nickname,
       u.status,
       m.created_by AS added_by,
       m.created_at AS added_at
FROM public.sys_user_group_mapping m
JOIN public.sys_user u ON u.id = m.user_id AND u.deleted_at IS NULL
WHERE m.group_id = sqlc.arg('group_id')
ORDER BY m.created_at, m.user_id
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- 统计组成员数（与 List 同条件，供分页信封）
-- name: CountSysUserGroupMembers :one
SELECT count(*)
FROM public.sys_user_group_mapping m
JOIN public.sys_user u ON u.id = m.user_id AND u.deleted_at IS NULL
WHERE m.group_id = sqlc.arg('group_id');

-- 统计候选成员中租户内未软删用户数（添加成员前的存在性判定：命中数与
-- 请求数不等即存在未知用户，整批拒绝不写入；跨租户用户经 RLS 不命中，
-- 等同不存在）
-- name: CountSysUserGroupMemberCandidates :one
SELECT count(*)
FROM public.sys_user
WHERE id = ANY (sqlc.arg('user_ids')::int8[])
  AND deleted_at IS NULL;

-- 批量写入成员映射；租户取会话 app.tenant_id，重复添加跳过
-- name: AddSysUserGroupMembers :execrows
INSERT INTO public.sys_user_group_mapping (group_id, user_id, tenant_id, created_by)
SELECT sqlc.arg('group_id'), r, current_setting('app.tenant_id'), sqlc.arg('created_by')
FROM unnest(sqlc.arg('user_ids')::int8[]) AS r
ON CONFLICT (group_id, user_id) DO NOTHING;

-- 批量物理删除成员映射；移除非成员更新 0 行（幂等）
-- name: RemoveSysUserGroupMembers :execrows
DELETE FROM public.sys_user_group_mapping
WHERE group_id = sqlc.arg('group_id')
  AND user_id = ANY (sqlc.arg('user_ids')::int8[]);
