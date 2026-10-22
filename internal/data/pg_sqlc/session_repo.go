package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// sessionRepo 是 biz.SessionRepo 的 PostgreSQL 实现。签发/撤销经身份
// 事务门面 IdentityTx 执行（RLS 限租户）；校验/刷新在租户身份成立之前
// 执行，经 TokenTx 凭哈希由 RLS 放行命中的那一行。令牌哈希由 biz 计
// 算，仓储只存取哈希。
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
func (r *sessionRepo) Issue(ctx context.Context, owner biz.Identity, in *biz.IssueSessionInput) (*biz.Session, error) {
	refreshHash := in.RefreshTokenHash
	var out *biz.Session
	err := r.data.IdentityTx(ctx, pgAuth(owner), func(s *IdentityTx) error {
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
	err := r.data.TokenTx(ctx, TokenLookup{AccessTokenHash: accessTokenHash}, func(s *TokenTx) error {
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
func (r *sessionRepo) Revoke(ctx context.Context, auth biz.Identity, id int64) error {
	return r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
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
	err := r.data.TokenTx(ctx, lookup, func(s *TokenTx) error {
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

// sessionRepo 实现 biz.FacilityMaintenance：过期会话清理与索引膨胀
// 维护不进会话存取接口（SessionRepo 注释已预告该设计），由介质侧维护
// 面承担，判定（宽限期/膨胀阈值）全部下沉在这里。
var _ biz.FacilityMaintenance = (*sessionRepo)(nil)

// 会话介质维护判定常量：介质内部知识，维护任务与 biz 会话接口不感知。
const (
	// sessionCleanupGrace 是过期会话的删除宽限期。访问令牌 TTL 只有
	// 30 分钟量级，留足在途请求与审计缓冲：宽限期内的已过期令牌仍可能
	// 出现在最后一次合法请求的日志里，删早了审计无从对账。
	sessionCleanupGrace = 24 * time.Hour

	// REINDEX 判定阈值：token_hash 唯一索引是登录/校验路径的每请求
	// 点查主索引，清理产生的死元组让它持续膨胀。绝对下限 10000 防小表
	// 抖动；死元组占比 20% 是"膨胀已影响扫描效率"的常见经验线。两条件
	// 同时满足才 REINDEX；判定放在清理之后——本轮删除也计入死元组，
	// 绝对下限同时防误判刚清完的表立刻重建索引。
	reindexMinDeadTuples = 10000
	reindexDeadRatio     = 0.2

	// sessionTokenHashIndex 是 token_hash 的唯一索引名（见
	// schema/80_session.sql），REINDEX 的唯一目标。
	sessionTokenHashIndex = "sys_session_token_hash_un"
)

// NewSessionMaintenance 构造会话介质的维护面。与仓储共享连接池、
// 独立实例：维护是与会话存取互不感知的另一条路径。
func NewSessionMaintenance(d *Data) biz.FacilityMaintenance {
	return &sessionRepo{data: d}
}

// Facility 返回设施名。
func (*sessionRepo) Facility() string { return "postgres" }

// Maintain 执行一轮会话介质维护，动作顺序先删后判：
//
//  1. 清理过期/已撤销会话：池直连调用 SECURITY DEFINER 清理函数
//     （sys_session 启用 ENABLE+FORCE 租户 RLS，维护无租户身份，跨
//     租户资格由函数属主承担），绝不走 IdentityTx/TokenTx/TenantTx；
//  2. 按 pg_stat_user_tables 死元组统计判定是否 REINDEX token_hash
//     索引。
func (r *sessionRepo) Maintain(ctx context.Context) (biz.MaintenanceReport, error) {
	report := biz.MaintenanceReport{}

	deleted, err := gen.New(r.data.db).
		CleanupExpiredSysSessions(ctx, int64(sessionCleanupGrace/time.Second))
	if err != nil {
		return report, fmt.Errorf("pg_sqlc: cleanup expired sessions: %w", err)
	}
	report.Actions = append(report.Actions, biz.MaintenanceAction{
		Kind:      "delete_expired_sessions",
		Target:    "public.sys_session",
		Rows:      deleted,
		Performed: true,
	})

	action, err := r.reindexTokenHashIndex(ctx)
	if err != nil {
		return report, err
	}
	report.Actions = append(report.Actions, action)
	return report, nil
}

// reindexTokenHashIndex 按死元组统计判定是否 REINDEX token_hash 索引。
// REINDEX CONCURRENTLY 不能在事务块/函数内执行，必须以池直连的顶层
// 独立语句运行；取裸连接走简单协议（PgConn.Exec），绕开 pgx 扩展协议
// 对事务块的包装。属主现实：表/索引属主是部署超级用户，应用角色大概率
// 42501 insufficient_privilege——按跳过处理并留痕，不视为错误；其余
// 错误向上返回。
func (r *sessionRepo) reindexTokenHashIndex(ctx context.Context) (biz.MaintenanceAction, error) {
	action := biz.MaintenanceAction{Kind: "reindex", Target: sessionTokenHashIndex}

	var live, dead int64
	err := r.data.db.QueryRow(ctx,
		`SELECT n_live_tup, n_dead_tup
		 FROM pg_stat_user_tables
		 WHERE schemaname = 'public' AND relname = 'sys_session'`).
		Scan(&live, &dead)
	if errors.Is(err, pgx.ErrNoRows) {
		// 统计视图无该表（未部署/未访问）即无从判定膨胀，跳过不报错
		action.Note = "table stats unavailable"
		return action, nil
	}
	if err != nil {
		return action, fmt.Errorf("pg_sqlc: query sys_session tuple stats: %w", err)
	}
	// dead > 10000 蕴含 dead+live > 10000，占比计算无除零
	if dead <= reindexMinDeadTuples || float64(dead)/float64(dead+live) <= reindexDeadRatio {
		action.Note = "bloat below threshold"
		return action, nil
	}

	conn, err := r.data.db.Acquire(ctx)
	if err != nil {
		return action, fmt.Errorf("pg_sqlc: acquire connection for reindex: %w", err)
	}
	defer conn.Release()
	_, err = conn.Conn().PgConn().Exec(ctx,
		"REINDEX INDEX CONCURRENTLY public."+sessionTokenHashIndex).ReadAll()
	if err != nil {
		var pgErr *pgconn.PgError
		// 42501 insufficient_privilege：索引属主不是应用角色，重建归
		// 部署侧人工/运维任务，本轮按跳过处理
		if errors.As(err, &pgErr) && pgErr.Code == "42501" {
			action.Note = "insufficient privilege: index owner required"
			return action, nil
		}
		return action, fmt.Errorf("pg_sqlc: reindex %s: %w", sessionTokenHashIndex, err)
	}
	action.Performed = true
	return action, nil
}
