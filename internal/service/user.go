package service

import (
	"context"

	v1 "grape/api/user/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// userEnumAliases 是用户查询中枚举字段的线上枚举名到存储值映射。
var userEnumAliases = buildUserStatusAliases()

// buildUserStatusAliases 从 proto 枚举派生映射，避免词表手写漂移。
func buildUserStatusAliases() enumAliases {
	mapping := make(map[string]string, len(v1.UserStatus_name))
	for num, name := range v1.UserStatus_name {
		status, ok := convertUserStatusDO(v1.UserStatus(num))
		if !ok || status == "" {
			continue
		}
		mapping[name] = string(status)
	}
	return enumAliases{"status": mapping}
}

// UserService 是用户服务。
type UserService struct {
	v1.UnimplementedUserServiceServer

	uc *biz.UserUsecase
}

// NewUserService 构造用户服务。
func NewUserService(uc *biz.UserUsecase) *UserService {
	return &UserService{uc: uc}
}

// CreateUser 创建用户并设置初始密码。
func (s *UserService) CreateUser(ctx context.Context, req *v1.CreateUserRequest) (*v1.User, error) {
	u := convertUser(req.GetUser())
	if u == nil {
		return nil, biz.ErrUserInvalidArgument
	}
	u.Password = req.GetPassword()
	u, err := s.uc.CreateUser(ctx, u)
	if err != nil {
		return nil, err
	}
	return convertUserReply(u), nil
}

// GetUser 按 ID 查询用户。
func (s *UserService) GetUser(ctx context.Context, req *v1.GetUserRequest) (*v1.User, error) {
	u, err := s.uc.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return convertUserReply(u), nil
}

// GetUserByEmail 按邮箱查询用户。
func (s *UserService) GetUserByEmail(ctx context.Context, req *v1.GetUserByEmailRequest) (*v1.User, error) {
	u, err := s.uc.GetUserByEmail(ctx, req.GetEmail())
	if err != nil {
		return nil, err
	}
	return convertUserReply(u), nil
}

// ListUsers 经动态查询协议列出用户。
func (s *UserService) ListUsers(ctx context.Context, req *v1.ListUsersRequest) (*v1.UserSet, error) {
	q, err := convertQuery(req.GetQuery(), userEnumAliases)
	if err != nil {
		return nil, invalidQueryError(biz.ErrUserInvalidArgument, err)
	}
	list, err := s.uc.ListUsers(ctx, q)
	if err != nil {
		return nil, err
	}
	set := &v1.UserSet{
		Items:      make([]*v1.User, 0, len(list.Items)),
		HasMore:    list.HasMore,
		NextCursor: list.NextCursor,
		Total:      list.Total,
		Page:       pageOf(req.GetQuery()),
	}
	for _, u := range list.Items {
		set.Items = append(set.Items, convertUserReply(u))
	}
	return set, nil
}

// UpdateUser 覆盖用户的可变字段。
func (s *UserService) UpdateUser(ctx context.Context, req *v1.UpdateUserRequest) (*v1.User, error) {
	u, err := s.uc.UpdateUser(ctx, convertUser(req.GetUser()))
	if err != nil {
		return nil, err
	}
	return convertUserReply(u), nil
}

// SetUserPassword 替换用户密码。
func (s *UserService) SetUserPassword(ctx context.Context, req *v1.SetUserPasswordRequest) (*emptypb.Empty, error) {
	if err := s.uc.SetPassword(ctx, req.GetId(), req.GetPassword()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// DeleteUser 物理删除用户。
func (s *UserService) DeleteUser(ctx context.Context, req *v1.DeleteUserRequest) (*emptypb.Empty, error) {
	if err := s.uc.DeleteUser(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// convertUser 将入站 proto 解析为 DO。时间戳与 status 不转换：二者由服务端
// 生成且只读，跨界只有可变字段。Password 由创建处理器填充，
// 仅创建路径携带。
func convertUser(in *v1.User) *biz.User {
	if in == nil {
		return nil
	}
	return &biz.User{
		ID:        in.GetId(),
		Name:      in.GetName(),
		Email:     in.GetEmail(),
		Role:      in.Role,
		Age:       in.Age,
		Score:     in.Score,
		Balance:   in.Balance,
		BirthDate: in.BirthDate,
		OrgID:     in.OrgId,
		Tags:      in.GetTags(),
		Meta:      in.GetMeta().AsMap(),
	}
}

// convertUserReply 将 DO 渲染为 API 用户。password 在此丢弃：
// 读取路径绝不跨越传输层。
func convertUserReply(in *biz.User) *v1.User {
	if in == nil {
		return nil
	}
	var meta *structpb.Struct
	if in.Meta != nil {
		meta, _ = structpb.NewStruct(in.Meta)
	}
	return &v1.User{
		Id:        in.ID,
		Name:      in.Name,
		Email:     in.Email,
		Status:    convertUserStatus(in.Status),
		Role:      in.Role,
		Age:       in.Age,
		Score:     in.Score,
		Balance:   in.Balance,
		BirthDate: in.BirthDate,
		OrgId:     in.OrgID,
		Tags:      in.Tags,
		Meta:      meta,
		CreatedAt: timestamppb.New(in.CreatedAt),
		UpdatedAt: timestampOf(in.UpdatedAt),
		DeletedAt: timestampOf(in.DeletedAt),
	}
}

// convertUserStatus 将领域状态映射到 api 枚举。两侧取值一致，
// 但映射显式写出，避免任何一侧静默漂移。
func convertUserStatus(in biz.UserStatus) v1.UserStatus {
	switch in {
	case biz.UserStatusActive:
		return v1.UserStatus_USER_STATUS_ACTIVE
	case biz.UserStatusInactive:
		return v1.UserStatus_USER_STATUS_INACTIVE
	case biz.UserStatusBanned:
		return v1.UserStatus_USER_STATUS_BANNED
	default:
		return v1.UserStatus_USER_STATUS_UNSPECIFIED
	}
}

// convertUserStatusDO 将 api 枚举映射到领域状态。零值枚举映射为空状态；
// 未定义枚举返回 false。
func convertUserStatusDO(in v1.UserStatus) (biz.UserStatus, bool) {
	switch in {
	case v1.UserStatus_USER_STATUS_UNSPECIFIED:
		return "", true
	case v1.UserStatus_USER_STATUS_ACTIVE:
		return biz.UserStatusActive, true
	case v1.UserStatus_USER_STATUS_INACTIVE:
		return biz.UserStatusInactive, true
	case v1.UserStatus_USER_STATUS_BANNED:
		return biz.UserStatusBanned, true
	default:
		return "", false
	}
}
