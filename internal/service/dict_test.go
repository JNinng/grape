package service

import (
	"context"
	"errors"
	"testing"
	"time"

	dictv1 "grape/api/dict/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakeDictRepo 捕获用例转发的字典 DO，供断言转换与身份透传。
type fakeDictRepo struct {
	biz.DictRepo

	gotAuth    biz.Identity
	callsCount int

	createdType  *biz.DictType
	updatedType  *biz.DictType
	deletedTypes []int64
	getTypeID    int64

	createdData  *biz.DictData
	updatedData  *biz.DictData
	deletedDatas []int64
	getDataID    int64

	typeList *biz.DictTypeList
	dataList *biz.DictDataList
}

func (f *fakeDictRepo) GetTypeByID(_ context.Context, auth biz.Identity, id int64) (*biz.DictType, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getTypeID = id
	return &biz.DictType{ID: id, Name: "typ", Code: "typ", Status: biz.DictStatusEnabled}, nil
}

func (f *fakeDictRepo) ListTypes(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.DictTypeList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.typeList, nil
}

func (f *fakeDictRepo) CreateType(_ context.Context, auth biz.Identity, t *biz.DictType) (*biz.DictType, error) {
	f.callsCount++
	f.gotAuth = auth
	f.createdType = t
	out := *t
	out.ID = 9
	return &out, nil
}

func (f *fakeDictRepo) UpdateType(_ context.Context, auth biz.Identity, t *biz.DictType) (*biz.DictType, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updatedType = t
	return t, nil
}

func (f *fakeDictRepo) SoftDeleteType(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedTypes = append(f.deletedTypes, id)
	return nil
}

func (f *fakeDictRepo) GetDataByID(_ context.Context, auth biz.Identity, id int64) (*biz.DictData, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getDataID = id
	return &biz.DictData{ID: id, Label: "lbl", Value: "v", ValueType: "string", Status: biz.DictStatusEnabled}, nil
}

func (f *fakeDictRepo) ListDatas(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.DictDataList, error) {
	f.callsCount++
	f.gotAuth = auth
	return f.dataList, nil
}

func (f *fakeDictRepo) CreateData(_ context.Context, auth biz.Identity, d *biz.DictData) (*biz.DictData, error) {
	f.callsCount++
	f.gotAuth = auth
	f.createdData = d
	out := *d
	out.ID = 10
	return &out, nil
}

func (f *fakeDictRepo) UpdateData(_ context.Context, auth biz.Identity, d *biz.DictData) (*biz.DictData, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updatedData = d
	return d, nil
}

func (f *fakeDictRepo) SoftDeleteData(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deletedDatas = append(f.deletedDatas, id)
	return nil
}

func TestDictServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetDictType(biz.WithIdentity(context.Background(), auth), &dictv1.GetDictTypeRequest{Id: 5})
	if err != nil {
		t.Fatalf("get dict type: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("dict type id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestDictServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))

	_, err := svc.GetDictType(context.Background(), &dictv1.GetDictTypeRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called without identity, calls = %d", repo.callsCount)
	}
}

func TestDictServiceCreateDictTypeDefaultsMissingStatusToEnabled(t *testing.T) {
	// optional status 缺失走未指定哨兵路径（用例落缺省启用），而非 0=禁用；
	// 仓储捕获的是用例落定后的值。
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateDictType(biz.WithIdentity(context.Background(), auth), &dictv1.CreateDictTypeRequest{
		DictType: &dictv1.DictType{Name: "状态", Code: "sys_status"},
	})
	if err != nil {
		t.Fatalf("create dict type: %v", err)
	}
	if repo.createdType == nil || repo.createdType.Status != biz.DictStatusEnabled {
		t.Fatalf("repo status = %+v, want enabled", repo.createdType)
	}
}

func TestDictServiceCreateDictTypeKeepsExplicitDisabled(t *testing.T) {
	zero := int32(0)
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.CreateDictType(biz.WithIdentity(context.Background(), auth), &dictv1.CreateDictTypeRequest{
		DictType: &dictv1.DictType{Name: "paused", Code: "paused", Status: &zero},
	})
	if err != nil {
		t.Fatalf("create dict type: %v", err)
	}
	if repo.createdType == nil || repo.createdType.Status != biz.DictStatusDisabled {
		t.Fatalf("repo status = %+v, want disabled", repo.createdType)
	}
}

func TestDictServiceUpdateDictTypeRequiresStatus(t *testing.T) {
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.UpdateDictType(biz.WithIdentity(context.Background(), auth), &dictv1.UpdateDictTypeRequest{
		DictType: &dictv1.DictType{Id: 1, Name: "typ"},
	})
	if !errors.Is(err, biz.ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo must not be called, calls = %d", repo.callsCount)
	}
}

func TestDictServiceUpdateDictTypeDropsCodeInput(t *testing.T) {
	// code 是身份列：更新输入中被忽略，转发的 DO 不携带 code。
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateDictType(biz.WithIdentity(context.Background(), auth), &dictv1.UpdateDictTypeRequest{
		DictType: &dictv1.DictType{Id: 1, Name: "typ", Code: "hacked", Status: &one},
	})
	if err != nil {
		t.Fatalf("update dict type: %v", err)
	}
	if repo.updatedType == nil || repo.updatedType.Code != "" {
		t.Fatalf("repo updated code = %+v, want empty (dropped)", repo.updatedType)
	}
}

func TestDictServiceListDictTypesReturnsEnvelope(t *testing.T) {
	total := int64(1)
	repo := &fakeDictRepo{typeList: &biz.DictTypeList{
		Items:      []*biz.DictType{{ID: 5, Name: "typ", Code: "typ", Status: biz.DictStatusEnabled, CreatedAt: time.Unix(1700000000, 0).UTC()}},
		Total:      &total,
		HasMore:    true,
		NextCursor: "cur",
	}}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(2)

	out, err := svc.ListDictTypes(biz.WithIdentity(context.Background(), auth), &dictv1.ListDictTypesRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list dict types: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 {
		t.Fatalf("items = %+v, want single id 5", out.GetItems())
	}
	if out.GetTotal() != 1 {
		t.Fatalf("total = %d, want 1", out.GetTotal())
	}
	if !out.GetHasMore() || out.GetNextCursor() != "cur" {
		t.Fatalf("has_more/next_cursor = %v/%q, want true/cur", out.GetHasMore(), out.GetNextCursor())
	}
	if out.GetPage() != 2 {
		t.Fatalf("page = %d, want 2", out.GetPage())
	}
}

func TestDictServiceListDictDatasRejectsInvalidQuery(t *testing.T) {
	// page 与 cursor 互斥由传输层协议校验拒绝，仓储不得被触达。
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListDictDatas(biz.WithIdentity(context.Background(), auth), &dictv1.ListDictDatasRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestDictServiceListDictTypesRejectsInvalidQuery(t *testing.T) {
	// page 与 cursor 互斥由传输层协议校验拒绝，仓储不得被触达。
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	page := int32(1)
	after := "cur"

	_, err := svc.ListDictTypes(biz.WithIdentity(context.Background(), auth), &dictv1.ListDictTypesRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("repo calls = %d, want 0", repo.callsCount)
	}
}

func TestDictServiceCreateDictDataForwardsHostAndValueSemantics(t *testing.T) {
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.CreateDictData(biz.WithIdentity(context.Background(), auth), &dictv1.CreateDictDataRequest{
		DictData: &dictv1.DictData{DictTypeId: 5, Label: "启用", Value: "1", ValueType: "integer", SortOrder: 3},
	})
	if err != nil {
		t.Fatalf("create dict data: %v", err)
	}
	if repo.createdData == nil || repo.createdData.DictTypeID != 5 || repo.createdData.ValueType != "integer" || repo.createdData.SortOrder != 3 {
		t.Fatalf("repo created = %+v, want host 5/integer/sort 3", repo.createdData)
	}
	if out.GetId() != 10 {
		t.Fatalf("out id = %d, want 10", out.GetId())
	}
}

func TestDictServiceUpdateDictDataDropsValueSemanticsInput(t *testing.T) {
	// value/value_type 与 dict_type_id 是身份/值语义/归属列：更新输入中被忽略。
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	one := int32(1)

	_, err := svc.UpdateDictData(biz.WithIdentity(context.Background(), auth), &dictv1.UpdateDictDataRequest{
		DictData: &dictv1.DictData{Id: 2, Label: "新标签", Value: "hacked", ValueType: "json", DictTypeId: 99, Status: &one},
	})
	if err != nil {
		t.Fatalf("update dict data: %v", err)
	}
	if repo.updatedData == nil {
		t.Fatal("repo must be called")
	}
	if repo.updatedData.Value != "" || repo.updatedData.ValueType != "" || repo.updatedData.DictTypeID != 0 {
		t.Fatalf("repo updated = %+v, want value semantics and host dropped", repo.updatedData)
	}
	if repo.updatedData.Label != "新标签" {
		t.Fatalf("repo updated label = %q, want 新标签", repo.updatedData.Label)
	}
}

func TestDictServiceDeleteDictDataForwardsID(t *testing.T) {
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	_, err := svc.DeleteDictData(biz.WithIdentity(context.Background(), auth), &dictv1.DeleteDictDataRequest{Id: 12})
	if err != nil {
		t.Fatalf("delete dict data: %v", err)
	}
	if len(repo.deletedDatas) != 1 || repo.deletedDatas[0] != 12 {
		t.Fatalf("deleted = %+v, want [12]", repo.deletedDatas)
	}
}

func TestDictServiceGetDictDataMapsTimestamps(t *testing.T) {
	created := time.Unix(1700000000, 0).UTC()
	updated := time.Unix(1700000100, 0).UTC()
	repo := &fakeDictRepo{}
	svc := NewDictService(biz.NewDictUsecase(repo))

	// GetDataByID 假仓储返回固定形态，这里换成手工构造验证 ts 映射。
	svc.uc = biz.NewDictUsecase(&tsFakeDictRepo{created: created, updated: &updated})
	out, err := svc.GetDictData(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &dictv1.GetDictDataRequest{Id: 3})
	if err != nil {
		t.Fatalf("get dict data: %v", err)
	}
	if out.GetCreatedAt().AsTime() != created {
		t.Fatalf("created_at = %v, want %v", out.GetCreatedAt().AsTime(), created)
	}
	if out.GetUpdatedAt().AsTime() != updated {
		t.Fatalf("updated_at = %v, want %v", out.GetUpdatedAt().AsTime(), updated)
	}
}

// tsFakeDictRepo 返回携带精确时间戳的字典数据，供 DTO 时间映射断言。
type tsFakeDictRepo struct {
	fakeDictRepo
	created time.Time
	updated *time.Time
}

func (f *tsFakeDictRepo) GetDataByID(context.Context, biz.Identity, int64) (*biz.DictData, error) {
	return &biz.DictData{ID: 3, Label: "lbl", Value: "v", ValueType: "string", Status: biz.DictStatusEnabled,
		CreatedAt: f.created, UpdatedAt: f.updated}, nil
}
