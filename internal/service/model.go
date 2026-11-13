package service

import (
	"context"

	modelv1 "grape/api/model/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ModelService 是模型目录资源的传输层适配器：DTO ↔ DO 转换，委托 biz
// 用例。密钥只写不读：响应只返回 hasApiKey，解析与解密在 biz/data 层。
type ModelService struct {
	modelv1.UnimplementedModelServiceServer
	uc *biz.ModelUsecase
}

// NewModelService 构造模型目录服务。
func NewModelService(uc *biz.ModelUsecase) *ModelService {
	return &ModelService{uc: uc}
}

// CreateModel 创建租户模型行。
func (s *ModelService) CreateModel(ctx context.Context, req *modelv1.CreateModelRequest) (*modelv1.Model, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetModel()
	if in == nil {
		return nil, biz.ErrModelInvalidArgument
	}
	out, err := s.uc.CreateModel(ctx, auth, modelFromProto(in), req.GetApiKey())
	if err != nil {
		return nil, err
	}
	return toProtoModel(out), nil
}

// GetModel 按 id 查询模型。
func (s *ModelService) GetModel(ctx context.Context, req *modelv1.GetModelRequest) (*modelv1.Model, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetModel(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoModel(out), nil
}

// ListModels 返回可见模型清单。
func (s *ModelService) ListModels(ctx context.Context, req *modelv1.ListModelsRequest) (*modelv1.ModelSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.ListModels(ctx, auth, req.GetKind())
	if err != nil {
		return nil, err
	}
	items := make([]*modelv1.Model, 0, len(out))
	for _, m := range out {
		items = append(items, toProtoModel(m))
	}
	return &modelv1.ModelSet{Items: items}, nil
}

// UpdateModel 覆盖租户模型行可变字段；status 必填。
func (s *ModelService) UpdateModel(ctx context.Context, req *modelv1.UpdateModelRequest) (*modelv1.Model, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetModel()
	if in == nil || in.Status == nil {
		return nil, biz.ErrModelInvalidArgument
	}
	out, err := s.uc.UpdateModel(ctx, auth, modelUpdateFromProto(req.GetId(), in), req.ApiKey, req.GetClearApiKey())
	if err != nil {
		return nil, err
	}
	return toProtoModel(out), nil
}

// DeleteModel 软删除租户模型行。
func (s *ModelService) DeleteModel(ctx context.Context, req *modelv1.DeleteModelRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteModel(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// CreateModelSuper 创建平台模型行（super 写面）。
func (s *ModelService) CreateModelSuper(ctx context.Context, req *modelv1.CreateModelSuperRequest) (*modelv1.Model, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetModel()
	if in == nil {
		return nil, biz.ErrModelInvalidArgument
	}
	out, err := s.uc.CreateModelPlatform(ctx, auth, modelFromProto(in), req.GetApiKey())
	if err != nil {
		return nil, err
	}
	return toProtoModel(out), nil
}

// UpdateModelSuper 覆盖平台模型行可变字段（super 写面）；status 必填。
func (s *ModelService) UpdateModelSuper(ctx context.Context, req *modelv1.UpdateModelSuperRequest) (*modelv1.Model, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetModel()
	if in == nil || in.Status == nil {
		return nil, biz.ErrModelInvalidArgument
	}
	out, err := s.uc.UpdateModelPlatform(ctx, auth, modelUpdateFromProto(req.GetId(), in), req.ApiKey, req.GetClearApiKey())
	if err != nil {
		return nil, err
	}
	return toProtoModel(out), nil
}

// DeleteModelSuper 软删除平台模型行（super 写面）。
func (s *ModelService) DeleteModelSuper(ctx context.Context, req *modelv1.DeleteModelSuperRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteModelPlatform(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func modelFromProto(in *modelv1.Model) *biz.ModelCfg {
	return &biz.ModelCfg{
		ID:              in.GetId(),
		Status:          normalizeModelStatus(in.Status),
		Kind:            in.GetKind(),
		Name:            in.GetName(),
		ModelKey:        in.GetModelKey(),
		Provider:        in.GetProvider(),
		BaseURL:         optionalString(in.BaseUrl),
		Model:           in.GetModel(),
		Dimension:       in.Dimension,
		TimeoutMs:       in.TimeoutMs,
		ContextWindow:   in.ContextWindow,
		MaxOutputTokens: in.MaxOutputTokens,
		APIKeyEnv:       optionalString(in.ApiKeyEnv),
		Remark:          optionalString(in.Remark),
	}
}

// modelUpdateFromProto 把更新变更集映射为领域入参：只填可变字段与目标
// ID，绝不设置 Kind / ModelKey——两者是创建后不可变的身份列，由 biz 以
// 锁定行的存量值回填。与创建路径的 modelFromProto 分开，避免更新协议
// 误带身份列。
func modelUpdateFromProto(id int64, in *modelv1.ModelUpdateSpec) *biz.ModelCfg {
	return &biz.ModelCfg{
		ID:              id,
		Status:          normalizeModelStatus(in.Status),
		Name:            in.GetName(),
		Provider:        in.GetProvider(),
		BaseURL:         optionalString(in.BaseUrl),
		Model:           in.GetModel(),
		Dimension:       in.Dimension,
		TimeoutMs:       in.TimeoutMs,
		ContextWindow:   in.ContextWindow,
		MaxOutputTokens: in.MaxOutputTokens,
		APIKeyEnv:       optionalString(in.ApiKeyEnv),
		Remark:          optionalString(in.Remark),
	}
}

func toProtoModel(m *biz.ModelCfg) *modelv1.Model {
	return &modelv1.Model{
		Id:              m.ID,
		Platform:        m.Platform,
		Status:          &m.Status,
		Kind:            m.Kind,
		Name:            m.Name,
		ModelKey:        m.ModelKey,
		Provider:        m.Provider,
		BaseUrl:         m.BaseURL,
		Model:           m.Model,
		Dimension:       m.Dimension,
		TimeoutMs:       m.TimeoutMs,
		ContextWindow:   m.ContextWindow,
		MaxOutputTokens: m.MaxOutputTokens,
		HasApiKey:       m.HasAPIKey,
		ApiKeyEnv:       m.APIKeyEnv,
		Remark:          m.Remark,
		CreatedBy:       m.CreatedBy,
		UpdatedBy:       m.UpdatedBy,
		CreatedAt:       timestamppb.New(m.CreatedAt),
		UpdatedAt:       tsPBPtr(m.UpdatedAt),
		DeletedAt:       tsPBPtr(m.DeletedAt),
	}
}

// normalizeModelStatus 归一化创建入参状态：optional 缺失映射为未指定哨兵。
func normalizeModelStatus(status *int32) int32 {
	if status == nil {
		return biz.ModelStatusUnspecified
	}
	return *status
}
