package service

import (
	"context"

	permv1 "grape/api/perm/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MenuService 是菜单/权限点树的传输层适配器：DTO ↔ DO 转换，委托给 biz
// 用例，不实现业务规则。读端点按权限默认处理（租户边界由存储层 RLS 承
// 担，表无 @scope 租户内全可见）；写端点是受控写面（部署面 SECURITY
// DEFINER 固定口径写函数，运行角色无直写权限），方法名按 permission.md
// 口径带 Super。
type MenuService struct {
	permv1.UnimplementedMenuServiceServer
	uc *biz.MenuUsecase
}

// NewMenuService 构造菜单服务。
func NewMenuService(uc *biz.MenuUsecase) *MenuService {
	return &MenuService{uc: uc}
}

// GetMenuTree 返回当前租户内全部未删除菜单的树形结构。
func (s *MenuService) GetMenuTree(ctx context.Context, req *permv1.GetMenuTreeRequest) (*permv1.MenuTreeReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	roots, total, err := s.uc.GetMenuTree(ctx, auth)
	if err != nil {
		return nil, err
	}
	return &permv1.MenuTreeReply{
		Roots: toProtoMenuNodes(roots),
		Total: int64(total),
	}, nil
}

// GetMenu 按 id 查询单个菜单。
func (s *MenuService) GetMenu(ctx context.Context, req *permv1.GetMenuRequest) (*permv1.Menu, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetMenu(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoMenu(out), nil
}

// CreateMenuSuper 创建菜单（super 面）。permission_code/menu_type 在
// 创建时生效（身份/类型列，创建后不可变）。
func (s *MenuService) CreateMenuSuper(ctx context.Context, req *permv1.CreateMenuSuperRequest) (*permv1.Menu, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetMenu()
	if in == nil {
		return nil, biz.ErrMenuInvalidArgument
	}
	out, err := s.uc.CreateMenuSuper(ctx, auth, &biz.Menu{
		Status:         normalizeMenuStatus(in.Status),
		Name:           in.GetName(),
		PermissionCode: in.GetPermissionCode(),
		ParentID:       in.ParentId,
		MenuType:       in.GetMenuType(),
		Icon:           in.Icon,
		RoutePath:      in.RoutePath,
		Component:      in.Component,
		PermissionExpr: in.PermissionExpr,
		SortOrder:      in.GetSortOrder(),
		Remark:         in.Remark,
	})
	if err != nil {
		return nil, err
	}
	return toProtoMenu(out), nil
}

// UpdateMenuSuper 覆盖可变字段（name、status、icon、route_path、
// component、permission_expr、sort_order、remark）；status 必填（optional
// 传达禁用语义），permission_code/menu_type/parent_id 在输入中被忽略
// （身份/类型/结构列）。
func (s *MenuService) UpdateMenuSuper(ctx context.Context, req *permv1.UpdateMenuSuperRequest) (*permv1.Menu, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetMenu()
	if in == nil || in.Status == nil {
		return nil, biz.ErrMenuInvalidArgument
	}
	out, err := s.uc.UpdateMenuSuper(ctx, auth, &biz.Menu{
		ID:             in.GetId(),
		Status:         *in.Status,
		Name:           in.GetName(),
		Icon:           in.Icon,
		RoutePath:      in.RoutePath,
		Component:      in.Component,
		PermissionExpr: in.PermissionExpr,
		SortOrder:      in.GetSortOrder(),
		Remark:         in.Remark,
	})
	if err != nil {
		return nil, err
	}
	return toProtoMenu(out), nil
}

// MoveMenuSuper 移动菜单到新父级；parent_id 未设置表示移为根菜单。
func (s *MenuService) MoveMenuSuper(ctx context.Context, req *permv1.MoveMenuSuperRequest) (*permv1.Menu, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.MoveMenuSuper(ctx, auth, req.GetId(), req.ParentId)
	if err != nil {
		return nil, err
	}
	return toProtoMenu(out), nil
}

// DeleteMenuSuper 软删除菜单。
func (s *MenuService) DeleteMenuSuper(ctx context.Context, req *permv1.DeleteMenuSuperRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteMenuSuper(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoMenu 将领域模型转换为 DTO。
func toProtoMenu(m *biz.Menu) *permv1.Menu {
	return &permv1.Menu{
		Id:             m.ID,
		Status:         &m.Status,
		Name:           m.Name,
		PermissionCode: m.PermissionCode,
		ParentId:       m.ParentID,
		MenuType:       m.MenuType,
		Icon:           m.Icon,
		RoutePath:      m.RoutePath,
		Component:      m.Component,
		PermissionExpr: m.PermissionExpr,
		SortOrder:      m.SortOrder,
		Remark:         m.Remark,
		CreatedBy:      m.CreatedBy,
		UpdatedBy:      m.UpdatedBy,
		CreatedAt:      timestamppb.New(m.CreatedAt),
		UpdatedAt:      tsPBPtr(m.UpdatedAt),
		DeletedAt:      tsPBPtr(m.DeletedAt),
	}
}

// toProtoMenuNodes 将领域树节点转换为 DTO 树节点（保持既有顺序）。
func toProtoMenuNodes(roots []*biz.MenuNode) []*permv1.MenuTreeNode {
	out := make([]*permv1.MenuTreeNode, 0, len(roots))
	for _, node := range roots {
		out = append(out, &permv1.MenuTreeNode{
			Menu:     toProtoMenu(node.Menu),
			Children: toProtoMenuNodes(node.Children),
		})
	}
	return out
}

// normalizeMenuStatus 归一化创建入参的状态：optional 的缺失映射为 biz 的
// 未指定哨兵（用例落缺省启用）；显式取值原样透传——含 0=禁用，不得并入
// 缺省分支。
func normalizeMenuStatus(status *int32) int32 {
	if status == nil {
		return biz.MenuStatusUnspecified
	}
	return *status
}
