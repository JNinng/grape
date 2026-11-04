package service

import (
	"context"
	"errors"
	"testing"

	permv1 "grape/api/perm/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakeRoleRepo 捕获用例转发的角色 DO 与配置入参，供断言转换与身份透传。
type fakeRoleRepo struct {
	biz.RoleRepo

	gotAuth    biz.Identity
	callsCount int

	created   *biz.Role
	updated   *biz.Role
	deleted   []int64
	getID     int64
	scopeSet  *biz.RoleDataScope
	menusSet  []int64
	menusSetI int64
	menus     []int64
}

func (f *fakeRoleRepo) GetByID(_ context.Context, auth biz.Identity, id int64) (*biz.Role, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getID = id
	return &biz.Role{ID: id, Name: "role", Code: "role", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf}, nil
}

func (f *fakeRoleRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.RoleList, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.RoleList{Items: []*biz.Role{{ID: 5, Name: "role-a", Code: "role-a", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeDept}}, HasMore: true, NextCursor: "cur-1"}, nil
}

func (f *fakeRoleRepo) Create(_ context.Context, auth biz.Identity, r *biz.Role) (*biz.Role, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = r
	out := *r
	out.ID = 9
	return &out, nil
}

func (f *fakeRoleRepo) Update(_ context.Context, auth biz.Identity, r *biz.Role) (*biz.Role, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = r
	return r, nil
}

func (f *fakeRoleRepo) SoftDelete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeRoleRepo) GetDataScope(_ context.Context, auth biz.Identity, id int64) (*biz.RoleDataScope, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.RoleDataScope{RoleID: id, DataScope: biz.RoleDataScopeUsers, IDScopes: []biz.RoleIDScopeEntry{{Type: biz.RoleIDScopeTypeUser, DataID: 3}}, AllowedDeptIDs: []int64{2}}, nil
}

func (f *fakeRoleRepo) SetDataScope(_ context.Context, auth biz.Identity, cfg *biz.RoleDataScope) (*biz.RoleDataScope, error) {
	f.callsCount++
	f.gotAuth = auth
	f.scopeSet = cfg
	return cfg, nil
}

func (f *fakeRoleRepo) ListMenus(_ context.Context, auth biz.Identity, id int64) ([]int64, error) {
	f.callsCount++
	f.gotAuth = auth
	return []int64{202, 203}, nil
}

func (f *fakeRoleRepo) SetMenus(_ context.Context, auth biz.Identity, id int64, menuIDs []int64) ([]int64, error) {
	f.callsCount++
	f.gotAuth = auth
	f.menusSetI = id
	f.menusSet = menuIDs
	return f.menus, nil
}

func TestRoleServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetRole(biz.WithIdentity(context.Background(), auth), &permv1.GetRoleRequest{Id: 5})
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("role id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestRoleServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))

	_, err := svc.GetRole(context.Background(), &permv1.GetRoleRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestRoleServiceCreateRoleMapsOptionalFieldsAndForcesServerColumns(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	remark := "ops role"
	scopeDept := int64(2)

	out, err := svc.CreateRole(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.CreateRoleRequest{
		Role: &permv1.Role{Name: "  ops  ", Code: "ops", Remark: &remark, RoleType: biz.RoleTypePreset, DataScope: biz.RoleDataScopeAll, ScopeDeptId: &scopeDept},
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if out.GetId() != 9 {
		t.Fatalf("created id = %d, want 9", out.GetId())
	}
	if repo.created == nil || repo.created.Name != "  ops  " || repo.created.Remark == nil || *repo.created.Remark != "ops role" {
		t.Fatalf("created = (%s, %+v), want (  ops  , ops role)", repo.created.Name, repo.created.Remark)
	}
	// 服务端管理列在用例内强制：类型=租户自定义、范围=仅本人、层级限定为空。
	if repo.created.RoleType != biz.RoleTypeCustom || repo.created.DataScope != biz.RoleDataScopeSelf || repo.created.ScopeDeptID != nil {
		t.Fatalf("created = (type %d, scope %d, scopeDept %+v), want (custom, self, nil)",
			repo.created.RoleType, repo.created.DataScope, repo.created.ScopeDeptID)
	}
}

func TestRoleServiceCreateRoleMissingStatusDefaultsToEnabled(t *testing.T) {
	// optional status 缺失经用例哨兵归一，落库语义为缺省启用（链路端到端断言）。
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))

	if _, err := svc.CreateRole(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.CreateRoleRequest{
		Role: &permv1.Role{Name: "r", Code: "r"},
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.RoleStatusEnabled {
		t.Fatalf("created status = %+v, want enabled", repo.created)
	}
}

func TestRoleServiceUpdateRoleRequiresStatus(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))

	_, err := svc.UpdateRole(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.UpdateRoleRequest{
		Role: &permv1.Role{Id: 5, Name: "n"},
	})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("usecase updated = %+v, want untouched", repo.updated)
	}
}

func TestRoleServiceUpdateRoleDropsIdentityAndScopeColumns(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	status := int32(biz.RoleStatusEnabled)
	scopeDept := int64(2)

	if _, err := svc.UpdateRole(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.UpdateRoleRequest{
		Role: &permv1.Role{Id: 5, Status: &status, Name: "n", Code: "hacked", RoleType: biz.RoleTypePreset, DataScope: biz.RoleDataScopeAll, ScopeDeptId: &scopeDept},
	}); err != nil {
		t.Fatalf("update role: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("usecase updated is nil")
	}
	if repo.updated.Code != "" || repo.updated.RoleType != 0 || repo.updated.DataScope != 0 || repo.updated.ScopeDeptID != nil {
		t.Fatalf("updated = %+v, want identity/scope columns zeroed", repo.updated)
	}
}

func TestRoleServiceListRolesMapsEnvelopeAndPage(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	page := int32(2)

	out, err := svc.ListRoles(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.ListRolesRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetCode() != "role-a" || out.GetItems()[0].GetDataScope() != biz.RoleDataScopeDept {
		t.Fatalf("items = %+v, want one role-a", out.GetItems())
	}
	if !out.GetHasMore() || out.GetNextCursor() != "cur-1" || out.GetPage() != 2 {
		t.Fatalf("envelope = (more %v, cursor %s, page %d), want (true, cur-1, 2)", out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestRoleServiceListRolesRejectsInvalidQuery(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	after := "cur"
	page := int32(1)

	_, err := svc.ListRoles(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.ListRolesRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{After: &after, Page: &page}},
	})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestRoleServiceDataScopeRoundTripsEntriesAndDepts(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	ctx := biz.WithIdentity(context.Background(), auth)

	out, err := svc.GetRoleDataScope(ctx, &permv1.GetRoleDataScopeRequest{Id: 6})
	if err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if out.GetRoleId() != 6 || out.GetDataScope() != biz.RoleDataScopeUsers {
		t.Fatalf("scope = (role %d, ds %d), want (6, users)", out.GetRoleId(), out.GetDataScope())
	}
	if len(out.GetIdScopes()) != 1 || out.GetIdScopes()[0].GetType() != biz.RoleIDScopeTypeUser || out.GetIdScopes()[0].GetDataId() != 3 {
		t.Fatalf("id scopes = %+v, want [(user, 3)]", out.GetIdScopes())
	}
	if len(out.GetAllowedDeptIds()) != 1 || out.GetAllowedDeptIds()[0] != 2 {
		t.Fatalf("allowed dept ids = %v, want [2]", out.GetAllowedDeptIds())
	}

	scopeDept := int64(2)
	set, err := svc.SetRoleDataScope(ctx, &permv1.SetRoleDataScopeRequest{
		Id:             6,
		DataScope:      biz.RoleDataScopeDepts,
		ScopeDeptId:    &scopeDept,
		IdScopes:       []*permv1.RoleIDScopeEntry{{Type: biz.RoleIDScopeTypeDept, DataId: 5}},
		AllowedDeptIds: []int64{2, 5},
	})
	if err != nil {
		t.Fatalf("set data scope: %v", err)
	}
	if repo.scopeSet == nil || repo.scopeSet.RoleID != 6 || repo.scopeSet.DataScope != biz.RoleDataScopeDepts || repo.scopeSet.ScopeDeptID == nil || *repo.scopeSet.ScopeDeptID != 2 {
		t.Fatalf("repo scopeSet = %+v, want (6, depts, dept 2)", repo.scopeSet)
	}
	if len(repo.scopeSet.IDScopes) != 1 || repo.scopeSet.IDScopes[0].Type != biz.RoleIDScopeTypeDept || repo.scopeSet.IDScopes[0].DataID != 5 {
		t.Fatalf("repo id scopes = %+v, want [(dept, 5)]", repo.scopeSet.IDScopes)
	}
	if len(set.GetAllowedDeptIds()) != 2 {
		t.Fatalf("reply allowed = %v, want [2 5]", set.GetAllowedDeptIds())
	}
}

func TestRoleServiceMenusRoundTripsIDs(t *testing.T) {
	repo := &fakeRoleRepo{menus: []int64{202, 203}}
	svc := NewRoleService(biz.NewRoleUsecase(repo))
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})

	out, err := svc.ListRoleMenus(ctx, &permv1.ListRoleMenusRequest{Id: 3})
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if len(out.GetMenuIds()) != 2 || out.GetMenuIds()[0] != 202 || out.GetMenuIds()[1] != 203 {
		t.Fatalf("menu ids = %v, want [202 203]", out.GetMenuIds())
	}

	set, err := svc.SetRoleMenus(ctx, &permv1.SetRoleMenusRequest{Id: 3, MenuIds: []int64{204}})
	if err != nil {
		t.Fatalf("set menus: %v", err)
	}
	if repo.menusSetI != 3 || len(repo.menusSet) != 1 || repo.menusSet[0] != 204 {
		t.Fatalf("repo menus = (role %d, %v), want (3, [204])", repo.menusSetI, repo.menusSet)
	}
	if len(set.GetMenuIds()) != 2 || set.GetMenuIds()[0] != 202 {
		t.Fatalf("reply menus = %v, want [202 203]", set.GetMenuIds())
	}
}

func TestRoleServiceDeleteRoleForwardsID(t *testing.T) {
	repo := &fakeRoleRepo{}
	svc := NewRoleService(biz.NewRoleUsecase(repo))

	if _, err := svc.DeleteRole(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.DeleteRoleRequest{Id: 8}); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 8 {
		t.Fatalf("repo deleted = %v, want [8]", repo.deleted)
	}
}
