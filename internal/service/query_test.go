package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeSchemaProvider 记录请求的资源名，返回固定 schema 或错误。
type fakeSchemaProvider struct {
	gotResource string
	schema      dynq.ResourceSchema
	err         error
}

func (f *fakeSchemaProvider) ListSchema(resource string) (dynq.ResourceSchema, error) {
	f.gotResource = resource
	if f.err != nil {
		return dynq.ResourceSchema{}, f.err
	}
	return f.schema, nil
}

// stubListSchema 覆盖生成器真实可产出的形态：integer + int64 options、
// string + string options、无注解字段，以及 API 选项源的转换分支。
func stubListSchema() dynq.ResourceSchema {
	return dynq.ResourceSchema{
		Resource: "sys_user",
		Fields: []dynq.FieldSchema{
			{
				Field:      "status",
				Label:      "状态",
				DataType:   "integer",
				Sortable:   true,
				Visibility: string(dynq.VisibilityOptional),
				Ops:        []string{"eq", "in"},
				Desc:       "0：禁用、1：启用",
				Options: []dynq.FieldOption{
					{Value: int64(7), Label: "禁用"},
					{Value: int64(9), Label: "启用"},
				},
			},
			{
				Field:      "valueType",
				Label:      "值类型",
				DataType:   "string",
				Visibility: string(dynq.VisibilityOptional),
				Ops:        []string{"eq"},
				Options: []dynq.FieldOption{
					{Value: "string", Label: "字符串"},
					{Value: "integer", Label: "整数"},
				},
			},
			{
				Field:      "name",
				Label:      "名称",
				DataType:   "string",
				Visibility: string(dynq.VisibilityOptional),
				Ops:        []string{"contains"},
			},
			{
				Field:      "ownerUserId",
				Label:      "归属用户 ID",
				Visibility: string(dynq.VisibilityHidden),
				Ops:        []string{"eq"},
				API:        &dynq.FieldAPI{Endpoint: "/v1/users/list", ValueKey: "id", LabelKey: "nickname", Mode: "multi"},
			},
		},
		DefaultSort: []dynq.OrderBy{{Field: "createdAt", Desc: true}},
		Pagination: dynq.PaginationSchema{
			DefaultPageSize: 20,
			MaxPageSize:     100,
			Modes:           []string{"offset", "cursor"},
			IncludeTotal:    true,
		},
	}
}

func TestQueryServiceGetListSchemaConvertsContract(t *testing.T) {
	provider := &fakeSchemaProvider{schema: stubListSchema()}
	svc := NewQueryService(biz.NewQueryUsecase(provider))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetListSchema(biz.WithIdentity(context.Background(), auth), &queryv1.GetListSchemaRequest{Resource: "sys_user"})
	if err != nil {
		t.Fatalf("get list schema: %v", err)
	}
	if provider.gotResource != "sys_user" {
		t.Fatalf("provider resource = %q, want sys_user", provider.gotResource)
	}
	if out.GetResource() != "sys_user" || out.GetHash() == "" {
		t.Fatalf("resource/hash = %q/%q, want non-empty pair", out.GetResource(), out.GetHash())
	}
	if len(out.GetFields()) != 4 {
		t.Fatalf("fields = %d, want 4", len(out.GetFields()))
	}

	t.Run("integer options keep number kind", func(t *testing.T) {
		status := out.GetFields()[0]
		if status.GetField() != "status" || status.GetDesc() != "0：禁用、1：启用" {
			t.Fatalf("status field/desc = %+v, want field status with carried desc", status)
		}
		if status.GetVisibility() != string(dynq.VisibilityOptional) {
			t.Fatalf("status visibility = %q, want optional", status.GetVisibility())
		}
		opts := status.GetOptions()
		if len(opts) != 2 {
			t.Fatalf("status options = %d, want 2", len(opts))
		}
		if got := numberOption(t, opts[0]); got != 7 {
			t.Fatalf("status option 0 value = %v, want 7", got)
		}
		if opts[0].GetLabel() != "禁用" {
			t.Fatalf("status option 0 label = %q, want 禁用", opts[0].GetLabel())
		}
		if got := numberOption(t, opts[1]); got != 9 {
			t.Fatalf("status option 1 value = %v, want 9", got)
		}
		if opts[1].GetLabel() != "启用" {
			t.Fatalf("status option 1 label = %q, want 启用", opts[1].GetLabel())
		}
	})

	t.Run("string options keep string kind", func(t *testing.T) {
		valueType := out.GetFields()[1]
		opts := valueType.GetOptions()
		if len(opts) != 2 {
			t.Fatalf("valueType options = %d, want 2", len(opts))
		}
		if got := stringOption(t, opts[0]); got != "string" {
			t.Fatalf("valueType option 0 value = %q, want string", got)
		}
		if opts[0].GetLabel() != "字符串" {
			t.Fatalf("valueType option 0 label = %q, want 字符串", opts[0].GetLabel())
		}
		if got := stringOption(t, opts[1]); got != "integer" {
			t.Fatalf("valueType option 1 value = %q, want integer", got)
		}
		if opts[1].GetLabel() != "整数" {
			t.Fatalf("valueType option 1 label = %q, want 整数", opts[1].GetLabel())
		}
	})

	t.Run("field without annotations keeps empty desc and nil options", func(t *testing.T) {
		name := out.GetFields()[2]
		if name.GetDesc() != "" {
			t.Fatalf("name desc = %q, want empty", name.GetDesc())
		}
		if name.GetOptions() != nil {
			t.Fatalf("name options = %+v, want nil", name.GetOptions())
		}
	})

	api := out.GetFields()[3].GetApi()
	if api.GetEndpoint() != "/v1/users/list" || api.GetValueKey() != "id" || api.GetLabelKey() != "nickname" || api.GetMode() != "multi" {
		t.Fatalf("api = %+v, want endpoint/valueKey/labelKey/mode carried", api)
	}
	if got := out.GetFields()[3].GetVisibility(); got != string(dynq.VisibilityHidden) {
		t.Fatalf("ownerUserId visibility = %q, want hidden", got)
	}
	sort := out.GetDefaultSort()
	if len(sort) != 1 || sort[0].GetField() != "createdAt" || !sort[0].GetDesc() {
		t.Fatalf("default_sort = %+v, want createdAt desc", sort)
	}
	pg := out.GetPagination()
	if pg.GetDefaultPageSize() != 20 || pg.GetMaxPageSize() != 100 || !pg.GetIncludeTotal() || len(pg.GetModes()) != 2 {
		t.Fatalf("pagination = %+v, want 20/100/offset+cursor/total", pg)
	}
}

func numberOption(t *testing.T, opt *queryv1.ListSchemaFieldOption) float64 {
	t.Helper()
	value, ok := opt.GetValue().GetKind().(*structpb.Value_NumberValue)
	if !ok {
		t.Fatalf("option value kind = %T, want number", opt.GetValue().GetKind())
	}
	return value.NumberValue
}

func stringOption(t *testing.T, opt *queryv1.ListSchemaFieldOption) string {
	t.Helper()
	value, ok := opt.GetValue().GetKind().(*structpb.Value_StringValue)
	if !ok {
		t.Fatalf("option value kind = %T, want string", opt.GetValue().GetKind())
	}
	return value.StringValue
}

func TestQueryServiceRejectsUnsupportedOptionValue(t *testing.T) {
	provider := &fakeSchemaProvider{schema: dynq.ResourceSchema{
		Resource: "sys_user",
		Fields: []dynq.FieldSchema{{
			Field:   "bad",
			Options: []dynq.FieldOption{{Value: struct{}{}, Label: "bad"}},
		}},
	}}
	svc := NewQueryService(biz.NewQueryUsecase(provider))

	_, err := svc.GetListSchema(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &queryv1.GetListSchemaRequest{Resource: "sys_user"})
	if err == nil {
		t.Fatal("unsupported option value must be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported option value") {
		t.Fatalf("err = %v, want unsupported option value", err)
	}
}

func TestQueryServiceMapsUnknownResource(t *testing.T) {
	provider := &fakeSchemaProvider{err: biz.ErrQueryResourceUnknown}
	svc := NewQueryService(biz.NewQueryUsecase(provider))

	_, err := svc.GetListSchema(biz.WithIdentity(context.Background(), biz.Identity{}), &queryv1.GetListSchemaRequest{Resource: "basic"})
	if !errors.Is(err, biz.ErrQueryResourceUnknown) {
		t.Fatalf("err = %v, want ErrQueryResourceUnknown", err)
	}
}

func TestQueryServiceFailsClosedWithoutIdentity(t *testing.T) {
	provider := &fakeSchemaProvider{schema: stubListSchema()}
	svc := NewQueryService(biz.NewQueryUsecase(provider))

	_, err := svc.GetListSchema(context.Background(), &queryv1.GetListSchemaRequest{Resource: "sys_user"})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if provider.gotResource != "" {
		t.Fatalf("provider called with %q, want untouched", provider.gotResource)
	}
}
