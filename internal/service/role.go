package service

import (
	"context"

	permv1 "grape/api/perm/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RoleService 是角色资源的传输层适配器：DTO ↔ DO 转换、动态查询协议
// 转换，委托给 biz 用例，不实现业务规则。角色是租户内资源，端点全部受
// 认证保护，租户边界与可见性由存储层 RLS 承担；数据范围与菜单授权是随
// 角色读写的配置面。
type RoleService struct {
	permv1.UnimplementedRoleServiceServer
	uc *biz.RoleUsecase
}

// NewRoleService 构造角色服务。
func NewRoleService(uc *biz.RoleUsecase) *RoleService {
	return &RoleService{uc: uc}
}

// CreateRole 创建角色。role_type、data_scope 与 scope_dept_id 在输入中
// 被忽略（服务端定值/配置面维护）。
func (s *RoleService) CreateRole(ctx context.Context, req *permv1.CreateRoleRequest) (*permv1.Role, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetRole()
	out, err := s.uc.CreateRole(ctx, auth, &biz.Role{
		Status: normalizeRoleStatus(in.Status),
		Name:   in.GetName(),
		Code:   in.GetCode(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoRole(out), nil
}

// GetRole 按 id 查询角色。
func (s *RoleService) GetRole(ctx context.Context, req *permv1.GetRoleRequest) (*permv1.Role, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetRole(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoRole(out), nil
}

// ListRoles 按动态查询协议返回一页角色；协议非法映射为 INVALID_ARGUMENT。
func (s *RoleService) ListRoles(ctx context.Context, req *permv1.ListRolesRequest) (*permv1.RoleSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrRoleInvalidArgument, err)
	}
	out, err := s.uc.ListRoles(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*permv1.Role, 0, len(out.Items))
	for _, r := range out.Items {
		items = append(items, toProtoRole(r))
	}
	return &permv1.RoleSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateRole 覆盖可变字段（name、status、remark）；status 必填（optional
// 传达禁用语义），code/role_type/data_scope/scope_dept_id 在输入中被忽略。
func (s *RoleService) UpdateRole(ctx context.Context, req *permv1.UpdateRoleRequest) (*permv1.Role, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetRole()
	if in.Status == nil {
		return nil, biz.ErrRoleInvalidArgument
	}
	out, err := s.uc.UpdateRole(ctx, auth, &biz.Role{
		ID:     in.GetId(),
		Status: *in.Status,
		Name:   in.GetName(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoRole(out), nil
}

// DeleteRole 软删除角色。
func (s *RoleService) DeleteRole(ctx context.Context, req *permv1.DeleteRoleRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteRole(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// GetRoleDataScope 读取角色的数据范围配置。
func (s *RoleService) GetRoleDataScope(ctx context.Context, req *permv1.GetRoleDataScopeRequest) (*permv1.RoleDataScope, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetRoleDataScope(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoRoleDataScope(out), nil
}

// SetRoleDataScope 设置角色的数据范围配置（整组替换）。
func (s *RoleService) SetRoleDataScope(ctx context.Context, req *permv1.SetRoleDataScopeRequest) (*permv1.RoleDataScope, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]biz.RoleIDScopeEntry, 0, len(req.GetIdScopes()))
	for _, e := range req.GetIdScopes() {
		entries = append(entries, biz.RoleIDScopeEntry{Type: e.GetType(), DataID: e.GetDataId()})
	}
	out, err := s.uc.SetRoleDataScope(ctx, auth, &biz.RoleDataScope{
		RoleID:         req.GetId(),
		DataScope:      req.GetDataScope(),
		ScopeDeptID:    req.ScopeDeptId,
		IDScopes:       entries,
		AllowedDeptIDs: req.GetAllowedDeptIds(),
	})
	if err != nil {
		return nil, err
	}
	return toProtoRoleDataScope(out), nil
}

// ListRoleMenus 返回角色当前的有效授权菜单 ID 集合（停用/软删菜单的残留
// 映射不出现在回显集合）。
func (s *RoleService) ListRoleMenus(ctx context.Context, req *permv1.ListRoleMenusRequest) (*permv1.RoleMenuSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.ListRoleMenus(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoRoleMenuSet(out), nil
}

// SetRoleMenus 设置角色的菜单授权（整组替换，差量同步）。
func (s *RoleService) SetRoleMenus(ctx context.Context, req *permv1.SetRoleMenusRequest) (*permv1.RoleMenuSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.SetRoleMenus(ctx, auth, req.GetId(), req.GetMenuIds())
	if err != nil {
		return nil, err
	}
	return toProtoRoleMenuSet(out), nil
}

// toProtoRole 将领域模型转换为 DTO。
func toProtoRole(r *biz.Role) *permv1.Role {
	return &permv1.Role{
		Id:          r.ID,
		Status:      &r.Status,
		Name:        r.Name,
		Code:        r.Code,
		RoleType:    r.RoleType,
		DataScope:   r.DataScope,
		ScopeDeptId: r.ScopeDeptID,
		Remark:      r.Remark,
		CreatedBy:   r.CreatedBy,
		UpdatedBy:   r.UpdatedBy,
		CreatedAt:   timestamppb.New(r.CreatedAt),
		UpdatedAt:   tsPBPtr(r.UpdatedAt),
		DeletedAt:   tsPBPtr(r.DeletedAt),
	}
}

// toProtoRoleDataScope 将数据范围配置转换为 DTO。
func toProtoRoleDataScope(cfg *biz.RoleDataScope) *permv1.RoleDataScope {
	entries := make([]*permv1.RoleIDScopeEntry, 0, len(cfg.IDScopes))
	for _, e := range cfg.IDScopes {
		entries = append(entries, &permv1.RoleIDScopeEntry{Type: e.Type, DataId: e.DataID})
	}
	return &permv1.RoleDataScope{
		RoleId:         cfg.RoleID,
		DataScope:      cfg.DataScope,
		ScopeDeptId:    cfg.ScopeDeptID,
		IdScopes:       entries,
		AllowedDeptIds: cfg.AllowedDeptIDs,
	}
}

// toProtoRoleMenuSet 将授权菜单集合转换为 DTO。
func toProtoRoleMenuSet(ids []int64) *permv1.RoleMenuSet {
	return &permv1.RoleMenuSet{MenuIds: ids}
}

// normalizeRoleStatus 归一化创建入参的状态：optional 的缺失映射为 biz 的
// 未指定哨兵（用例落缺省启用）；显式取值原样透传——含 0=禁用，不得并入
// 缺省分支。
func normalizeRoleStatus(status *int32) int32 {
	if status == nil {
		return biz.RoleStatusUnspecified
	}
	return *status
}
