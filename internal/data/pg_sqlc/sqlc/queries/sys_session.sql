-- 插入会话记录；租户取会话 app.tenant_id，有效期按 TTL 从数据库时钟起算
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

-- 按访问令牌哈希读取启用、未撤销、未过期的会话；无行即无效
-- name: GetActiveSysSessionByTokenHash :one
SELECT * FROM public.sys_session
WHERE token_hash = sqlc.arg('access_token_hash')
  AND status = 1
  AND is_revoked = false
  AND expires_at > CURRENT_TIMESTAMP;

-- 撤销指定会话；已撤销时更新 0 行
-- name: RevokeSysSession :execrows
UPDATE public.sys_session
SET is_revoked = true,
    updated_by = sqlc.arg('updated_by')
WHERE id = sqlc.arg('id')
  AND is_revoked = false;

-- 登出：按访问令牌哈希撤销所属会话。令牌哈希是身份成立前的秘密凭据，
-- 持有即授权撤销命中的那一行（RLS 见 sys_session_token_revoke 策略），
-- 走令牌维度事务执行。会话级撤销同时使该会话的刷新令牌失效（轮换
-- 查询要求未撤销）。已撤销、已删除或令牌不匹配时更新 0 行——登出
-- 幂等，重放与未知令牌都不报错、不泄露会话是否存在；updated_by 记
-- 会话用户：令牌维度事务无调用者身份，令牌持有者作用于会话属主自己
-- 的会话（与轮换的审计归属一致）。
-- name: RevokeSysSessionByTokenHash :execrows
UPDATE public.sys_session
SET is_revoked = true,
    updated_by = user_id
WHERE token_hash = sqlc.arg('access_token_hash')
  AND is_revoked = false;

-- 按当前刷新令牌哈希轮换令牌；仅命中启用、未撤销且未过期的会话，
-- 未命中时更新 0 行
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

-- 删除已撤销或过期超过宽限期的会话，返回删除行数
-- name: CleanupExpiredSysSessions :one
SELECT public.fn_cleanup_expired_sessions(sqlc.arg('grace_seconds')::bigint) AS deleted;
