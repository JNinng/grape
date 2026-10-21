package service

import (
	"context"
	"strings"
	"time"

	userv1 "grape/api/user/v1"
	"grape/internal/biz"
	"grape/internal/conf"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserService 是用户资源的传输层适配器：DTO ↔ DO 转换、动态查询协议
// 转换，委托给 biz 用例，不实现业务规则。
type UserService struct {
	userv1.UnimplementedUserServiceServer
	uc   *biz.UserUsecase
	auth *conf.Auth
}

// NewUserService 构造用户服务。
func NewUserService(uc *biz.UserUsecase, auth *conf.Auth) *UserService {
	return &UserService{uc: uc, auth: auth}
}

// authOf 构造过渡期身份（固定配置）；认证落地后仅替换本方法的来源，
// 用例、仓储与门面不改。
func (s *UserService) authOf() biz.AuthContext {
	return biz.AuthContext{
		TenantID: s.auth.GetDefaultTenantId(),
		UserID:   s.auth.GetDefaultUserId(),
	}
}

// CreateUser 创建用户并设置初始密码。
func (s *UserService) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.User, error) {
	in := req.GetUser()
	out, err := s.uc.CreateUser(ctx, s.authOf(), &biz.User{
		Status:           normalizeUserStatus(in.Status),
		Name:             in.GetName(),
		Nickname:         in.GetNickname(),
		Password:         req.GetPassword(),
		Email:            optionalString(in.Email),
		Phone:            optionalString(in.Phone),
		Remark:           optionalString(in.Remark),
		Locale:           strings.TrimSpace(in.GetLocale()),
		OwnerUserID:      in.OwnerUserId,
		OwnerDeptID:      in.OwnerDeptId,
		OwnerUserGroupID: in.OwnerUserGroupId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// GetUser 按 id 查询用户。
func (s *UserService) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.User, error) {
	out, err := s.uc.GetUser(ctx, s.authOf(), req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// ListUsers 按动态查询协议返回一页用户；协议非法映射为 INVALID_ARGUMENT。
func (s *UserService) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.UserSet, error) {
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrUserInvalidArgument, err)
	}
	out, err := s.uc.ListUsers(ctx, s.authOf(), q)
	if err != nil {
		return nil, err
	}
	items := make([]*userv1.User, 0, len(out.Items))
	for _, u := range out.Items {
		items = append(items, toProtoUser(u))
	}
	return &userv1.UserSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateUser 覆盖可变字段；status 必填（optional 传达禁用语义），
// 归属三元组在输入中被忽略。
func (s *UserService) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.User, error) {
	in := req.GetUser()
	if in.Status == nil {
		return nil, biz.ErrUserInvalidArgument
	}
	out, err := s.uc.UpdateUser(ctx, s.authOf(), &biz.User{
		ID:       in.GetId(),
		Status:   *in.Status,
		Name:     in.GetName(),
		Nickname: in.GetNickname(),
		Email:    optionalString(in.Email),
		Phone:    optionalString(in.Phone),
		Remark:   optionalString(in.Remark),
		Locale:   strings.TrimSpace(in.GetLocale()),
	})
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// SetUserPassword 设置新密码。
func (s *UserService) SetUserPassword(ctx context.Context, req *userv1.SetUserPasswordRequest) (*emptypb.Empty, error) {
	if err := s.uc.SetUserPassword(ctx, s.authOf(), req.GetId(), req.GetPassword()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// DeleteUser 软删除用户。
func (s *UserService) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*emptypb.Empty, error) {
	if err := s.uc.DeleteUser(ctx, s.authOf(), req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// AssignRoles 批量分配角色。
func (s *UserService) AssignRoles(ctx context.Context, req *userv1.AssignRolesRequest) (*userv1.AssignRolesReply, error) {
	added, err := s.uc.AssignRoles(ctx, s.authOf(), req.GetUserId(), req.GetRoleIds())
	if err != nil {
		return nil, err
	}
	return &userv1.AssignRolesReply{Added: added}, nil
}

// TransferUserOwner 修改归属三元组。
func (s *UserService) TransferUserOwner(ctx context.Context, req *userv1.TransferUserOwnerRequest) (*userv1.User, error) {
	out, err := s.uc.TransferUserOwner(ctx, s.authOf(), req.GetId(), biz.OwnerTriple{
		UserID:  req.OwnerUserId,
		DeptID:  req.OwnerDeptId,
		GroupID: req.OwnerUserGroupId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// toProtoUser 将领域模型转换为 DTO。Password 不存在于 DO 读取路径，
// 天然不跨越传输层。
func toProtoUser(u *biz.User) *userv1.User {
	return &userv1.User{
		Id:               u.ID,
		Status:           &u.Status,
		Name:             u.Name,
		Nickname:         u.Nickname,
		Email:            u.Email,
		Phone:            u.Phone,
		Remark:           u.Remark,
		Locale:           u.Locale,
		OwnerUserId:      u.OwnerUserID,
		OwnerDeptId:      u.OwnerDeptID,
		OwnerUserGroupId: u.OwnerUserGroupID,
		CreatedBy:        u.CreatedBy,
		UpdatedBy:        u.UpdatedBy,
		CreatedAt:        timestamppb.New(u.CreatedAt),
		UpdatedAt:        tsPBPtr(u.UpdatedAt),
		DeletedAt:        tsPBPtr(u.DeletedAt),
	}
}

// tsPBPtr 渲染可空时间戳；nil 返回 nil。
func tsPBPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// normalizeUserStatus 归一化创建入参的状态：proto3 下 0 与未设置不可
// 区分，统一交给用例按缺省启用处理。
func normalizeUserStatus(status *int32) int32 {
	if status == nil {
		return 0
	}
	return *status
}

// optionalString 将空串归一为未设置（存储 NULL）。
func optionalString(v *string) *string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return v
}
