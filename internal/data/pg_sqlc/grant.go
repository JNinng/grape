package pg_sqlc

import (
	"context"
	"errors"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// grantRepo 是 biz.GrantRepo 的 PostgreSQL 实现（agent_tool_grant，
// TODO C2）。全部操作经会话门面在租户事务内执行（RLS）；每条查询显式
// 携带认证租户过滤——tenant_id 参数取自认证身份而非请求侧输入，RLS
// 被关闭时跨租户仍不可见（应用层兜底，决策 6/D1 口径）。
type grantRepo struct {
	data *Data
}

// NewGrantRepo 构造工具授权仓储。
func NewGrantRepo(data *Data) biz.GrantRepo {
	return &grantRepo{data: data}
}

// toBizGrant 将持久化行转换为领域模型（列与 biz.ToolGrant 一一对应，
// 同一 DO 贯穿全程）。
func toBizGrant(po *gen.AgentToolGrant) *biz.ToolGrant {
	return &biz.ToolGrant{
		ID:        po.ID,
		TenantID:  po.TenantID,
		UserID:    po.UserID,
		ToolName:  po.ToolName,
		SessionID: po.SessionID,
		CreatedBy: po.CreatedBy,
		CreatedAt: tsToTime(po.CreatedAt),
		RevokedBy: po.RevokedBy,
		RevokedAt: tsToTimePtr(po.RevokedAt),
	}
}

// toBizGrants 批量行转换。
func toBizGrants(rows []gen.AgentToolGrant) []*biz.ToolGrant {
	out := make([]*biz.ToolGrant, 0, len(rows))
	for i := range rows {
		out = append(out, toBizGrant(&rows[i]))
	}
	return out
}

// FindActiveForSession 按会话级查活跃许可；未命中不是错误，返回
// (nil, nil)（评估路径的 miss 是正常控制流，见 biz.GrantRepo 注释）。
func (r *grantRepo) FindActiveForSession(ctx context.Context, auth biz.Identity, userID int64, sessionID, toolName string) (*biz.ToolGrant, error) {
	var out *biz.ToolGrant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetActiveAgentToolGrantForSession(ctx, gen.GetActiveAgentToolGrantForSessionParams{
			TenantID:  auth.TenantID,
			UserID:    userID,
			ToolName:  toolName,
			SessionID: &sessionID,
		})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			return nil // miss：(nil, nil)
		}
		out = toBizGrant(&row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FindActiveGlobal 按全局级查活跃许可；未命中语义同会话级。
func (r *grantRepo) FindActiveGlobal(ctx context.Context, auth biz.Identity, userID int64, toolName string) (*biz.ToolGrant, error) {
	var out *biz.ToolGrant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetActiveAgentToolGrantGlobal(ctx, gen.GetActiveAgentToolGrantGlobalParams{
			TenantID: auth.TenantID,
			UserID:   userID,
			ToolName: toolName,
		})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			return nil // miss：(nil, nil)
		}
		out = toBizGrant(&row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Create 写入一条授权：主键应用层发号，租户取认证身份；同键活跃许可
// 已存在时唯一索引命中、ON CONFLICT 跳过（返回 false，不报错——许可
// 语义是布尔存在，先到决定生效）。
func (r *grantRepo) Create(ctx context.Context, auth biz.Identity, g *biz.ToolGrant) (bool, error) {
	var inserted bool
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().InsertAgentToolGrant(ctx, gen.InsertAgentToolGrantParams{
			ID:        newID(),
			TenantID:  auth.TenantID,
			UserID:    g.UserID,
			ToolName:  g.ToolName,
			SessionID: g.SessionID,
			CreatedBy: g.CreatedBy,
		})
		if err != nil {
			return err
		}
		inserted = rows == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// GetByID 按租户内 id 取授权行（含已吊销）；不存在或跨租户统一
// ErrGrantNotFound（不泄露存在性）。
func (r *grantRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.ToolGrant, error) {
	var out *biz.ToolGrant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetAgentToolGrantByID(ctx, gen.GetAgentToolGrantByIDParams{
			ID:       id,
			TenantID: auth.TenantID,
		})
		if err != nil {
			return err
		}
		out = toBizGrant(&row)
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, biz.ErrGrantNotFound
		}
		return nil, err
	}
	return out, nil
}

// ListActiveByUser 列出指定用户的活跃授权（本人视图）。
func (r *grantRepo) ListActiveByUser(ctx context.Context, auth biz.Identity, userID int64) ([]*biz.ToolGrant, error) {
	var out []*biz.ToolGrant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListActiveAgentToolGrantsByUser(ctx, gen.ListActiveAgentToolGrantsByUserParams{
			TenantID: auth.TenantID,
			UserID:   userID,
		})
		if err != nil {
			return err
		}
		out = toBizGrants(rows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListActiveByTenant 列出租户内全部活跃授权（管理视图，权限码 gate 在
// 用例层）。
func (r *grantRepo) ListActiveByTenant(ctx context.Context, auth biz.Identity) ([]*biz.ToolGrant, error) {
	var out []*biz.ToolGrant
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListActiveAgentToolGrantsByTenant(ctx, auth.TenantID)
		if err != nil {
			return err
		}
		out = toBizGrants(rows)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeOwn 属主吊销：id + 归属人双定位，非本人不可经本查询触达；
// 0 行（非本人/已吊销/不存在）统一 ErrGrantNotFound。
func (r *grantRepo) RevokeOwn(ctx context.Context, auth biz.Identity, id int64) error {
	revokedBy := auth.UserID
	return r.revoke(ctx, auth, func(s *IdentityTx) (int64, error) {
		return s.Q().RevokeAgentToolGrantSelf(ctx, gen.RevokeAgentToolGrantSelfParams{
			ID:        id,
			TenantID:  auth.TenantID,
			UserID:    auth.UserID,
			RevokedBy: &revokedBy,
		})
	})
}

// RevokeManaged 管理吊销：调用方已验 agent:grant:revoke 权限码；0 行
// （不存在/已吊销）统一 ErrGrantNotFound。
func (r *grantRepo) RevokeManaged(ctx context.Context, auth biz.Identity, id int64) error {
	revokedBy := auth.UserID
	return r.revoke(ctx, auth, func(s *IdentityTx) (int64, error) {
		return s.Q().RevokeAgentToolGrantManaged(ctx, gen.RevokeAgentToolGrantManagedParams{
			ID:        id,
			TenantID:  auth.TenantID,
			RevokedBy: &revokedBy,
		})
	})
}

// revoke 是两条吊销原语共用的壳：0 行统一映射 ErrGrantNotFound。
func (r *grantRepo) revoke(ctx context.Context, auth biz.Identity, exec func(*IdentityTx) (int64, error)) error {
	return r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := exec(s)
		if err != nil {
			return err
		}
		if rows == 0 {
			return biz.ErrGrantNotFound
		}
		return nil
	})
}

// HasPermission 判断用户是否持有功能权限码（fn_has_permission，库内
// 推导：角色→菜单→权限码）。
func (r *grantRepo) HasPermission(ctx context.Context, auth biz.Identity, code string) (bool, error) {
	var ok bool
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		granted, err := s.Q().HasPermission(ctx, gen.HasPermissionParams{
			Operator: auth.UserID,
			Code:     code,
		})
		if err != nil {
			return err
		}
		ok = granted
		return nil
	})
	if err != nil {
		return false, err
	}
	return ok, nil
}
