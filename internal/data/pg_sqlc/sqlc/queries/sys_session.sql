-- sys_session 会话表固定操作：令牌只以 SHA-256 哈希出入（明文只在
-- biz 用例内存中短暂存在）。签发与撤销经租户会话门面执行；校验与
-- 刷新发生在租户身份成立之前，以令牌哈希作为 RLS 可见性凭据（见
-- rls.sql 的 sys_session_token_* 策略）。

-- 签发会话：租户取自会话 app.tenant_id；status/created_at/updated_at
-- 由列默认值生成，last_activity_at 从签发时刻起算。有效期按 TTL 在
-- 数据库时钟上起算，与应用时钟/时区解耦
-- name: InsertSysSession :one
INSERT INTO public.sys_session (id, tenant_id, user_id, token_hash, refresh_token_hash,
                                expires_at, refresh_expires_at, last_activity_at,
                                device_info, ip_address, created_by, updated_by)
VALUES (sqlc.arg('id'), current_setting('app.tenant_id'), sqlc.arg('user_id'),
        sqlc.arg('access_token_hash'), sqlc.arg('refresh_token_hash'),
        CURRENT_TIMESTAMP + make_interval(secs => sqlc.arg('access_ttl_seconds')::int),
        CURRENT_TIMESTAMP + make_interval(secs => sqlc.arg('refresh_ttl_seconds')::int),
        CURRENT_TIMESTAMP,
        sqlc.narg('device_info'), sqlc.narg('ip_address'),
        sqlc.arg('created_by'), sqlc.arg('updated_by'))
RETURNING *;

-- 按访问令牌哈希校验会话：有效性条件（启用、未撤销、未过期）放在
-- SQL WHERE，租户身份尚未成立时 RLS 只放行哈希命中的行。不存在、
-- 已撤销、已过期一视同仁——无行即无效，不泄露会话状态
-- name: GetActiveSysSessionByTokenHash :one
SELECT * FROM public.sys_session
WHERE token_hash = sqlc.arg('access_token_hash')
  AND status = 1
  AND is_revoked = false
  AND expires_at > CURRENT_TIMESTAMP;

-- 撤销租户内的一条会话（登出）：已撤销或不可见的行更新 0 行，调用方
-- 按幂等处理；updated_by 记操作者
-- name: RevokeSysSession :execrows
UPDATE public.sys_session
SET is_revoked = true,
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND is_revoked = false;

-- 刷新令牌轮换：以当前刷新令牌哈希点更新，命中行必须启用、未撤销且
-- 刷新令牌未过期（条件放 SQL WHERE，并发轮换时后到者更新 0 行）。
-- 新旧令牌哈希均由 biz 计算；有效期按新 TTL 在数据库时钟上重新起算；
-- 操作者是会话所属用户，updated_at 由触发器兜底刷新
-- name: RotateSysSession :one
UPDATE public.sys_session
SET token_hash         = sqlc.arg('new_access_token_hash'),
    refresh_token_hash = sqlc.arg('new_refresh_token_hash'),
    expires_at         = CURRENT_TIMESTAMP + make_interval(secs => sqlc.arg('access_ttl_seconds')::int),
    refresh_expires_at = CURRENT_TIMESTAMP + make_interval(secs => sqlc.arg('refresh_ttl_seconds')::int),
    last_activity_at   = CURRENT_TIMESTAMP,
    updated_by         = user_id
WHERE refresh_token_hash = sqlc.arg('current_refresh_token_hash')
  AND status = 1
  AND is_revoked = false
  AND refresh_expires_at > CURRENT_TIMESTAMP
RETURNING *;

-- 过期会话清理（介质维护路径）：以池直连执行，不经租户/令牌事务门面、
-- 不注入 app.tenant_id——维护任务没有租户身份，跨租户资格由被调函数
-- 的 SECURITY DEFINER 属主决定（见 schema/80_session.sql 的
-- fn_cleanup_expired_sessions）。宽限期语义也收在函数内
-- name: CleanupExpiredSysSessions :one
SELECT public.fn_cleanup_expired_sessions(sqlc.arg('grace_seconds')::bigint) AS deleted;
