package biz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockDictRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与领域
// 校验，不触真实存储。
type mockDictRepo struct {
	DictRepo

	getTypeID   int64
	listedTypes bool
	createdType *DictType
	updatedType *DictType
	deletedType []int64
	typeErr     error

	getDataID    int64
	listedDatas  bool
	createdData  *DictData
	updatedData  *DictData
	deletedData  []int64
	dataErr      error
	createDataID int64 // CreateData 收到的宿主类型 ID
}

func (m *mockDictRepo) GetTypeByID(_ context.Context, _ Identity, id int64) (*DictType, error) {
	m.getTypeID = id
	if m.typeErr != nil {
		return nil, m.typeErr
	}
	return &DictType{ID: id, Status: DictStatusEnabled, Name: "typ", Code: "typ"}, nil
}

func (m *mockDictRepo) ListTypes(context.Context, Identity, *dynq.Query) (*DictTypeList, error) {
	m.listedTypes = true
	return &DictTypeList{}, nil
}

func (m *mockDictRepo) CreateType(_ context.Context, _ Identity, t *DictType) (*DictType, error) {
	m.createdType = t
	out := *t
	out.ID = 100
	return &out, nil
}

func (m *mockDictRepo) UpdateType(_ context.Context, _ Identity, t *DictType) (*DictType, error) {
	m.updatedType = t
	return t, nil
}

func (m *mockDictRepo) SoftDeleteType(_ context.Context, _ Identity, id int64) error {
	if m.typeErr != nil {
		return m.typeErr
	}
	m.deletedType = append(m.deletedType, id)
	return nil
}

func (m *mockDictRepo) GetDataByID(_ context.Context, _ Identity, id int64) (*DictData, error) {
	m.getDataID = id
	if m.dataErr != nil {
		return nil, m.dataErr
	}
	return &DictData{ID: id, Status: DictStatusEnabled, Label: "lbl", Value: "v", ValueType: "string"}, nil
}

func (m *mockDictRepo) ListDatas(context.Context, Identity, *dynq.Query) (*DictDataList, error) {
	m.listedDatas = true
	return &DictDataList{}, nil
}

func (m *mockDictRepo) CreateData(_ context.Context, _ Identity, d *DictData) (*DictData, error) {
	m.createDataID = d.DictTypeID
	m.createdData = d
	out := *d
	out.ID = 200
	return &out, nil
}

func (m *mockDictRepo) UpdateData(_ context.Context, _ Identity, d *DictData) (*DictData, error) {
	m.updatedData = d
	return d, nil
}

func (m *mockDictRepo) SoftDeleteData(_ context.Context, _ Identity, id int64) error {
	if m.dataErr != nil {
		return m.dataErr
	}
	m.deletedData = append(m.deletedData, id)
	return nil
}

func TestDictUsecaseCreateDictTypeDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	out, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: "状态", Code: "sys_status", Status: DictStatusUnspecified})
	if err != nil {
		t.Fatalf("create dict type: %v", err)
	}
	if out.Status != DictStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.createdType == nil || repo.createdType.Code != "sys_status" {
		t.Fatalf("repo created = %+v, want code sys_status", repo.createdType)
	}
}

func TestDictUsecaseCreateDictTypeKeepsExplicitDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: "paused", Code: "paused", Status: DictStatusDisabled})
	if err != nil {
		t.Fatalf("create dict type: %v", err)
	}
	if repo.createdType == nil || repo.createdType.Status != DictStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.createdType)
	}
}

func TestDictUsecaseCreateDictTypeRejectsBlankName(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: "   ", Code: "c"})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if repo.createdType != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.createdType)
	}
}

func TestDictUsecaseCreateDictTypeRejectsBlankCode(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: "typ", Code: "  "})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseCreateDictTypeRejectsNameOverColumnWidth(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: strings.Repeat("名", 257), Code: "c"})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseCreateDictTypeAcceptsNameAtColumnWidth(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: strings.Repeat("名", 256), Code: "c"})
	if err != nil {
		t.Fatalf("create dict type with 256-rune name: %v", err)
	}
}

func TestDictUsecaseCreateDictTypeRejectsInvalidStatus(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{Name: "typ", Code: "c", Status: 2})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsInvalidStatus(t *testing.T) {
	// 更新路径 status 必填且只认 0/1；未指定哨兵在更新输入里非法。
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{ID: 1, Name: "typ", Status: DictStatusUnspecified})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsMissingID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{ID: 0, Name: "typ", Status: DictStatusEnabled})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsNil(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, nil)
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsBlankName(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{ID: 1, Name: "  ", Status: DictStatusEnabled})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if repo.updatedType != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.updatedType)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsNameOverColumnWidth(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{ID: 1, Name: strings.Repeat("名", 257), Status: DictStatusEnabled})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictDataRejectsBlankLabel(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{ID: 1, Label: "  ", Status: DictStatusEnabled})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictTypeRejectsRemarkOverColumnWidth(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	remark := strings.Repeat("注", 513)
	_, err := uc.UpdateDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictType{ID: 1, Name: "typ", Status: DictStatusEnabled, Remark: &remark})
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictUsecaseGetDictTypeRejectsNonPositiveID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.GetDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if repo.getTypeID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.getTypeID)
	}
}

func TestDictUsecaseDeleteDictTypeRejectsNonPositiveID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	err := uc.DeleteDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1)
	if !errors.Is(err, ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
	if len(repo.deletedType) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deletedType)
	}
}

func TestDictUsecaseDeleteDictTypePropagatesHasDataRejection(t *testing.T) {
	repo := &mockDictRepo{typeErr: ErrDictTypeHasData}
	uc := NewDictUsecase(repo)

	err := uc.DeleteDictType(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 3)
	if !errors.Is(err, ErrDictTypeHasData) {
		t.Fatalf("err = %v, want ErrDictTypeHasData", err)
	}
}

func TestDictUsecaseListDictTypesForwardsQuery(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.ListDictTypes(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list dict types: %v", err)
	}
	if !repo.listedTypes {
		t.Fatal("repo ListTypes must be called")
	}
}

func TestDictUsecaseCreateDictDataDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	out, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "启用", Value: "1", ValueType: "integer", Status: DictStatusUnspecified})
	if err != nil {
		t.Fatalf("create dict data: %v", err)
	}
	if out.Status != DictStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.createDataID != 5 {
		t.Fatalf("repo host type id = %d, want 5", repo.createDataID)
	}
}

func TestDictUsecaseCreateDictDataAcceptsAllWhiteListValueTypes(t *testing.T) {
	for _, vt := range DictValueTypeWhiteList {
		repo := &mockDictRepo{}
		uc := NewDictUsecase(repo)

		_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: "v", ValueType: vt})
		if err != nil {
			t.Fatalf("create dict data with value_type %q: %v", vt, err)
		}
	}
}

func TestDictUsecaseCreateDictDataRejectsUnknownValueType(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: "v", ValueType: "object"})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
	if repo.createdData != nil {
		t.Fatalf("repo must not be called on invalid input, got %+v", repo.createdData)
	}
}

func TestDictUsecaseCreateDictDataRejectsBlankValue(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: " ", ValueType: "string"})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseCreateDictDataRejectsMissingHostType(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 0, Label: "l", Value: "v", ValueType: "string"})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseCreateDictDataRejectsNegativeSortOrder(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: "v", ValueType: "string", SortOrder: -1})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseCreateDictDataAcceptsZeroSortOrder(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: "v", ValueType: "string", SortOrder: 0})
	if err != nil {
		t.Fatalf("create dict data with sort_order 0: %v", err)
	}
}

func TestDictUsecaseCreateDictDataRejectsValueTypeOverColumnWidth(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.CreateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{DictTypeID: 5, Label: "l", Value: "v", ValueType: strings.Repeat("s", 33)})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictDataRejectsInvalidStatus(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{ID: 1, Label: "l", Status: DictStatusUnspecified})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseUpdateDictDataRejectsMissingID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.UpdateDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &DictData{ID: 0, Label: "l", Status: DictStatusEnabled})
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictUsecaseGetDictDataRejectsNonPositiveID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.GetDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
	if repo.getDataID != 0 {
		t.Fatalf("repo must not be called, got id %d", repo.getDataID)
	}
}

func TestDictUsecaseDeleteDictDataRejectsNonPositiveID(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	err := uc.DeleteDictData(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrDictDataInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictDataInvalidArgument", err)
	}
	if len(repo.deletedData) != 0 {
		t.Fatalf("repo must not be called, got %+v", repo.deletedData)
	}
}

func TestDictUsecaseListDictDatasForwardsQuery(t *testing.T) {
	repo := &mockDictRepo{}
	uc := NewDictUsecase(repo)

	_, err := uc.ListDictDatas(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{})
	if err != nil {
		t.Fatalf("list dict datas: %v", err)
	}
	if !repo.listedDatas {
		t.Fatal("repo ListDatas must be called")
	}
}
