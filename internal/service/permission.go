package service

import (
	"context"

	permv1 "grape/api/perm/v1"
	"grape/internal/biz"
)

// PermissionService 是功能权限码查询的传输层适配器：返回当前认证用户
// 持有的全部权限码（前端导航过滤的数据源）。只读、目标恒取认证身份，
// 不设权限码——集合本身是用户界面的引导数据，判定中间件的 403 兜底
// 口径见 permission.md。
type PermissionService struct {
	permv1.UnimplementedPermissionServiceServer
	uc *biz.PermissionUsecase
}

// NewPermissionService 构造权限码查询服务。
func NewPermissionService(uc *biz.PermissionUsecase) *PermissionService {
	return &PermissionService{uc: uc}
}

// GetMyPermissions 返回当前认证用户持有的全部功能权限码（去重升序）。
// 用户无效（会话存续期间被禁用/删除）时返回空集合——判定函数对无效
// 用户恒返回不持有，空集是正常返回而非错误。
func (s *PermissionService) GetMyPermissions(ctx context.Context, req *permv1.GetMyPermissionsRequest) (*permv1.GetMyPermissionsReply, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	codes, err := s.uc.Codes(ctx, auth)
	if err != nil {
		return nil, err
	}
	if codes == nil {
		codes = []string{}
	}
	return &permv1.GetMyPermissionsReply{Codes: codes}, nil
}
