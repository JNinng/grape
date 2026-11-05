package pg_sqlc

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 字典仓储集成测试：以真实 PostgreSQL 验证 sys_dict_type / sys_dict_data
// 固定 CRUD、动态查询协议、两级写路径纪律（数据创建先锁宿主类型行、类型
// 删除先校验活跃数据）与 RLS 租户边界。复用根包套件的 setupTestDB（deploy
// 全清单装载，种子数据见 sqlc/seed.sql：t-super-01 有字典类型 1-3 与字典
// 数据 1-11；t-test-02 无字典种子）。各用例经 setupTestDB 重建数据库实现
// 隔离；新建行用 newID() 发号，不与种子主键冲突。
//
// 运行（PostgreSQL 必需，未设环境变量时跳过）：
//
//	GRAPE_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	GRAPE_TEST_DSN=postgres://grape_u:grape_u_pwd@127.0.0.1:5432/grape_pgsqlc_test \
//	go test ./internal/data/pg_sqlc/ -run TestDictRepo -count=1

const (
	// dictTenantSeeded 是种子字典所在的租户。
	dictTenantSeeded = "t-super-01"
	// dictTenantEmpty 是无字典种子的租户，供新建与跨租户断言使用。
	dictTenantEmpty = "t-test-02"
)

// dictAuth 返回指定租户的操作者身份；字典表无 @scope，操作者仅落入审计列。
func dictAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: 1}
}

// mustCreateDictType 在指定租户内创建字典类型，失败即终止用例。
func mustCreateDictType(t *testing.T, d *Data, auth biz.Identity, typ *biz.DictType) *biz.DictType {
	t.Helper()
	out, err := NewDictRepo(d).CreateType(context.Background(), auth, typ)
	if err != nil {
		t.Fatalf("create dict type %s: %v", typ.Code, err)
	}
	return out
}

// mustCreateDictData 在指定租户内创建字典数据，失败即终止用例。
func mustCreateDictData(t *testing.T, d *Data, auth biz.Identity, data *biz.DictData) *biz.DictData {
	t.Helper()
	out, err := NewDictRepo(d).CreateData(context.Background(), auth, data)
	if err != nil {
		t.Fatalf("create dict data %s: %v", data.Value, err)
	}
	return out
}

// mustSoftDeleteDictData 软删除指定字典数据（写侧属字典域，直接走仓储），
// 失败即终止用例。
func mustSoftDeleteDictData(t *testing.T, d *Data, auth biz.Identity, id int64) {
	t.Helper()
	if err := NewDictRepo(d).SoftDeleteData(context.Background(), auth, id); err != nil {
		t.Fatalf("soft delete dict data %d: %v", id, err)
	}
}

// dictTypeQuery 构造按 code 精确过滤的动态查询。
func dictTypeQuery(code string) *dynq.Query {
	return &dynq.Query{
		Where: []dynq.Condition{{Field: "code", Op: "eq", Value: code}},
	}
}

// dictDataQuery 构造按宿主类型过滤的动态查询。
func dictDataQuery(typeID int64) *dynq.Query {
	return &dynq.Query{
		Where: []dynq.Condition{{Field: "dictTypeId", Op: "eq", Value: typeID}},
	}
}

func TestDictRepoGetDictTypeByIDReturnsSeededRow(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	out, err := repo.GetTypeByID(context.Background(), dictAuth(dictTenantSeeded), 1)
	if err != nil {
		t.Fatalf("get dict type 1: %v", err)
	}
	if out.Code != "sys_data_scope" || out.Name != "数据范围" {
		t.Fatalf("row = (%s, %s), want (sys_data_scope, 数据范围)", out.Code, out.Name)
	}
}

func TestDictRepoGetDictTypeByIDAcrossTenantReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.GetTypeByID(context.Background(), dictAuth(dictTenantEmpty), 1)
	if !errors.Is(err, biz.ErrDictTypeNotFound) {
		t.Fatalf("err = %v, want ErrDictTypeNotFound", err)
	}
}

func TestDictRepoGetDictTypeByIDMissingRowReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.GetTypeByID(context.Background(), dictAuth(dictTenantSeeded), 999999)
	if !errors.Is(err, biz.ErrDictTypeNotFound) {
		t.Fatalf("err = %v, want ErrDictTypeNotFound", err)
	}
}

func TestDictRepoCreateDictTypePersistsAndDefaultsTimestamps(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	out, err := repo.CreateType(context.Background(), auth, &biz.DictType{
		Status: biz.DictStatusEnabled, Name: "性别", Code: "sys_gender",
	})
	if err != nil {
		t.Fatalf("create dict type: %v", err)
	}
	if out.ID == 0 || out.CreatedAt.IsZero() {
		t.Fatalf("row = id %d created_at %v, want generated id and timestamp", out.ID, out.CreatedAt)
	}
	got, err := repo.GetTypeByID(context.Background(), auth, out.ID)
	if err != nil {
		t.Fatalf("re-read dict type: %v", err)
	}
	if got.Code != "sys_gender" || got.CreatedBy != auth.UserID {
		t.Fatalf("row = (%s, created_by %d), want (sys_gender, %d)", got.Code, got.CreatedBy, auth.UserID)
	}
}

func TestDictRepoCreateDictTypeDuplicateCodeReturnsAlreadyExists(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.CreateType(context.Background(), dictAuth(dictTenantSeeded), &biz.DictType{
		Status: biz.DictStatusEnabled, Name: "重复", Code: "sys_data_scope",
	})
	if !errors.Is(err, biz.ErrDictTypeAlreadyExists) {
		t.Fatalf("err = %v, want ErrDictTypeAlreadyExists", err)
	}
}

func TestDictRepoCreateDictTypeCodeReusableAfterSoftDelete(t *testing.T) {
	// 唯一约束限定未删除行间：软删后同租户 code 可复用。
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	first := mustCreateDictType(t, d, auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "临时", Code: "sys_tmp"})
	if err := repo.SoftDeleteType(context.Background(), auth, first.ID); err != nil {
		t.Fatalf("soft delete dict type: %v", err)
	}

	second, err := repo.CreateType(context.Background(), auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "复用", Code: "sys_tmp"})
	if err != nil {
		t.Fatalf("reuse code after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestDictRepoUpdateDictTypeCoversMutableFieldsOnly(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	created := mustCreateDictType(t, d, auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "旧名", Code: "sys_upd"})
	updated, err := repo.UpdateType(context.Background(), auth, &biz.DictType{
		ID: created.ID, Status: biz.DictStatusDisabled, Name: "新名",
	})
	if err != nil {
		t.Fatalf("update dict type: %v", err)
	}
	if updated.Name != "新名" || updated.Status != biz.DictStatusDisabled {
		t.Fatalf("row = (%s, status %d), want (新名, disabled)", updated.Name, updated.Status)
	}
	if updated.Code != "sys_upd" {
		t.Fatalf("code = %s, want unchanged sys_upd", updated.Code)
	}
}

func TestDictRepoUpdateDictTypeMissingRowReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.UpdateType(context.Background(), dictAuth(dictTenantSeeded), &biz.DictType{
		ID: 999999, Status: biz.DictStatusEnabled, Name: "x",
	})
	if !errors.Is(err, biz.ErrDictTypeNotFound) {
		t.Fatalf("err = %v, want ErrDictTypeNotFound", err)
	}
}

func TestDictRepoSoftDeleteTypeWithActiveDataReturnsHasData(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	// 种子类型 1 下有 7 条活跃数据，软删必须被拒绝。
	err := repo.SoftDeleteType(context.Background(), dictAuth(dictTenantSeeded), 1)
	if !errors.Is(err, biz.ErrDictTypeHasData) {
		t.Fatalf("err = %v, want ErrDictTypeHasData", err)
	}
	if _, err := repo.GetTypeByID(context.Background(), dictAuth(dictTenantSeeded), 1); err != nil {
		t.Fatalf("rejected delete must keep row readable: %v", err)
	}
}

func TestDictRepoSoftDeleteTypeAfterDataClearedSucceeds(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	typ := mustCreateDictType(t, d, auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "待删", Code: "sys_del"})
	data := mustCreateDictData(t, d, auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: typ.ID, Label: "项", Value: "a", ValueType: "string",
	})
	mustSoftDeleteDictData(t, d, auth, data.ID)

	if err := repo.SoftDeleteType(context.Background(), auth, typ.ID); err != nil {
		t.Fatalf("soft delete cleared dict type: %v", err)
	}
	if _, err := repo.GetTypeByID(context.Background(), auth, typ.ID); !errors.Is(err, biz.ErrDictTypeNotFound) {
		t.Fatalf("err = %v, want ErrDictTypeNotFound after soft delete", err)
	}
}

func TestDictRepoListDictTypesFiltersByCodeAndIsolatesTenant(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	out, err := repo.ListTypes(context.Background(), dictAuth(dictTenantSeeded), dictTypeQuery("sys_role_type"))
	if err != nil {
		t.Fatalf("list dict types: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != 2 {
		t.Fatalf("items = %+v, want single seeded type 2", out.Items)
	}

	// 同一查询在无种子租户内必须为空集（RLS 隔离），不报错。
	empty, err := repo.ListTypes(context.Background(), dictAuth(dictTenantEmpty), dictTypeQuery("sys_role_type"))
	if err != nil {
		t.Fatalf("list dict types in empty tenant: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("items = %+v, want empty across tenant", empty.Items)
	}
}

func TestDictRepoListDictTypesCountsTotalIndependentOfPage(t *testing.T) {
	// 种子类型共 3 条；第 2 页（页大小 2）的 total 不得随翻页位置递减。
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	page := 2
	pageSize := 2

	q := &dynq.Query{Pagination: &dynq.Pagination{Page: &page, PageSize: &pageSize}}
	out, err := repo.ListTypes(context.Background(), dictAuth(dictTenantSeeded), q)
	if err != nil {
		t.Fatalf("list dict types page 2: %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("page 2 items = %d, want 1", len(out.Items))
	}
	if out.Total == nil || *out.Total != 3 {
		t.Fatalf("total = %+v, want 3", out.Total)
	}
}

func TestDictRepoListDictTypesRejectsUnknownField(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	q := &dynq.Query{Where: []dynq.Condition{{Field: "noSuchField", Op: "eq", Value: "x"}}}
	_, err := repo.ListTypes(context.Background(), dictAuth(dictTenantSeeded), q)
	if !errors.Is(err, biz.ErrDictTypeInvalidArgument) {
		t.Fatalf("err = %v, want ErrDictTypeInvalidArgument", err)
	}
}

func TestDictRepoGetDictDataByIDReturnsSeededRow(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	out, err := repo.GetDataByID(context.Background(), dictAuth(dictTenantSeeded), 7)
	if err != nil {
		t.Fatalf("get dict data 7: %v", err)
	}
	if out.Value != "7" || out.DictTypeID != 1 || out.ValueType != "integer" {
		t.Fatalf("row = (%s, type %d, vt %s), want (7, 1, integer)", out.Value, out.DictTypeID, out.ValueType)
	}
}

func TestDictRepoGetDictDataByIDAcrossTenantReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.GetDataByID(context.Background(), dictAuth(dictTenantEmpty), 7)
	if !errors.Is(err, biz.ErrDictDataNotFound) {
		t.Fatalf("err = %v, want ErrDictDataNotFound", err)
	}
}

func TestDictRepoCreateDictDataRequiresActiveHostType(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	// 宿主类型不存在：整批拒绝不写入。
	_, err := repo.CreateData(context.Background(), auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 999999, Label: "项", Value: "a", ValueType: "string",
	})
	if !errors.Is(err, biz.ErrDictDataInvalidArgument) {
		t.Fatalf("missing host err = %v, want ErrDictDataInvalidArgument", err)
	}

	// 宿主类型跨租户（RLS 之外等同不存在）：同样拒绝。
	_, err = repo.CreateData(context.Background(), auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 1, Label: "项", Value: "a", ValueType: "string",
	})
	if !errors.Is(err, biz.ErrDictDataInvalidArgument) {
		t.Fatalf("cross-tenant host err = %v, want ErrDictDataInvalidArgument", err)
	}

	// 宿主类型已软删：同样拒绝（活跃校验，非仅存在校验）。
	typ := mustCreateDictType(t, d, auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "已删", Code: "sys_gone"})
	if err := repo.SoftDeleteType(context.Background(), auth, typ.ID); err != nil {
		t.Fatalf("soft delete host type: %v", err)
	}
	_, err = repo.CreateData(context.Background(), auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: typ.ID, Label: "项", Value: "a", ValueType: "string",
	})
	if !errors.Is(err, biz.ErrDictDataInvalidArgument) {
		t.Fatalf("deleted host err = %v, want ErrDictDataInvalidArgument", err)
	}
}

func TestDictRepoCreateDictDataDuplicateValueReturnsAlreadyExists(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	// 种子类型 1 下 value "1" 已存在（数据行 1）。
	_, err := repo.CreateData(context.Background(), dictAuth(dictTenantSeeded), &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 1, Label: "重复", Value: "1", ValueType: "integer",
	})
	if !errors.Is(err, biz.ErrDictDataAlreadyExists) {
		t.Fatalf("err = %v, want ErrDictDataAlreadyExists", err)
	}
}

func TestDictRepoCreateDictDataValueUniqueScopeIsPerType(t *testing.T) {
	// 唯一约束按 (tenant_id, dict_type_id, value)：同租户不同类型下同值合法。
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	out, err := repo.CreateData(context.Background(), dictAuth(dictTenantSeeded), &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 3, Label: "其他类型同值", Value: "7", ValueType: "integer",
	})
	if err != nil {
		t.Fatalf("create dict data with same value in another type: %v", err)
	}
	if out.DictTypeID != 3 {
		t.Fatalf("host = %d, want 3", out.DictTypeID)
	}
}

func TestDictRepoCreateDictDataValueReusableAfterSoftDelete(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantSeeded)

	first := mustCreateDictData(t, d, auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 1, Label: "临时", Value: "tmp-x", ValueType: "string",
	})
	mustSoftDeleteDictData(t, d, auth, first.ID)

	second, err := repo.CreateData(context.Background(), auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: 1, Label: "复用", Value: "tmp-x", ValueType: "string",
	})
	if err != nil {
		t.Fatalf("reuse value after soft delete: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("reused row id = %d, want a new id", second.ID)
	}
}

func TestDictRepoUpdateDictDataCoversMutableFieldsOnly(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantEmpty)

	typ := mustCreateDictType(t, d, auth, &biz.DictType{Status: biz.DictStatusEnabled, Name: "宿主", Code: "sys_host"})
	created := mustCreateDictData(t, d, auth, &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: typ.ID, Label: "旧标签", Value: "v1", ValueType: "string",
	})
	updated, err := repo.UpdateData(context.Background(), auth, &biz.DictData{
		ID: created.ID, Status: biz.DictStatusDisabled, Label: "新标签", SortOrder: 5,
	})
	if err != nil {
		t.Fatalf("update dict data: %v", err)
	}
	if updated.Label != "新标签" || updated.Status != biz.DictStatusDisabled || updated.SortOrder != 5 {
		t.Fatalf("row = (%s, %d, %d), want (新标签, disabled, 5)", updated.Label, updated.Status, updated.SortOrder)
	}
	if updated.Value != "v1" || updated.ValueType != "string" || updated.DictTypeID != typ.ID {
		t.Fatalf("identity columns = (%s, %s, %d), want unchanged (v1, string, %d)", updated.Value, updated.ValueType, updated.DictTypeID, typ.ID)
	}
}

func TestDictRepoUpdateDictDataMissingRowReturnsNotFound(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	_, err := repo.UpdateData(context.Background(), dictAuth(dictTenantSeeded), &biz.DictData{
		ID: 999999, Status: biz.DictStatusEnabled, Label: "x", SortOrder: 0,
	})
	if !errors.Is(err, biz.ErrDictDataNotFound) {
		t.Fatalf("err = %v, want ErrDictDataNotFound", err)
	}
}

func TestDictRepoSoftDeleteDictDataAcrossTenantReturnsNotFound(t *testing.T) {
	// 跨租户行不可见：软删返回 NotFound（不泄露存在性），目标行原样保留。
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	err := repo.SoftDeleteData(context.Background(), dictAuth(dictTenantEmpty), 9)
	if !errors.Is(err, biz.ErrDictDataNotFound) {
		t.Fatalf("err = %v, want ErrDictDataNotFound", err)
	}
	if _, err := repo.GetDataByID(context.Background(), dictAuth(dictTenantSeeded), 9); err != nil {
		t.Fatalf("target row must stay readable in its own tenant: %v", err)
	}
}

func TestDictRepoListDictTypesCursorPageKeepsTotalUnshifted(t *testing.T) {
	// 游标模式下 total 与翻页位置无关：第二页（After 游标）的 total 仍为
	// 全集 3，而非剩余行数。
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	pageSize := 2
	includeTotal := true

	first := &dynq.Query{Pagination: &dynq.Pagination{PageSize: &pageSize, IncludeTotal: &includeTotal}}
	out, err := repo.ListTypes(context.Background(), dictAuth(dictTenantSeeded), first)
	if err != nil {
		t.Fatalf("list first cursor page: %v", err)
	}
	if len(out.Items) != 2 || out.NextCursor == "" {
		t.Fatalf("first page = (%d items, cursor %q), want 2 items with a cursor", len(out.Items), out.NextCursor)
	}

	after := out.NextCursor
	second := &dynq.Query{Pagination: &dynq.Pagination{After: &after, PageSize: &pageSize, IncludeTotal: &includeTotal}}
	secondOut, err := repo.ListTypes(context.Background(), dictAuth(dictTenantSeeded), second)
	if err != nil {
		t.Fatalf("list second cursor page: %v", err)
	}
	if len(secondOut.Items) != 1 {
		t.Fatalf("second page items = %d, want 1", len(secondOut.Items))
	}
	if secondOut.Total == nil || *secondOut.Total != 3 {
		t.Fatalf("second page total = %+v, want 3", secondOut.Total)
	}
}

func TestDictRepoSoftDeleteDataKeepsHostReadable(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)
	auth := dictAuth(dictTenantSeeded)

	// 种子数据 1 软删后：数据不可读，宿主类型 1 仍可读且活跃计数减一。
	mustSoftDeleteDictData(t, d, auth, 1)
	if _, err := repo.GetDataByID(context.Background(), auth, 1); !errors.Is(err, biz.ErrDictDataNotFound) {
		t.Fatalf("err = %v, want ErrDictDataNotFound after soft delete", err)
	}
	if _, err := repo.GetTypeByID(context.Background(), auth, 1); err != nil {
		t.Fatalf("host must stay readable: %v", err)
	}
	// 剩余 6 条活跃数据仍在，类型 1 的删除仍被拒绝。
	if err := repo.SoftDeleteType(context.Background(), auth, 1); !errors.Is(err, biz.ErrDictTypeHasData) {
		t.Fatalf("err = %v, want ErrDictTypeHasData with remaining active data", err)
	}
}

func TestDictRepoListDictDatasFiltersByHostType(t *testing.T) {
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	out, err := repo.ListDatas(context.Background(), dictAuth(dictTenantSeeded), dictDataQuery(2))
	if err != nil {
		t.Fatalf("list dict datas: %v", err)
	}
	if len(out.Items) != 2 || out.Total == nil || *out.Total != 2 {
		t.Fatalf("items/total = %d/%+v, want 2/2", len(out.Items), out.Total)
	}
	for _, item := range out.Items {
		if item.DictTypeID != 2 {
			t.Fatalf("item host = %d, want 2", item.DictTypeID)
		}
	}
}

func TestDictRepoListDictDatasAcceptsStringInt64Filter(t *testing.T) {
	// 前端走 protojson 口径：int64 一律序列化为字符串，where 值即字符串
	// 形态。协议门必须接受十进制字符串并精确解析（>2^53 的 newID 主键
	// 无法用 JSON number 无损表达，此形态是唯一无损传输口径）——这正是
	// 字典数据维护弹窗按宿主过滤的真实请求形态，曾因协议门只收数字形态
	// 而整单 400。
	d := setupTestDB(t)
	repo := NewDictRepo(d)

	seeded := mustCreateDictType(t, d, dictAuth(dictTenantSeeded), &biz.DictType{
		Status: biz.DictStatusEnabled, Name: "字符串过滤", Code: "sys_str_filter",
	})
	created := mustCreateDictData(t, d, dictAuth(dictTenantSeeded), &biz.DictData{
		Status: biz.DictStatusEnabled, DictTypeID: seeded.ID, Label: "项", Value: "v", ValueType: "string",
	})

	q := &dynq.Query{Where: []dynq.Condition{{Field: "dictTypeId", Op: "eq", Value: strconv.FormatInt(seeded.ID, 10)}}}
	out, err := repo.ListDatas(context.Background(), dictAuth(dictTenantSeeded), q)
	if err != nil {
		t.Fatalf("list dict datas with string int64 filter: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != created.ID {
		t.Fatalf("items = %+v, want the created data %d", out.Items, created.ID)
	}
}
