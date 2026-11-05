package service

import (
	"context"
	"errors"
	"testing"

	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	dynq "github.com/jninng/sqlc_gen_dynamic/dynq"
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

func stubListSchema() dynq.ResourceSchema {
	return dynq.ResourceSchema{
		Resource: "sys_user",
		Fields: []dynq.FieldSchema{
			{
				Field:      "status",
				Label:      "状态",
				DataType:   "enum",
				Sortable:   true,
				Hideable:   true,
				Selectable: true,
				Ops:        []string{"eq", "in"},
				Enum:       []string{"enabled", "disabled"},
				Desc:       "0：禁用、1：启用",
				Options: []dynq.FieldOption{
					{Value: int64(0), Label: "disabled"},
					{Value: int64(1), Label: "enabled"},
				},
			},
			{
				Field: "ownerUserId",
				Label: "归属用户 ID",
				Ops:   []string{"eq"},
				API:   &dynq.FieldAPI{Endpoint: "/v1/users/list", ValueKey: "id", LabelKey: "nickname", Mode: "multi"},
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
	if len(out.GetFields()) != 2 {
		t.Fatalf("fields = %d, want 2", len(out.GetFields()))
	}
	status := out.GetFields()[0]
	if status.GetField() != "status" || status.GetEnumValues() == nil {
		t.Fatalf("status field = %+v, want enum values carried", status)
	}
	if status.GetDesc() != "0：禁用、1：启用" {
		t.Fatalf("status desc = %q, want carried", status.GetDesc())
	}
	opts := status.GetOptions()
	if len(opts) != 2 || opts[0].GetValue().GetNumberValue() != 0 || opts[0].GetLabel() != "disabled" || opts[1].GetValue().GetNumberValue() != 1 || opts[1].GetLabel() != "enabled" {
		t.Fatalf("status options = %+v, want value/label pairs carried", opts)
	}
	api := out.GetFields()[1].GetApi()
	if api.GetEndpoint() != "/v1/users/list" || api.GetValueKey() != "id" || api.GetLabelKey() != "nickname" || api.GetMode() != "multi" {
		t.Fatalf("api = %+v, want endpoint/valueKey/labelKey/mode carried", api)
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
