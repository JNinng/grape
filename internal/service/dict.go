package service

import (
	"context"

	dictv1 "grape/api/dict/v1"
	"grape/internal/biz"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DictService 是字典资源的传输层适配器：DTO ↔ DO 转换、动态查询协议转换，
// 委托给 biz 用例，不实现业务规则。字典是租户内公共数据（类型 + 数据两级），
// 端点全部受认证保护，租户边界由存储层 RLS 承担。
type DictService struct {
	dictv1.UnimplementedDictServiceServer
	uc *biz.DictUsecase
}

// NewDictService 构造字典服务。
func NewDictService(uc *biz.DictUsecase) *DictService {
	return &DictService{uc: uc}
}

// CreateDictType 创建字典类型。
func (s *DictService) CreateDictType(ctx context.Context, req *dictv1.CreateDictTypeRequest) (*dictv1.DictType, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDictType()
	if in == nil {
		return nil, biz.ErrDictTypeInvalidArgument
	}
	out, err := s.uc.CreateDictType(ctx, auth, &biz.DictType{
		Status: normalizeDictStatus(in.Status),
		Name:   in.GetName(),
		Code:   in.GetCode(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDictType(out), nil
}

// GetDictType 按 id 查询字典类型。
func (s *DictService) GetDictType(ctx context.Context, req *dictv1.GetDictTypeRequest) (*dictv1.DictType, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetDictType(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoDictType(out), nil
}

// ListDictTypes 按动态查询协议返回一页字典类型；协议非法映射为
// INVALID_ARGUMENT。
func (s *DictService) ListDictTypes(ctx context.Context, req *dictv1.ListDictTypesRequest) (*dictv1.DictTypeSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrDictTypeInvalidArgument, err)
	}
	out, err := s.uc.ListDictTypes(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*dictv1.DictType, 0, len(out.Items))
	for _, t := range out.Items {
		items = append(items, toProtoDictType(t))
	}
	return &dictv1.DictTypeSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateDictType 覆盖可变字段（name、status、remark）；status 必填
// （optional 传达禁用语义），code 在输入中被忽略。
func (s *DictService) UpdateDictType(ctx context.Context, req *dictv1.UpdateDictTypeRequest) (*dictv1.DictType, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDictType()
	if in == nil || in.Status == nil {
		return nil, biz.ErrDictTypeInvalidArgument
	}
	out, err := s.uc.UpdateDictType(ctx, auth, &biz.DictType{
		ID:     in.GetId(),
		Status: *in.Status,
		Name:   in.GetName(),
		Remark: optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDictType(out), nil
}

// DeleteDictType 软删除字典类型。
func (s *DictService) DeleteDictType(ctx context.Context, req *dictv1.DeleteDictTypeRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteDictType(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// CreateDictData 创建字典数据。
func (s *DictService) CreateDictData(ctx context.Context, req *dictv1.CreateDictDataRequest) (*dictv1.DictData, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDictData()
	if in == nil {
		return nil, biz.ErrDictDataInvalidArgument
	}
	out, err := s.uc.CreateDictData(ctx, auth, &biz.DictData{
		Status:     normalizeDictStatus(in.Status),
		DictTypeID: in.GetDictTypeId(),
		Label:      in.GetLabel(),
		Value:      in.GetValue(),
		ValueType:  in.GetValueType(),
		SortOrder:  in.GetSortOrder(),
		Remark:     optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDictData(out), nil
}

// GetDictData 按 id 查询字典数据。
func (s *DictService) GetDictData(ctx context.Context, req *dictv1.GetDictDataRequest) (*dictv1.DictData, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.uc.GetDictData(ctx, auth, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProtoDictData(out), nil
}

// ListDictDatas 按动态查询协议返回一页字典数据；协议非法映射为
// INVALID_ARGUMENT。
func (s *DictService) ListDictDatas(ctx context.Context, req *dictv1.ListDictDatasRequest) (*dictv1.DictDataSet, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	q, err := convertQuery(req.GetQuery(), nil)
	if err != nil {
		return nil, invalidQueryError(biz.ErrDictDataInvalidArgument, err)
	}
	out, err := s.uc.ListDictDatas(ctx, auth, q)
	if err != nil {
		return nil, err
	}
	items := make([]*dictv1.DictData, 0, len(out.Items))
	for _, d := range out.Items {
		items = append(items, toProtoDictData(d))
	}
	return &dictv1.DictDataSet{
		Items:      items,
		Total:      out.Total,
		HasMore:    out.HasMore,
		NextCursor: out.NextCursor,
		Page:       pageOf(req.GetQuery()),
	}, nil
}

// UpdateDictData 覆盖可变字段（label、status、sort_order、remark）；
// status 必填（optional 传达禁用语义），value/value_type 与 dict_type_id
// 在输入中被忽略。
func (s *DictService) UpdateDictData(ctx context.Context, req *dictv1.UpdateDictDataRequest) (*dictv1.DictData, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	in := req.GetDictData()
	if in == nil || in.Status == nil {
		return nil, biz.ErrDictDataInvalidArgument
	}
	out, err := s.uc.UpdateDictData(ctx, auth, &biz.DictData{
		ID:        in.GetId(),
		Status:    *in.Status,
		Label:     in.GetLabel(),
		SortOrder: in.GetSortOrder(),
		Remark:    optionalString(in.Remark),
	})
	if err != nil {
		return nil, err
	}
	return toProtoDictData(out), nil
}

// DeleteDictData 软删除字典数据。
func (s *DictService) DeleteDictData(ctx context.Context, req *dictv1.DeleteDictDataRequest) (*emptypb.Empty, error) {
	auth, err := authOf(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.uc.DeleteDictData(ctx, auth, req.GetId()); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// toProtoDictType 将领域模型转换为 DTO。
func toProtoDictType(t *biz.DictType) *dictv1.DictType {
	return &dictv1.DictType{
		Id:        t.ID,
		Status:    &t.Status,
		Name:      t.Name,
		Code:      t.Code,
		Remark:    t.Remark,
		CreatedBy: t.CreatedBy,
		UpdatedBy: t.UpdatedBy,
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: tsPBPtr(t.UpdatedAt),
		DeletedAt: tsPBPtr(t.DeletedAt),
	}
}

// toProtoDictData 将领域模型转换为 DTO。
func toProtoDictData(d *biz.DictData) *dictv1.DictData {
	return &dictv1.DictData{
		Id:         d.ID,
		Status:     &d.Status,
		DictTypeId: d.DictTypeID,
		Label:      d.Label,
		Value:      d.Value,
		ValueType:  d.ValueType,
		SortOrder:  d.SortOrder,
		Remark:     d.Remark,
		CreatedBy:  d.CreatedBy,
		UpdatedBy:  d.UpdatedBy,
		CreatedAt:  timestamppb.New(d.CreatedAt),
		UpdatedAt:  tsPBPtr(d.UpdatedAt),
		DeletedAt:  tsPBPtr(d.DeletedAt),
	}
}

// normalizeDictStatus 归一化字典两级创建入参的状态：optional 的缺失映射为
// biz 的未指定哨兵（用例落缺省启用）；显式取值原样透传——含 0=禁用，
// 不得并入缺省分支。
func normalizeDictStatus(status *int32) int32 {
	if status == nil {
		return biz.DictStatusUnspecified
	}
	return *status
}
