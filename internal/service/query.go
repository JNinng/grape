package service

import (
	"context"
	"errors"
	"fmt"

	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// QueryService 是动态查询能力出口的传输适配器：返回资源 List schema 供
// 前端渲染查询组件，委托给 biz 用例，不实现业务规则。
type QueryService struct {
	queryv1.UnimplementedQueryServiceServer
	uc *biz.QueryUsecase
}

// NewQueryService 构造查询能力出口服务。
func NewQueryService(uc *biz.QueryUsecase) *QueryService {
	return &QueryService{uc: uc}
}

// GetListSchema 返回资源的 List 能力 schema。schema 是确定性纯函数，
// 内容 hash 可作 ETag；资源未收录映射为 INVALID_ARGUMENT。
func (s *QueryService) GetListSchema(ctx context.Context, req *queryv1.GetListSchemaRequest) (*queryv1.ListSchema, error) {
	if _, err := authOf(ctx); err != nil {
		return nil, err
	}
	schema, err := s.uc.ListSchema(req.GetResource())
	if err != nil {
		return nil, err
	}
	return toProtoListSchema(schema)
}

// toProtoListSchema 将存储层 schema 契约转换为 DTO。字段键、操作符、枚举名
// 与展示注解（desc/options）直接透传，前端渲染的唯一事实源是存储层 schema。
func toProtoListSchema(s dynq.ResourceSchema) (*queryv1.ListSchema, error) {
	fields := make([]*queryv1.ListSchemaField, 0, len(s.Fields))
	for _, f := range s.Fields {
		pf := &queryv1.ListSchemaField{
			Field:      f.Field,
			Label:      f.Label,
			DataType:   f.DataType,
			Nullable:   f.Nullable,
			Sortable:   f.Sortable,
			Visibility: f.Visibility,
			Ops:        f.Ops,
			EnumValues: f.Enum,
			Desc:       f.Desc,
		}
		if f.API != nil {
			pf.Api = &queryv1.ListSchemaFieldAPI{
				Endpoint: f.API.Endpoint,
				ValueKey: f.API.ValueKey,
				LabelKey: f.API.LabelKey,
				Mode:     f.API.Mode,
			}
		}
		for _, o := range f.Options {
			v, err := structpb.NewValue(o.Value)
			if err != nil {
				return nil, fmt.Errorf("unsupported option value for field %q: %w", f.Field, err)
			}
			pf.Options = append(pf.Options, &queryv1.ListSchemaFieldOption{Value: v, Label: o.Label})
		}
		fields = append(fields, pf)
	}
	sorts := make([]*queryv1.ListSchemaSort, 0, len(s.DefaultSort))
	for _, o := range s.DefaultSort {
		sorts = append(sorts, &queryv1.ListSchemaSort{Field: string(o.Field), Desc: o.Desc})
	}
	return &queryv1.ListSchema{
		Resource:    s.Resource,
		Hash:        s.Hash(),
		Fields:      fields,
		DefaultSort: sorts,
		Pagination: &queryv1.ListSchemaPagination{
			DefaultPageSize: int32(s.Pagination.DefaultPageSize),
			MaxPageSize:     int32(s.Pagination.MaxPageSize),
			Modes:           s.Pagination.Modes,
			IncludeTotal:    s.Pagination.IncludeTotal,
		},
	}, nil
}

// errPageAfterConflict 表示分页同时指定了 page 与 after；二者互斥。
var errPageAfterConflict = errors.New("page and after are mutually exclusive")

// enumAliases 描述资源枚举字段的线上枚举名到存储枚举值的映射。
// key 为查询字段名（小驼峰），value 为该字段的合法线上枚举名。
type enumAliases map[string]map[string]string

// convertQuery 将 proto 查询协议转换为 dynq 运行时形态。
// 枚举字段的过滤值统一使用线上枚举名，这里归一化为存储层取值。
func convertQuery(in *queryv1.Query, aliases enumAliases) (*dynq.Query, error) {
	if in == nil {
		return &dynq.Query{}, nil
	}
	q := &dynq.Query{Fields: in.GetFields()}
	for _, c := range in.GetWhere() {
		value, err := normalizeEnumValue(aliases, c.GetField(), c.GetOp(), valueOf(c.GetValue()))
		if err != nil {
			return nil, err
		}
		q.Where = append(q.Where, dynq.Condition{
			Field:      c.GetField(),
			Op:         dynq.Op(c.GetOp()),
			Value:      value,
			RightValue: valueOf(c.GetRightValue()),
		})
	}
	for _, s := range in.GetSort() {
		q.Sort = append(q.Sort, dynq.SortItem{Field: s.GetField(), Order: s.GetOrder()})
	}
	if p := in.GetPagination(); p != nil {
		if p.After != nil && *p.After != "" && p.Page != nil {
			return nil, errPageAfterConflict
		}
		pg := &dynq.Pagination{}
		if p.Page != nil {
			v := int(*p.Page)
			pg.Page = &v
		}
		if p.PageSize != nil {
			v := int(*p.PageSize)
			pg.PageSize = &v
		}
		if p.After != nil {
			v := *p.After
			pg.After = &v
		}
		if p.IncludeTotal != nil {
			v := *p.IncludeTotal
			pg.IncludeTotal = &v
		}
		q.Pagination = pg
	}
	return q, nil
}

// normalizeEnumValue 将枚举字段的过滤值从线上枚举名归一化为存储取值。
// 枚举字段只接受字符串形式的符号名；数字、未知名称与非期望形态都视为
// 非法输入。非枚举字段与不使用取值的操作符原样返回。
func normalizeEnumValue(aliases enumAliases, field, op string, value any) (any, error) {
	mapping, ok := aliases[field]
	if !ok || value == nil {
		return value, nil
	}
	switch op {
	case "eq", "neq", "in", "not_in":
	default:
		return value, nil
	}
	switch v := value.(type) {
	case string:
		stored, ok := mapping[v]
		if !ok {
			return nil, fmt.Errorf("unknown enum value %q for field %q", v, field)
		}
		return stored, nil
	case []any:
		out := make([]any, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("field %q expects enum string at index %d", field, i)
			}
			stored, ok := mapping[s]
			if !ok {
				return nil, fmt.Errorf("unknown enum value %q for field %q", s, field)
			}
			out = append(out, stored)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("field %q expects enum string, got %T", field, value)
	}
}

// invalidQueryError 将查询转换错误包装为资源自身的 INVALID_ARGUMENT，
// 保留具体原因。
func invalidQueryError(target, cause error) error {
	be, ok := target.(*kratoserrors.Error)
	if !ok {
		return target
	}
	return kratoserrors.New(int(be.Code), be.Reason, cause.Error())
}

// valueOf 将 protobuf JSON 值转换为查询解析器消费的动态 any 形态：
// 数字以 float64 到达、数组以 []any 到达，与解析器面向的 JSON 协议一致。
func valueOf(v *structpb.Value) any {
	if v == nil {
		return nil
	}
	return v.AsInterface()
}

// pageOf 报告分页信封中的当前页码：offset 模式返回请求页码（缺省 1），
// 游标模式返回 0。
func pageOf(q *queryv1.Query) int32 {
	p := q.GetPagination()
	if p == nil {
		return 1
	}
	if p.After != nil && *p.After != "" {
		return 0
	}
	if p.Page != nil {
		return *p.Page
	}
	return 1
}
