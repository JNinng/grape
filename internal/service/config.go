package service

import (
	"context"

	configv1 "grape/api/config/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConfigService 是全局配置（sys_config）的传输层适配器：DTO ↔ DO 与动态
// 查询协议转换，委托 biz 用例。sys_config 是平台级全局表（无 tenant_id、
// 无 RLS），读端点不设 super；写端点是部署面 SECURITY DEFINER 受控写函数，
// 方法名按 permission.md 带 Super，且仅平台根租户可写（子租户 403
// CONFIG_PLATFORM_REQUIRED）。
type ConfigService struct {
	configv1.UnimplementedConfigServiceServer
	uc *biz.ConfigUsecase
}

// NewConfigService 构造全局配置服务。
func NewConfigService(uc *biz.ConfigUsecase) *ConfigService {
	return &ConfigService{uc: uc}
}

// CreateConfigSuper 创建全局配置（super 面）。
func (s *ConfigService) CreateConfigSuper(ctx context.Context, req *configv1.CreateConfigSuperRequest) (*configv1.Config, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetConfig()
	if in == nil {
		return nil, biz.ErrConfigInvalidArgument
	}
	status, ok := normalizeConfigStatus(in.Status)
	if !ok {
		return nil, biz.ErrConfigInvalidArgument
	}
	out, err := s.uc.CreateConfigSuper(ctx, auth, &biz.Config{
		Status:    status,
		Name:      in.GetName(),
		ConfigKey: in.GetConfigKey(),
		Value:     optionalValue(in.Value),
		ValueType: in.GetValueType(),
		Remark:    optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoConfig(out), nil
}

// GetConfig 按 id 查询全局配置。
func (s *ConfigService) GetConfig(ctx context.Context, req *configv1.GetConfigRequest) (*configv1.Config, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetConfig(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoConfig(out), nil
}

// ListConfigs 按动态查询协议返回一页全局配置；协议非法映射为
// INVALID_ARGUMENT。
func (s *ConfigService) ListConfigs(ctx context.Context, req *configv1.ListConfigsRequest) (*configv1.ConfigSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrConfigInvalidArgument, err)
	}
	out, err := s.uc.ListConfigs(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*configv1.Config, 0, len(out.Items))
	for _, c := range out.Items {
		items = append(items, toProtoConfig(c))
	}
	return &configv1.ConfigSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateConfigSuper 覆盖可变字段（name、status、value、remark）；status
// 必填（optional 传达禁用语义），config_key/value_type 在输入中被忽略
// （身份/值语义列）。
func (s *ConfigService) UpdateConfigSuper(ctx context.Context, req *configv1.UpdateConfigSuperRequest) (*configv1.Config, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetConfig()
	if in == nil || in.Status == nil {
		return nil, biz.ErrConfigInvalidArgument
	}
	out, err := s.uc.UpdateConfigSuper(ctx, auth, &biz.Config{
		ID:     in.GetId(),
		Status: *in.Status,
		Name:   in.GetName(),
		Value:  optionalValue(in.Value),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoConfig(out), nil
}

// DeleteConfigSuper 软删除全局配置。
func (s *ConfigService) DeleteConfigSuper(ctx context.Context, req *configv1.DeleteConfigSuperRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteConfigSuper(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// TenantConfigService 是租户配置（sys_tenant_config）的传输层适配器。
// 租户配置是租户内资源：租户边界由存储层 RLS 承担，读写方法一律不带
// Super；tenant_id 只读，由服务端按认证身份落值（请求中的值被忽略）。
type TenantConfigService struct {
	configv1.UnimplementedTenantConfigServiceServer
	uc *biz.TenantConfigUsecase
}

// NewTenantConfigService 构造租户配置服务。
func NewTenantConfigService(uc *biz.TenantConfigUsecase) *TenantConfigService {
	return &TenantConfigService{uc: uc}
}

// CreateTenantConfig 创建租户配置；tenant_id 在输入中被忽略。
func (s *TenantConfigService) CreateTenantConfig(ctx context.Context, req *configv1.CreateTenantConfigRequest) (*configv1.TenantConfig, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTenantConfig()
	if in == nil {
		return nil, biz.ErrTenantConfigInvalidArgument
	}
	status, ok := normalizeConfigStatus(in.Status)
	if !ok {
		return nil, biz.ErrTenantConfigInvalidArgument
	}
	out, err := s.uc.CreateTenantConfig(ctx, auth, &biz.TenantConfig{
		Status:    status,
		Name:      in.GetName(),
		ConfigKey: in.GetConfigKey(),
		Value:     optionalValue(in.Value),
		ValueType: in.GetValueType(),
		Remark:    optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTenantConfig(out), nil
}

// GetTenantConfig 按 id 查询租户配置。
func (s *TenantConfigService) GetTenantConfig(ctx context.Context, req *configv1.GetTenantConfigRequest) (*configv1.TenantConfig, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetTenantConfig(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTenantConfig(out), nil
}

// ListTenantConfigs 按动态查询协议返回一页租户配置；协议非法映射为
// INVALID_ARGUMENT。
func (s *TenantConfigService) ListTenantConfigs(ctx context.Context, req *configv1.ListTenantConfigsRequest) (*configv1.TenantConfigSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrTenantConfigInvalidArgument, err)
	}
	out, err := s.uc.ListTenantConfigs(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*configv1.TenantConfig, 0, len(out.Items))
	for _, c := range out.Items {
		items = append(items, toProtoTenantConfig(c))
	}
	return &configv1.TenantConfigSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateTenantConfig 覆盖可变字段（name、status、value、remark）；
// status 必填，config_key/value_type 与 tenant_id 在输入中被忽略。
func (s *TenantConfigService) UpdateTenantConfig(ctx context.Context, req *configv1.UpdateTenantConfigRequest) (*configv1.TenantConfig, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTenantConfig()
	if in == nil || in.Status == nil {
		return nil, biz.ErrTenantConfigInvalidArgument
	}
	out, err := s.uc.UpdateTenantConfig(ctx, auth, &biz.TenantConfig{
		ID:     in.GetId(),
		Status: *in.Status,
		Name:   in.GetName(),
		Value:  optionalValue(in.Value),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTenantConfig(out), nil
}

// DeleteTenantConfig 软删除租户配置。
func (s *TenantConfigService) DeleteTenantConfig(ctx context.Context, req *configv1.DeleteTenantConfigRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteTenantConfig(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoConfig 将全局配置领域模型转换为 DTO。
func toProtoConfig(c *biz.Config) *configv1.Config {
	return &configv1.Config{
		Id:        c.ID,
		Status:    &c.Status,
		Name:      c.Name,
		ConfigKey: c.ConfigKey,
		Value:     c.Value,
		ValueType: c.ValueType,
		Remark:    c.Remark,
		CreatedBy: c.CreatedBy,
		UpdatedBy: c.UpdatedBy,
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: tsPBPtr(c.UpdatedAt),
		DeletedAt: tsPBPtr(c.DeletedAt),
	}
}

// toProtoTenantConfig 将租户配置领域模型转换为 DTO。
func toProtoTenantConfig(c *biz.TenantConfig) *configv1.TenantConfig {
	return &configv1.TenantConfig{
		Id:        c.ID,
		Status:    &c.Status,
		Name:      c.Name,
		ConfigKey: c.ConfigKey,
		Value:     c.Value,
		ValueType: c.ValueType,
		Remark:    c.Remark,
		CreatedBy: c.CreatedBy,
		UpdatedBy: c.UpdatedBy,
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: tsPBPtr(c.UpdatedAt),
		DeletedAt: tsPBPtr(c.DeletedAt),
		TenantId:  c.TenantID,
	}
}

// FeatureFlagService 是功能开关（sys_feature_flag）的传输层适配器。
// 功能开关是租户内资源：租户边界由存储层 RLS 承担，读写方法一律不带
// Super；tenant_id 只读，由服务端按认证身份落值（请求中的值被忽略）。
type FeatureFlagService struct {
	configv1.UnimplementedFeatureFlagServiceServer
	uc *biz.FeatureFlagUsecase
}

// NewFeatureFlagService 构造功能开关服务。
func NewFeatureFlagService(uc *biz.FeatureFlagUsecase) *FeatureFlagService {
	return &FeatureFlagService{uc: uc}
}

// CreateFeatureFlag 创建功能开关；flag_key 与 tenant_id 不取请求值
// （前者创建时写入、后者由服务端按认证身份落值）。is_enabled 未设置即
// false，显式设置按值透传。
func (s *FeatureFlagService) CreateFeatureFlag(ctx context.Context, req *configv1.CreateFeatureFlagRequest) (*configv1.FeatureFlag, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetFeatureFlag()
	if in == nil {
		return nil, biz.ErrFeatureFlagInvalidArgument
	}
	status, ok := normalizeConfigStatus(in.Status)
	if !ok {
		return nil, biz.ErrFeatureFlagInvalidArgument
	}
	out, err := s.uc.CreateFeatureFlag(ctx, auth, &biz.FeatureFlag{
		Status:      status,
		Name:        in.GetName(),
		FlagKey:     in.GetFlagKey(),
		Description: optionalString(in.Description),
		IsEnabled:   in.GetIsEnabled(),
		Remark:      optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoFeatureFlag(out), nil
}

// GetFeatureFlag 按 id 查询功能开关。
func (s *FeatureFlagService) GetFeatureFlag(ctx context.Context, req *configv1.GetFeatureFlagRequest) (*configv1.FeatureFlag, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetFeatureFlag(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoFeatureFlag(out), nil
}

// ListFeatureFlags 按动态查询协议返回一页功能开关；协议非法映射为
// INVALID_ARGUMENT。
func (s *FeatureFlagService) ListFeatureFlags(ctx context.Context, req *configv1.ListFeatureFlagsRequest) (*configv1.FeatureFlagSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrFeatureFlagInvalidArgument, err)
	}
	out, err := s.uc.ListFeatureFlags(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*configv1.FeatureFlag, 0, len(out.Items))
	for _, f := range out.Items {
		items = append(items, toProtoFeatureFlag(f))
	}
	return &configv1.FeatureFlagSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateFeatureFlag 覆盖可变字段（name、description、is_enabled、remark、
// status）；status 必填，flag_key 与 tenant_id 在输入中被忽略（身份/归属
// 列）。
func (s *FeatureFlagService) UpdateFeatureFlag(ctx context.Context, req *configv1.UpdateFeatureFlagRequest) (*configv1.FeatureFlag, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetFeatureFlag()
	if in == nil || in.Status == nil {
		return nil, biz.ErrFeatureFlagInvalidArgument
	}
	out, err := s.uc.UpdateFeatureFlag(ctx, auth, &biz.FeatureFlag{
		ID:          in.GetId(),
		Status:      *in.Status,
		Name:        in.GetName(),
		Description: optionalString(in.Description),
		IsEnabled:   in.GetIsEnabled(),
		Remark:      optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoFeatureFlag(out), nil
}

// DeleteFeatureFlag 软删除功能开关。
func (s *FeatureFlagService) DeleteFeatureFlag(ctx context.Context, req *configv1.DeleteFeatureFlagRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteFeatureFlag(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// TranslationService 是通用翻译（sys_translation）的传输层适配器。翻译是
// 租户内资源：租户边界由存储层 RLS 承担，读写方法一律不带 Super；
// tenant_id 只读，由服务端按认证身份落值（请求中的值被忽略）。
type TranslationService struct {
	configv1.UnimplementedTranslationServiceServer
	uc *biz.TranslationUsecase
}

// NewTranslationService 构造翻译服务。
func NewTranslationService(uc *biz.TranslationUsecase) *TranslationService {
	return &TranslationService{uc: uc}
}

// CreateTranslation 创建翻译；身份四元组创建时写入，tenant_id 不取请求
// 值；value 原样透传（trim 与必填校验在用例层落地）。
func (s *TranslationService) CreateTranslation(ctx context.Context, req *configv1.CreateTranslationRequest) (*configv1.Translation, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTranslation()
	if in == nil {
		return nil, biz.ErrTranslationInvalidArgument
	}
	status, ok := normalizeConfigStatus(in.Status)
	if !ok {
		return nil, biz.ErrTranslationInvalidArgument
	}
	out, err := s.uc.CreateTranslation(ctx, auth, &biz.Translation{
		Status:     status,
		TableName:  in.GetTableName(),
		RowID:      in.GetRowId(),
		ColumnName: in.GetColumnName(),
		Locale:     in.GetLocale(),
		Value:      in.GetValue(),
		Remark:     optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTranslation(out), nil
}

// GetTranslation 按 id 查询翻译。
func (s *TranslationService) GetTranslation(ctx context.Context, req *configv1.GetTranslationRequest) (*configv1.Translation, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetTranslation(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoTranslation(out), nil
}

// ListTranslations 按动态查询协议返回一页翻译；协议非法映射为
// INVALID_ARGUMENT。
func (s *TranslationService) ListTranslations(ctx context.Context, req *configv1.ListTranslationsRequest) (*configv1.TranslationSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrTranslationInvalidArgument, err)
	}
	out, err := s.uc.ListTranslations(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*configv1.Translation, 0, len(out.Items))
	for _, t := range out.Items {
		items = append(items, toProtoTranslation(t))
	}
	return &configv1.TranslationSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateTranslation 覆盖可变字段（status、value、remark）；status 必填，
// 身份四元组与 tenant_id 在输入中被忽略；value 原样透传。
func (s *TranslationService) UpdateTranslation(ctx context.Context, req *configv1.UpdateTranslationRequest) (*configv1.Translation, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetTranslation()
	if in == nil || in.Status == nil {
		return nil, biz.ErrTranslationInvalidArgument
	}
	out, err := s.uc.UpdateTranslation(ctx, auth, &biz.Translation{
		ID:     in.GetId(),
		Status: *in.Status,
		Value:  in.GetValue(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoTranslation(out), nil
}

// DeleteTranslation 软删除翻译。
func (s *TranslationService) DeleteTranslation(ctx context.Context, req *configv1.DeleteTranslationRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteTranslation(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoFeatureFlag 将功能开关领域模型转换为 DTO。
func toProtoFeatureFlag(f *biz.FeatureFlag) *configv1.FeatureFlag {
	return &configv1.FeatureFlag{
		Id:          f.ID,
		Status:      &f.Status,
		Name:        f.Name,
		FlagKey:     f.FlagKey,
		Description: f.Description,
		IsEnabled:   &f.IsEnabled,
		Remark:      f.Remark,
		CreatedBy:   f.CreatedBy,
		UpdatedBy:   f.UpdatedBy,
		CreatedAt:   timestamppb.New(f.CreatedAt),
		UpdatedAt:   tsPBPtr(f.UpdatedAt),
		DeletedAt:   tsPBPtr(f.DeletedAt),
		TenantId:    f.TenantID,
	}
}

// toProtoTranslation 将翻译领域模型转换为 DTO。
func toProtoTranslation(t *biz.Translation) *configv1.Translation {
	return &configv1.Translation{
		Id:         t.ID,
		Status:     &t.Status,
		TableName:  t.TableName,
		RowId:      t.RowID,
		ColumnName: t.ColumnName,
		Locale:     t.Locale,
		Value:      t.Value,
		Remark:     t.Remark,
		CreatedBy:  t.CreatedBy,
		UpdatedBy:  t.UpdatedBy,
		CreatedAt:  timestamppb.New(t.CreatedAt),
		UpdatedAt:  tsPBPtr(t.UpdatedAt),
		DeletedAt:  tsPBPtr(t.DeletedAt),
		TenantId:   t.TenantID,
	}
}

// normalizeConfigStatus 归一化配置族创建入参的状态：仅 nil（未设置）映射
// 为 biz 的未指定哨兵（用例落缺省启用）；显式取值只接受 0=禁用与 1=启用，
// 其余（含显式 -1 与 2）返回 false 由调用方映射为各自域的 InvalidArgument
// ——哨兵只是传输层「未设置」的表示，不是合法请求值。
func normalizeConfigStatus(status *int32) (int32, bool) {
	if status == nil {
		return biz.ConfigStatusUnspecified, true
	}
	if *status != biz.ConfigStatusDisabled && *status != biz.ConfigStatusEnabled {
		return biz.ConfigStatusUnspecified, false
	}
	return *status, true
}

// optionalValue 归一化配置值：nil（未设置）透传 nil；显式空串是有效的
// 配置值（与「未设置」语义不同），原样保留。remark 等展示文本仍走
// optionalString（空串归一为未设置）。
func optionalValue(value *string) *string {
	if value == nil {
		return nil
	}
	return value
}
