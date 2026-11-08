package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// menuRepo 是 biz.MenuRepo 的 PostgreSQL 实现。sys_menu 是功能权限码链的
// 宿主树：读面为租户内普通读（表无 @scope，RLS 承担边界）；写面运行角色
// 无直写权限，全部写入经 98_menu_write.sql 的 SECURITY DEFINER 写函数——
// super 命名只表示走受控写路径，仍经 IdentityTx 设 app.tenant_id。
type menuRepo struct {
	data *Data
}

// NewMenuRepo 构造菜单仓储。
func NewMenuRepo(data *Data) biz.MenuRepo {
	return &menuRepo{data: data}
}

// toBizMenu 将持久化菜单转换为领域模型。
func toBizMenu(po *gen.SysMenu) *biz.Menu {
	if po == nil {
		return nil
	}
	return &biz.Menu{
		ID:             po.ID,
		Status:         int32(po.Status),
		Name:           po.Name,
		PermissionCode: po.PermissionCode,
		ParentID:       po.ParentID,
		MenuType:       int32(po.MenuType),
		Icon:           po.Icon,
		RoutePath:      po.RoutePath,
		Component:      po.Component,
		PermissionExpr: po.PermissionExpr,
		SortOrder:      po.SortOrder,
		Remark:         po.Remark,
		CreatedBy:      po.CreatedBy,
		UpdatedBy:      po.UpdatedBy,
		CreatedAt:      tsToTime(po.CreatedAt),
		UpdatedAt:      tsToTimePtr(po.UpdatedAt),
		DeletedAt:      tsToTimePtr(po.DeletedAt),
	}
}

// GetByID 按主键读取菜单。租户边界由 RLS 承担，跨租户等同不存在。
func (r *menuRepo) GetByID(ctx context.Context, auth biz.Identity, id int64) (*biz.Menu, error) {
	var out *biz.Menu
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysMenuByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizMenu(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListAll 返回租户内全部未删除菜单（菜单树装配与环检测的全量输入）。
func (r *menuRepo) ListAll(ctx context.Context, auth biz.Identity) ([]*biz.Menu, error) {
	var out []*biz.Menu
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListSysMenuAll(ctx)
		if err != nil {
			return err
		}
		out = make([]*biz.Menu, 0, len(rows))
		for i := range rows {
			out = append(out, toBizMenu(&rows[i]))
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// CreateSuper 创建菜单（受控写面）：主键由应用层发号（newID），租户由
// 受控函数从会话 app.tenant_id 落值，审计列记操作者；租户内未删除菜单
// 间 permission_code 唯一冲突映射为 ErrMenuAlreadyExists；父级合法性由
// 受控函数在 DEFINER 上下文内校验兜底。
func (r *menuRepo) CreateSuper(ctx context.Context, auth biz.Identity, m *biz.Menu) (*biz.Menu, error) {
	var out *biz.Menu
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysMenu(ctx, gen.InsertSysMenuParams{
			ID:             newID(),
			Status:         int16(m.Status),
			Name:           m.Name,
			PermissionCode: m.PermissionCode,
			ParentID:       m.ParentID,
			MenuType:       int16(m.MenuType),
			Icon:           m.Icon,
			RoutePath:      m.RoutePath,
			Component:      m.Component,
			PermissionExpr: m.PermissionExpr,
			SortOrder:      m.SortOrder,
			Remark:         m.Remark,
			Operator:       auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizMenu(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateSuper 覆盖可变字段（name、status、icon、route_path、component、
// permission_expr、sort_order、remark；不含 permission_code/menu_type/
// parent_id 身份/类型/结构列）。受控函数对未命中目标返回 NULL——生成
// 代码按无行扫描失败，映射为 NotFound。
func (r *menuRepo) UpdateSuper(ctx context.Context, auth biz.Identity, m *biz.Menu) (*biz.Menu, error) {
	var out *biz.Menu
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysMenu(ctx, gen.UpdateSysMenuParams{
			ID:             m.ID,
			Status:         int16(m.Status),
			Name:           m.Name,
			Icon:           m.Icon,
			RoutePath:      m.RoutePath,
			Component:      m.Component,
			PermissionExpr: m.PermissionExpr,
			SortOrder:      m.SortOrder,
			Remark:         m.Remark,
			Operator:       auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizMenu(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// MoveSuper 移动菜单到新父级（parentID 为 nil 表示移为根菜单）。父级
// 合法性与防环由受控函数在 DEFINER 上下文内校验兜底（调用方已在用例
// 层预检），拒绝原样透传为存储错误并整体回滚。
func (r *menuRepo) MoveSuper(ctx context.Context, auth biz.Identity, id int64, parentID *int64) (*biz.Menu, error) {
	var out *biz.Menu
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().MoveSysMenu(ctx, gen.MoveSysMenuParams{
			ID:       id,
			ParentID: parentID,
			Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizMenu(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// SoftDeleteSuper 软删除菜单。同一事务内先数未删除直接子菜单：有子菜单
// 返回 ErrMenuHasChildren，否则交受控函数置位 deleted_at（函数内子菜单
// 校验兜底同口径）；返回 0 行即目标未命中（映射 NotFound）。
func (r *menuRepo) SoftDeleteSuper(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		children, err := s.Q().CountSysMenuActiveChildren(ctx, &id)
		if err != nil {
			return err
		}
		if children > 0 {
			return biz.ErrMenuHasChildren
		}
		rows, err := s.Q().SoftDeleteSysMenu(ctx, gen.SoftDeleteSysMenuParams{
			ID:       id,
			Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 将驱动与协议错误映射为菜单资源的领域错误：无行（含跨租户等同
// 不存在）→ NotFound；唯一约束冲突 → AlreadyExists；其余（含受控函数
// 的父级/防环/子菜单拒绝）原样透传。
func (r *menuRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrMenuNotFound, biz.ErrMenuInvalidArgument, biz.ErrMenuAlreadyExists)
}
