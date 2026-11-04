-- 菜单固定读查询与受控写调用（sys_menu）。sys_menu 是功能权限码链的
-- 宿主树：读面为租户内普通读（表无 @scope，租户边界由 RLS 承担，行级
-- 无范围）；写面运行角色无直写权限（role.sql 对 sys_menu REVOKE 写授权，
-- 本文件不动），全部写入经 98_menu_write.sql 的 SECURITY DEFINER 固定
-- 口径写函数（租户边界复位、父级校验、防环、软删口径收在函数内）——
-- 写查询即调用受控函数的 SELECT，事务纪律与普通写一致（调用方事务每
-- 事务设 app.tenant_id，函数内读取并落值）。动态查询能力由
-- schema/40_rbac.sql 的列注释 DSL 声明（ParseSysMenuQuery 等顶层函数为
-- 生成物），树查询走本文件的专用列表（菜单树全量装配，不做分页）。

-- 按 id 读取未软删的菜单（租户边界由 RLS 承担；表无 @scope，租户内全可见）
-- name: GetSysMenuByID :one
SELECT *
FROM public.sys_menu
WHERE id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 读取租户内全部未软删菜单（菜单树装配与环检测的专用列表，租户规模内
-- 全量返回；sort_order ASC、id ASC 稳定排序，与表注释 @default_sort 一致）
-- name: ListSysMenuAll :many
SELECT *
FROM public.sys_menu
WHERE deleted_at IS NULL
ORDER BY sort_order, id;

-- 统计菜单未软删的直接子菜单数（删除前置校验；受控写函数兜底同口径）
-- name: CountSysMenuActiveChildren :one
SELECT count(*) AS count
FROM public.sys_menu
WHERE parent_id = sqlc.arg('id')
  AND deleted_at IS NULL;

-- 新建菜单：调用受控写函数 fn_menu_create（SECURITY DEFINER 固定口径：
-- 函数内以 app.tenant_id 复位租户边界并校验父节点）；租户不取请求值，
-- 由函数内会话变量落值
-- name: InsertSysMenu :one
SELECT *
FROM public.fn_menu_create(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('name'),
                           sqlc.arg('permission_code'), sqlc.narg('parent_id'),
                           sqlc.arg('menu_type'), sqlc.narg('icon'), sqlc.narg('route_path'),
                           sqlc.narg('component'), sqlc.narg('permission_expr'),
                           sqlc.arg('sort_order'), sqlc.narg('remark'), sqlc.arg('operator'));

-- 覆盖可变字段（status/name/icon/route_path/component/permission_expr/
-- sort_order/remark）：调用受控写函数 fn_menu_update（身份/类型/结构列
-- permission_code/menu_type/parent_id 不经本路径变更）；目标未命中函数
-- 返回 NULL 复合行，以 id IS NOT NULL 过滤成空集（映射 NotFound）；
-- updated_at 由触发器刷新
-- name: UpdateSysMenu :one
SELECT *
FROM public.fn_menu_update(sqlc.arg('id'), sqlc.arg('status'), sqlc.arg('name'),
                           sqlc.narg('icon'), sqlc.narg('route_path'), sqlc.narg('component'),
                           sqlc.narg('permission_expr'), sqlc.arg('sort_order'),
                           sqlc.narg('remark'), sqlc.arg('operator'))
WHERE id IS NOT NULL;

-- 移动菜单到新父级（parent_id 为 NULL 表示移为根）：调用受控写函数
-- fn_menu_move（父级合法性/防环由函数内校验兜底，调用方已在用例层预检）；
-- 目标未命中函数返回 NULL 复合行，以 id IS NOT NULL 过滤成空集
--（映射 NotFound）
-- name: MoveSysMenu :one
SELECT *
FROM public.fn_menu_move(sqlc.arg('id'), sqlc.narg('parent_id'), sqlc.arg('operator'))
WHERE id IS NOT NULL;

-- 置位 deleted_at 软删除菜单：调用受控写函数 fn_menu_delete（存在未软删
-- 直接子菜单时函数内拒绝）；返回删除行数，0 即目标未命中（映射 NotFound）
-- name: SoftDeleteSysMenu :one
SELECT public.fn_menu_delete(sqlc.arg('id'), sqlc.arg('operator')) AS deleted_count;
