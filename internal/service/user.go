package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	queryv1 "grape/api/query/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// UserService 是用户资源的传输层适配器：DTO ↔ DO 转换、动态查询协议
// 转换，委托给 biz 用例，不实现业务规则。
type UserService struct {
	userv1.UnimplementedUserServiceServer
	uc         *biz.UserUsecase
	assignment *biz.AssignmentUsecase
}

// NewUserService 构造用户服务。
func NewUserService(uc *biz.UserUsecase, assignment *biz.AssignmentUsecase) *UserService {
	return &UserService{uc: uc, assignment: assignment}
}

// authOf 读取认证中间件注入的身份事实。受保护端点已由中间件完成令牌
// 校验，缺失即装配错误或绕过中间件的调用，按未认证 fail-closed。
func authOf(ctx context.Context) (biz.Identity, error) {
	auth, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return biz.Identity{}, biz.ErrSessionInvalid
	}
	return auth, nil
}

// CreateUser 创建用户并设置初始密码。
func (s *UserService) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.User, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetUser()
	out, err := s.uc.CreateUser(ctx, auth, &biz.User{
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
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetUser(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// ListUsers 按动态查询协议返回一页用户；协议非法映射为 INVALID_ARGUMENT。
func (s *UserService) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.UserSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrUserInvalidArgument, err)
	}
	out, err := s.uc.ListUsers(ctx, auth, q)
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
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetUser()
	if in.Status == nil {
		return nil, biz.ErrUserInvalidArgument
	}
	out, err := s.uc.UpdateUser(ctx, auth, &biz.User{
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
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.SetUserPassword(ctx, auth, req.GetId(), req.GetPassword()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// DeleteUser 软删除用户。
func (s *UserService) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteUser(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// AssignRoles 批量分配角色。
func (s *UserService) AssignRoles(ctx context.Context, req *userv1.AssignRolesRequest) (*userv1.AssignRolesReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	added, err := s.uc.AssignRoles(ctx, auth, req.GetUserId(), req.GetRoleIds())
	if err != nil {
		return nil, err
	}
	return &userv1.AssignRolesReply{Added: added}, nil
}

// TransferUserOwner 修改归属三元组。
func (s *UserService) TransferUserOwner(ctx context.Context, req *userv1.TransferUserOwnerRequest) (*userv1.User, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.TransferUserOwner(ctx, auth, req.GetId(), biz.OwnerTriple{
		UserID:  req.OwnerUserId,
		DeptID:  req.OwnerDeptId,
		GroupID: req.OwnerUserGroupId,
	})
	if err != nil {
		return nil, err
	}
	return toProtoUser(out), nil
}

// ListAssignableRoles 返回目标用户可分配且尚未拥有的活跃角色。
func (s *UserService) ListAssignableRoles(ctx context.Context, req *userv1.ListAssignableRolesRequest) (*queryv1.OptionSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.ListAssignableRoles(ctx, auth, req.GetTargetUserId(), optionQueryOf(req.GetQuery()))
	if err != nil {
		return nil, err
	}
	return toProtoOptionSet(out), nil
}

// ListUserRoles 返回目标用户当前可管理角色集合与版本。
func (s *UserService) ListUserRoles(ctx context.Context, req *userv1.ListUserRolesRequest) (*userv1.UserRoleSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.ListUserRoles(ctx, auth, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return toProtoUserRoleSet(out), nil
}

// SetUserRoles 以「管理范围内整组替换」写入角色集合；版本不一致返回 409。
func (s *UserService) SetUserRoles(ctx context.Context, req *userv1.SetUserRolesRequest) (*userv1.UserRoleSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID:          req.GetUserId(),
		RoleIDs:         req.GetRoleIds(),
		ExpectedVersion: req.GetExpectedVersion(),
	})
	if err != nil {
		return nil, err
	}
	return toProtoUserRoleSet(out), nil
}

// ListOwnerUserOptions 返回归属用户候选。
func (s *UserService) ListOwnerUserOptions(ctx context.Context, req *userv1.ListOwnerOptionsRequest) (*queryv1.OptionSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.ListOwnerUserOptions(ctx, auth, optionQueryOf(req.GetQuery()))
	if err != nil {
		return nil, err
	}
	return toProtoOptionSet(out), nil
}

// ListOwnerDeptOptions 返回归属部门候选。
func (s *UserService) ListOwnerDeptOptions(ctx context.Context, req *userv1.ListOwnerOptionsRequest) (*queryv1.OptionSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.ListOwnerDeptOptions(ctx, auth, optionQueryOf(req.GetQuery()))
	if err != nil {
		return nil, err
	}
	return toProtoOptionSet(out), nil
}

// ListOwnerGroupOptions 返回归属用户组候选。
func (s *UserService) ListOwnerGroupOptions(ctx context.Context, req *userv1.ListOwnerOptionsRequest) (*queryv1.OptionSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.assignment.ListOwnerGroupOptions(ctx, auth, optionQueryOf(req.GetQuery()))
	if err != nil {
		return nil, err
	}
	return toProtoOptionSet(out), nil
}

// optionQueryOf 把 DTO 选项查询转换为领域入参；未设置即零值（由用例
// 归一化为缺省页码与页大小）。
func optionQueryOf(q *queryv1.OptionQuery) biz.OptionQuery {
	if q == nil {
		return biz.OptionQuery{}
	}
	return biz.OptionQuery{
		Keyword:  q.GetKeyword(),
		Page:     q.GetPage(),
		PageSize: q.GetPageSize(),
	}
}

// toProtoOptionSet 渲染选项分页信封：int64 ID 转十进制字符串（前端不丢
// 精度）；total v1 恒不设置。
func toProtoOptionSet(set *biz.OptionSet) *queryv1.OptionSet {
	items := make([]*queryv1.Option, 0, len(set.Items))
	for _, o := range set.Items {
		items = append(items, toProtoOption(o))
	}
	return &queryv1.OptionSet{
		Items:   items,
		HasMore: set.HasMore,
		Total:   set.Total,
		Page:    set.Page,
	}
}

// toProtoUserRoleSet 渲染角色集合：条目同 Option 口径，unmanaged_count
// 只暴露数量。
func toProtoUserRoleSet(set *biz.UserRoleSet) *userv1.UserRoleSet {
	items := make([]*queryv1.Option, 0, len(set.Items))
	for _, o := range set.Items {
		items = append(items, toProtoOption(o))
	}
	return &userv1.UserRoleSet{
		Items:          items,
		UnmanagedCount: set.UnmanagedCount,
		Version:        set.Version,
	}
}

// toProtoOption 渲染单个候选项。
func toProtoOption(o *biz.Option) *queryv1.Option {
	return &queryv1.Option{
		Value:    strconv.FormatInt(o.Value, 10),
		Label:    o.Label,
		Enabled:  o.Enabled,
		Reason:   o.Reason,
		Assigned: o.Assigned,
	}
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
