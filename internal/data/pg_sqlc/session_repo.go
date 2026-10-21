package pg_sqlc

import (
	"context"
	"errors"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// sessionRepo 是 biz.SessionRepo 的 PostgreSQL 实现。签发/撤销经租户
// 会话门面执行（RLS 限租户）；校验/刷新在租户身份成立之前执行，经
// 令牌维度事务凭哈希由 RLS 放行命中的那一行。令牌哈希由 biz 计算，
// 仓储只存取哈希。
type sessionRepo struct {
	data *Data
}

// NewSessionRepo 构造会话仓储。
func NewSessionRepo(data *Data) biz.SessionRepo {
	return &sessionRepo{data: data}
}

// toBizSession 将持久化会话转换为领域模型。令牌哈希不随行：领域模型
// 不携带令牌的任何形态。
func toBizSession(po *gen.SysSession) *biz.Session {
	if po == nil {
		return nil
	}
	return &biz.Session{
		ID:               po.ID,
		Status:           int32(po.Status),
		UserID:           po.UserID,
		TenantID:         po.TenantID,
		ExpiresAt:        tsToTime(po.ExpiresAt),
		RefreshExpiresAt: tsToTimePtr(po.RefreshExpiresAt),
		LastActivityAt:   tsToTimePtr(po.LastActivityAt),
		DeviceInfo:       po.DeviceInfo,
		IPAddress:        po.IpAddress,
		IsRevoked:        po.IsRevoked,
		CreatedBy:        po.CreatedBy,
		UpdatedBy:        po.UpdatedBy,
		CreatedAt:        tsToTime(po.CreatedAt),
		UpdatedAt:        tsToTimePtr(po.UpdatedAt),
	}
}

// Issue 写入会话：租户取自门面的 app.tenant_id，审计列记所有者；主键
// 由应用层发号（发号能力目录落地前的过渡方案，同用户仓储）。
func (r *sessionRepo) Issue(ctx context.Context, owner biz.AuthContext, in *biz.IssueSessionInput) (*biz.Session, error) {
	refreshHash := in.RefreshTokenHash
	var out *biz.Session
	err := r.data.Session(ctx, pgAuth(owner), func(s *Session) error {
		row, err := s.Q().InsertSysSession(ctx, gen.InsertSysSessionParams{
			ID:                newID(),
			UserID:            owner.UserID,
			AccessTokenHash:   in.AccessTokenHash,
			RefreshTokenHash:  &refreshHash,
			AccessTtlSeconds:  ttlSeconds(in.AccessTTL),
			RefreshTtlSeconds: ttlSeconds(in.RefreshTTL),
			DeviceInfo:        in.DeviceInfo,
			IpAddress:         in.IPAddress,
			CreatedBy:         owner.UserID,
			UpdatedBy:         owner.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizSession(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Validate 按访问令牌哈希点查有效会话。租户身份尚未成立，经令牌维度
// 事务执行；有效条件在 SQL WHERE 内，无行即无效（不区分原因）。校验
// 是只读点查，不刷新 last_activity_at：避免每个请求一次写放大，活跃
// 时间由签发与刷新维护。
func (r *sessionRepo) Validate(ctx context.Context, accessTokenHash string) (*biz.Session, error) {
	var out *biz.Session
	err := r.data.TokenSession(ctx, TokenLookup{AccessTokenHash: accessTokenHash}, func(s *TokenSession) error {
		row, err := s.Q().GetActiveSysSessionByTokenHash(ctx, accessTokenHash)
		if err != nil {
			return err
		}
		out = toBizSession(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Revoke 撤销租户内的会话。已撤销或不可见的行更新 0 行，按幂等返回
// nil：登出重试不报错，也不泄露会话是否存在。
func (r *sessionRepo) Revoke(ctx context.Context, auth biz.AuthContext, id int64) error {
	return r.data.Session(ctx, pgAuth(auth), func(s *Session) error {
		_, err := s.Q().RevokeSysSession(ctx, gen.RevokeSysSessionParams{
			UpdatedBy: auth.UserID,
			ID:        id,
		})
		return err
	})
}

// Refresh 以当前刷新令牌哈希轮换令牌。并发轮换同一刷新令牌时，后到
// 者更新 0 行（旧哈希已失效），返回 ErrSessionInvalid。
func (r *sessionRepo) Refresh(ctx context.Context, in *biz.RefreshSessionInput) (*biz.Session, error) {
	currentRefreshHash := in.CurrentRefreshTokenHash
	newRefreshHash := in.NewRefreshTokenHash
	var out *biz.Session
	lookup := TokenLookup{
		RefreshTokenHash:    in.CurrentRefreshTokenHash,
		NewRefreshTokenHash: in.NewRefreshTokenHash,
	}
	err := r.data.TokenSession(ctx, lookup, func(s *TokenSession) error {
		row, err := s.Q().RotateSysSession(ctx, gen.RotateSysSessionParams{
			NewAccessTokenHash:      in.NewAccessTokenHash,
			NewRefreshTokenHash:     &newRefreshHash,
			AccessTtlSeconds:        ttlSeconds(in.AccessTTL),
			RefreshTtlSeconds:       ttlSeconds(in.RefreshTTL),
			CurrentRefreshTokenHash: &currentRefreshHash,
		})
		if err != nil {
			return err
		}
		out = toBizSession(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// mapErr 把无行（不存在/已撤销/已过期/已轮换）统一映射为
// ErrSessionInvalid，不向调用方区分具体原因。
func (r *sessionRepo) mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return biz.ErrSessionInvalid
	}
	return err
}

// ttlSeconds 把领域 TTL 映射为数据库区间秒数（秒级精度）。过期时刻
// 统一由数据库时钟起算，避免应用与存储的时钟/时区差异。
func ttlSeconds(d time.Duration) int32 {
	return int32(d / time.Second)
}
