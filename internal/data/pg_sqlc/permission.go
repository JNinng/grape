package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// permissionRepo 是 biz.PermissionRepo 的 PostgreSQL 实现：功能权限码的
// 库内推导（角色 → 菜单 → 权限码）薄封装。全部经会话门面在租户事务内
// 执行（RLS 把查询限制在当前租户内，跨租户权限码等同于不存在）；
// 用户、角色、菜单任一环无效时判定返回 false、枚举返回空集——不持有
// 是正常返回，不是错误。
type permissionRepo struct {
	data *Data
}

// NewPermissionRepo 构造功能权限码仓储。
func NewPermissionRepo(data *Data) biz.PermissionRepo {
	return &permissionRepo{data: data}
}

// HasPermission 判断用户是否持有功能权限码（fn_has_permission）。
func (r *permissionRepo) HasPermission(ctx context.Context, auth biz.Identity, code string) (bool, error) {
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

// ListCodes 枚举用户持有的全部权限码（GetPermissionCodes，去重升序）。
func (r *permissionRepo) ListCodes(ctx context.Context, auth biz.Identity) ([]string, error) {
	var out []string
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		codes, err := s.Q().GetPermissionCodes(ctx, auth.UserID)
		if err != nil {
			return err
		}
		out = codes
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
