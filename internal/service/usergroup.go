package service

import (
	"context"

	orgv1 "grape/api/org/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserGroupService 是用户组资源的传输层适配器：DTO ↔ DO 转换、动态查询
// 协议转换，委托给 biz 用例，不实现业务规则。用户组是租户内资源，端点
// 全部受认证保护，租户边界与可见性由存储层 RLS 承担。
type UserGroupService struct {
	orgv1.UnimplementedUserGroupServiceServer
	uc *biz.UserGroupUsecase
}

// NewUserGroupService 构造用户组服务。
func NewUserGroupService(uc *biz.UserGroupUsecase) *UserGroupService {
	return &UserGroupService{uc: uc}
}

// CreateUserGroup 创建用户组。
func (s *UserGroupService) CreateUserGroup(ctx context.Context, req *orgv1.CreateUserGroupRequest) (*orgv1.UserGroup, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetGroup()
	if in == nil {
		return nil, biz.ErrUserGroupInvalidArgument
	}
	out, err := s.uc.CreateUserGroup(ctx, auth, &biz.UserGroup{
		Status:      normalizeUserGroupStatus(in.Status),
		Name:        in.GetName(),
		Code:        in.GetCode(),
		Remark:      optionalString(in.Remark),
		OwnerUserID: in.OwnerUserId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoUserGroup(out), nil
}

// GetUserGroup 按 id 查询用户组。
func (s *UserGroupService) GetUserGroup(ctx context.Context, req *orgv1.GetUserGroupRequest) (*orgv1.UserGroup, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetUserGroup(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoUserGroup(out), nil
}

// ListUserGroups 按动态查询协议返回一页用户组；协议非法映射为
// INVALID_ARGUMENT。
func (s *UserGroupService) ListUserGroups(ctx context.Context, req *orgv1.ListUserGroupsRequest) (*orgv1.UserGroupSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrUserGroupInvalidArgument, err)
	}
	out, err := s.uc.ListUserGroups(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*orgv1.UserGroup, 0, len(out.Items))
	for _, g := range out.Items {
		items = append(items, toProtoUserGroup(g))
	}
	return &orgv1.UserGroupSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateUserGroup 覆盖可变字段（name、status、remark）；status 必填
// （optional 传达禁用语义），code 与 owner_user_id 在输入中被忽略。
func (s *UserGroupService) UpdateUserGroup(ctx context.Context, req *orgv1.UpdateUserGroupRequest) (*orgv1.UserGroup, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetGroup()
	if in == nil || in.Status == nil {
		return nil, biz.ErrUserGroupInvalidArgument
	}
	out, err := s.uc.UpdateUserGroup(ctx, auth, &biz.UserGroup{
		ID:     in.GetId(),
		Status: *in.Status,
		Name:   in.GetName(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoUserGroup(out), nil
}

// DeleteUserGroup 软删除用户组。
func (s *UserGroupService) DeleteUserGroup(ctx context.Context, req *orgv1.DeleteUserGroupRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteUserGroup(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// ListUserGroupMembers 分页返回组成员。
func (s *UserGroupService) ListUserGroupMembers(ctx context.Context, req *orgv1.ListUserGroupMembersRequest) (*orgv1.UserGroupMemberSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.ListUserGroupMembers(ctx, auth, req.GetId(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	items := make([]*orgv1.UserGroupMember, 0, len(out.Items))
	for _, m := range out.Items {
		items = append(items, &orgv1.UserGroupMember{
			UserId:   m.UserID,
			Name:     m.Name,
			Nickname: m.Nickname,
			Status:   m.Status,
			AddedBy:  m.AddedBy,
			AddedAt:  timestamppb.New(m.AddedAt),
		})
	}
	page := normalizeMemberPage(req.GetPage())
	pageSize := normalizeMemberPageSize(req.GetPageSize())
	hasMore := int64(page)*int64(pageSize) < out.Total
	return &orgv1.UserGroupMemberSet{
		Items:   items,
		Total:   out.Total,
		HasMore: hasMore,
	}, nil
}

// AddUserGroupMembers 批量添加成员。
func (s *UserGroupService) AddUserGroupMembers(ctx context.Context, req *orgv1.AddUserGroupMembersRequest) (*orgv1.AddUserGroupMembersReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	added, err := s.uc.AddUserGroupMembers(ctx, auth, req.GetId(), req.GetUserIds())
	if err != nil {
		return nil, err
	}
	return &orgv1.AddUserGroupMembersReply{Added: added}, nil
}

// RemoveUserGroupMembers 批量移除成员。
func (s *UserGroupService) RemoveUserGroupMembers(ctx context.Context, req *orgv1.RemoveUserGroupMembersRequest) (*orgv1.RemoveUserGroupMembersReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	removed, err := s.uc.RemoveUserGroupMembers(ctx, auth, req.GetId(), req.GetUserIds())
	if err != nil {
		return nil, err
	}
	return &orgv1.RemoveUserGroupMembersReply{Removed: removed}, nil
}

// toProtoUserGroup 将领域模型转换为 DTO。
func toProtoUserGroup(g *biz.UserGroup) *orgv1.UserGroup {
	return &orgv1.UserGroup{
		Id:          g.ID,
		Status:      &g.Status,
		Name:        g.Name,
		Code:        g.Code,
		Remark:      g.Remark,
		OwnerUserId: g.OwnerUserID,
		CreatedBy:   g.CreatedBy,
		UpdatedBy:   g.UpdatedBy,
		CreatedAt:   timestamppb.New(g.CreatedAt),
		UpdatedAt:   tsPBPtr(g.UpdatedAt),
		DeletedAt:   tsPBPtr(g.DeletedAt),
	}
}

// normalizeUserGroupStatus 归一化创建入参的状态：optional 的缺失映射为
// biz 的未指定哨兵（用例落缺省启用）；显式取值原样透传——含 0=禁用，
// 不得并入缺省分支。
func normalizeUserGroupStatus(status *int32) int32 {
	if status == nil {
		return biz.UserGroupStatusUnspecified
	}
	return *status
}

// normalizeMemberPage 归一化成员分页的页码（与用例同口径）：小于 1 取 1。
func normalizeMemberPage(page int32) int32 {
	if page < 1 {
		return 1
	}
	return page
}

// normalizeMemberPageSize 归一化成员分页的页大小（与用例同口径）：
// 小于 1 取缺省，大于上限取上限。
func normalizeMemberPageSize(pageSize int32) int32 {
	if pageSize < 1 {
		return biz.UserGroupMemberPageDefault
	}
	if pageSize > biz.UserGroupMemberPageMax {
		return biz.UserGroupMemberPageMax
	}
	return pageSize
}
