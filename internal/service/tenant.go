package service

import (
	"context"
	"time"

	tenantv1 "grape/api/tenant/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TenantService 是平台级租户实体（sys_tenant）的传输层适配器：DTO ↔ DO 与
// 动态查询协议转换，委托 biz 用例。方法名带 Super 表达「管理对象是一整个
// 租户实体」，不表示绕过约束——租户树边界仍由 RLS 承担，树外行等同
// NOT_FOUND（口径见 permission.md）。
type TenantService struct {
	tenantv1.UnimplementedTenantServiceServer
	uc *biz.TenantUsecase
}

// NewTenantService 构造租户服务。
func NewTenantService(uc *biz.TenantUsecase) *TenantService {
	return &TenantService{uc: uc}
}

// CreateTenantSuper 创建租户实体。id/tenant_id/审计列/时间戳在输入中被
// 忽略；parent_id 未设置（nil 或空串）由用例缺省为调用方租户，显式值
// 必须等于调用方租户；status 未设置（optional 缺失）缺省启用，显式值只
// 接受 0/1（-1 等哨兵值不是合法输入）。
func (s *TenantService) CreateTenantSuper(ctx context.Context, req *tenantv1.CreateTenantSuperRequest) (*tenantv1.Tenant, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTenant()
	if in == nil {
		return nil, biz.ErrTenantInvalidArgument
	}
	status := normalizeTenantStatus(in.Status)
	if in.Status != nil && status != biz.TenantStatusDisabled && status != biz.TenantStatusEnabled {
		return nil, biz.ErrTenantInvalidArgument
	}
	out, err := s.uc.CreateTenantSuper(ctx, auth, &biz.Tenant{
		Status:        status,
		Code:          in.GetCode(),
		Name:          in.GetName(),
		ParentID:      optionalString(in.ParentId),
		ContactName:   optionalString(in.ContactName),
		ContactPhone:  optionalString(in.ContactPhone),
		ExpiredAt:     timePtrOf(in.ExpiredAt),
		MaxUserCount:  in.MaxUserCount,
		DefaultLocale: in.GetDefaultLocale(),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTenant(out), nil
}

// GetTenantSuper 按 id 查询租户。
func (s *TenantService) GetTenantSuper(ctx context.Context, req *tenantv1.GetTenantSuperRequest) (*tenantv1.Tenant, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetTenantSuper(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTenant(out), nil
}

// ListTenantsSuper 按动态查询协议返回一页租户；协议非法映射为
// INVALID_ARGUMENT。
func (s *TenantService) ListTenantsSuper(ctx context.Context, req *tenantv1.ListTenantsSuperRequest) (*tenantv1.TenantSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrTenantInvalidArgument, err)
	}
	out, err := s.uc.ListTenantsSuper(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*tenantv1.Tenant, 0, len(out.Items))
	for _, t := range out.Items {
		items = append(items, toProtoTenant(t))
	}
	return &tenantv1.TenantSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateTenantSuper 覆盖可变字段（status、name、contact_name、
// contact_phone、expired_at、max_user_count、default_locale）；status
// 必填。id 标识目标；code 是身份列、parent_id 是结构列、tenant_id 是
// 归属列，创建后不可变，均在输入中被忽略（不映射进 DO）。
func (s *TenantService) UpdateTenantSuper(ctx context.Context, req *tenantv1.UpdateTenantSuperRequest) (*tenantv1.Tenant, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTenant()
	if in == nil || in.Status == nil {
		return nil, biz.ErrTenantInvalidArgument
	}
	out, err := s.uc.UpdateTenantSuper(ctx, auth, &biz.Tenant{
		ID:            in.GetId(),
		Status:        *in.Status,
		Name:          in.GetName(),
		ContactName:   optionalString(in.ContactName),
		ContactPhone:  optionalString(in.ContactPhone),
		ExpiredAt:     timePtrOf(in.ExpiredAt),
		MaxUserCount:  in.MaxUserCount,
		DefaultLocale: in.GetDefaultLocale(),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTenant(out), nil
}

// DeleteTenantSuper 软删除租户；存在未删除直接子租户时拒绝。
func (s *TenantService) DeleteTenantSuper(ctx context.Context, req *tenantv1.DeleteTenantSuperRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteTenantSuper(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// DisableTenantSuper 停用租户（status=0，幂等目标状态写）。
func (s *TenantService) DisableTenantSuper(ctx context.Context, req *tenantv1.DisableTenantSuperRequest) (*tenantv1.Tenant, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.DisableTenantSuper(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTenant(out), nil
}

// RestoreTenantSuper 恢复租户（status=1，幂等目标状态写）。
func (s *TenantService) RestoreTenantSuper(ctx context.Context, req *tenantv1.RestoreTenantSuperRequest) (*tenantv1.Tenant, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.RestoreTenantSuper(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTenant(out), nil
}

// toProtoTenant 将租户领域模型转换为 DTO。
func toProtoTenant(t *biz.Tenant) *tenantv1.Tenant {
	return &tenantv1.Tenant{
		Id:            t.ID,
		Status:        &t.Status,
		Code:          t.Code,
		Name:          t.Name,
		ParentId:      t.ParentID,
		ContactName:   t.ContactName,
		ContactPhone:  t.ContactPhone,
		ExpiredAt:     tsPBPtr(t.ExpiredAt),
		MaxUserCount:  t.MaxUserCount,
		DefaultLocale: t.DefaultLocale,
		CreatedBy:     t.CreatedBy,
		UpdatedBy:     t.UpdatedBy,
		CreatedAt:     timestamppb.New(t.CreatedAt),
		UpdatedAt:     tsPBPtr(t.UpdatedAt),
		DeletedAt:     tsPBPtr(t.DeletedAt),
		TenantId:      t.TenantID,
	}
}

// normalizeTenantStatus 归一化创建入参的状态：只有 optional 缺失（nil）
// 映射为 biz 的未指定哨兵（用例落缺省启用）；显式取值原样透传——含
// 0=禁用，不得并入缺省分支。显式值的 0/1 合法性由 CreateTenantSuper
// 在调用用例前校验（-1 等哨兵值不对外暴露为合法输入）。
func normalizeTenantStatus(status *int32) int32 {
	if status == nil {
		return biz.TenantStatusUnspecified
	}
	return *status
}

// timePtrOf 将 protobuf 时间戳转换为可空时间：nil 表示未设置（落库
// NULL，更新为覆盖语义——未设置即清空到期时间）。
func timePtrOf(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
