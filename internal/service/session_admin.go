package service

import (
	"context"

	auditv1 "grape/api/audit/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SessionAdminService 是会话管理面（治理面）的传输层适配器：租户内会话
// 列表（动态查询协议）与主动撤销，委托给 biz 用例。令牌哈希不进入任何
// DTO（凭据不回显）；撤销幂等（目标不存在/已撤销/已被清理均成功返回，
// 不泄露存在性，与登出/认证撤销同口径）。租户边界由存储层 RLS 承担。
type SessionAdminService struct {
	auditv1.UnimplementedSessionAdminServiceServer
	uc *biz.SessionUsecase
}

// NewSessionAdminService 构造会话管理面服务。
func NewSessionAdminService(uc *biz.SessionUsecase) *SessionAdminService {
	return &SessionAdminService{uc: uc}
}

// ListSessions 按动态查询协议返回一页租户内会话；协议非法映射为
// INVALID_ARGUMENT。
func (s *SessionAdminService) ListSessions(ctx context.Context, req *auditv1.ListSessionsRequest) (*auditv1.SessionAdminSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrSessionInvalidArgument, err)
	}
	out, err := s.uc.ListSessions(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*auditv1.SessionAdmin, 0, len(out.Items))
	for _, row := range out.Items {
		items = append(items, toProtoSessionAdmin(row))
	}
	return &auditv1.SessionAdminSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// RevokeSession 按租户内会话 ID 主动撤销（幂等）。
func (s *SessionAdminService) RevokeSession(ctx context.Context, req *auditv1.RevokeSessionRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.Revoke(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoSessionAdmin 将领域模型转换为 DTO：令牌哈希不随行（模型不携带），
// 租户 ID 不回显（租户边界由 RLS 承担，管理面无跨租户语义）。
func toProtoSessionAdmin(row *biz.Session) *auditv1.SessionAdmin {
	return &auditv1.SessionAdmin{
		Id:               row.ID,
		Status:           row.Status,
		UserId:           row.UserID,
		ExpiresAt:        timestamppb.New(row.ExpiresAt),
		RefreshExpiresAt: tsPBPtr(row.RefreshExpiresAt),
		LastActivityAt:   tsPBPtr(row.LastActivityAt),
		DeviceInfo:       row.DeviceInfo,
		IpAddress:        row.IPAddress,
		IsRevoked:        row.IsRevoked,
		CreatedBy:        row.CreatedBy,
		UpdatedBy:        row.UpdatedBy,
		CreatedAt:        timestamppb.New(row.CreatedAt),
		UpdatedAt:        tsPBPtr(row.UpdatedAt),
	}
}
