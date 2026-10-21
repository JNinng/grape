package service

import (
	"context"
	"strings"

	sessionv1 "grape/api/session/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// SessionService 是登录/刷新的传输层适配器：DTO ↔ DO 转换，委托给
// biz 用例，不实现业务规则。过渡期默认租户的解析在 AuthUsecase
// （装配层注入），本层只透传请求事实。
type SessionService struct {
	sessionv1.UnimplementedSessionServiceServer
	auth     *biz.AuthUsecase
	sessions *biz.SessionUsecase
}

// NewSessionService 构造会话服务。
func NewSessionService(auth *biz.AuthUsecase, sessions *biz.SessionUsecase) *SessionService {
	return &SessionService{auth: auth, sessions: sessions}
}

// Login 校验用户名与口令并签发会话。device_info/ip_address 为过渡期
// 客户端声明，审计采集切面落地后改为服务端收集。
func (s *SessionService) Login(ctx context.Context, req *sessionv1.LoginRequest) (*sessionv1.LoginReply, error) {
	out, err := s.auth.Login(ctx, &biz.LoginInput{
		TenantID:   strings.TrimSpace(req.GetTenantId()),
		UserName:   strings.TrimSpace(req.GetUserName()),
		Password:   req.GetPassword(),
		DeviceInfo: req.DeviceInfo,
		IPAddress:  req.IpAddress,
	})
	if err != nil {
		return nil, err
	}
	return toLoginReply(out.Issued, &out.User), nil
}

// Refresh 以刷新令牌轮换一对新令牌。会话行不携带账号名，用户摘要仅
// 填 id 与租户。
func (s *SessionService) Refresh(ctx context.Context, req *sessionv1.RefreshRequest) (*sessionv1.LoginReply, error) {
	out, err := s.sessions.Refresh(ctx, req.GetRefreshToken(), biz.SessionOptions{})
	if err != nil {
		return nil, err
	}
	return toLoginReply(out, nil), nil
}

// toLoginReply 渲染登录/刷新响应：令牌类型固定 Bearer，经
// Authorization: Bearer 头携带；过期时间以存储返回值为准。summary 为
// nil（刷新路径）时用户摘要只填会话行内事实。
func toLoginReply(out *biz.IssuedSession, summary *biz.UserSummary) *sessionv1.LoginReply {
	user := out.Session
	reply := &sessionv1.LoginReply{
		TokenType:    "Bearer",
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		ExpiresAt:    timestamppb.New(user.ExpiresAt),
		User: &sessionv1.SessionUser{
			Id:       user.UserID,
			TenantId: user.TenantID,
		},
	}
	if summary != nil {
		reply.User.Name = summary.Name
		reply.User.Nickname = summary.Nickname
	}
	return reply
}
