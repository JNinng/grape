package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockRoleRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与领域
// 校验，不触真实存储。
type mockRoleRepo struct {
	RoleRepo

	getErr   error
	listed   bool
	created  *Role
	updated  *Role
	deleted  []int64
	deleteEE error

	scopeReadID int64
	scopeRead   *RoleDataScope
	scopeSet    *RoleDataScope
	scopeSetErr error
	menusReadID int64
	menusRead   []int64
	menusSetID  int64
	menusSet    []int64
	menusSetEE  error
	menusResult []int64
}

func (m *mockRoleRepo) GetByID(_ context.Context, _ Identity, id int64) (*Role, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &Role{ID: id, Status: RoleStatusEnabled, Name: "role", Code: "role", RoleType: RoleTypeCustom, DataScope: RoleDataScopeSelf}, nil
}

func (m *mockRoleRepo) List(context.Context, Identity, *dynq.Query) (*RoleList, error) {
	m.listed = true
	return &RoleList{}, nil
}

func (m *mockRoleRepo) Create(_ context.Context, _ Identity, r *Role) (*Role, error) {
	m.created = r
	out := *r
	out.ID = 100
	return &out, nil
}

func (m *mockRoleRepo) Update(_ context.Context, _ Identity, r *Role) (*Role, error) {
	m.updated = r
	return r, nil
}

func (m *mockRoleRepo) SoftDelete(_ context.Context, _ Identity, id int64) error {
	m.deleted = append(m.deleted, id)
	return m.deleteEE
}

func (m *mockRoleRepo) GetDataScope(_ context.Context, _ Identity, id int64) (*RoleDataScope, error) {
	m.scopeReadID = id
	if m.scopeRead == nil {
		return &RoleDataScope{RoleID: id, DataScope: RoleDataScopeSelf}, nil
	}
	return m.scopeRead, nil
}

func (m *mockRoleRepo) SetDataScope(_ context.Context, _ Identity, cfg *RoleDataScope) (*RoleDataScope, error) {
	m.scopeSet = cfg
	if m.scopeSetErr != nil {
		return nil, m.scopeSetErr
	}
	return cfg, nil
}

func (m *mockRoleRepo) ListMenus(_ context.Context, _ Identity, id int64) ([]int64, error) {
	m.menusReadID = id
	return m.menusRead, nil
}

func (m *mockRoleRepo) SetMenus(_ context.Context, _ Identity, id int64, menuIDs []int64) ([]int64, error) {
	m.menusSetID = id
	m.menusSet = menuIDs
	if m.menusSetEE != nil {
		return nil, m.menusSetEE
	}
	return m.menusResult, nil
}

func TestRoleUsecaseCreateRoleDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	out, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "role", Code: "role-1", Status: RoleStatusUnspecified})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	if out.Status != RoleStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.Code != "role-1" {
		t.Fatalf("repo created = %+v, want code role-1", repo.created)
	}
}

func TestRoleUsecaseCreateRoleWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if _, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "paused", Code: "paused", Status: RoleStatusDisabled}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if repo.created == nil || repo.created.Status != RoleStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestRoleUsecaseCreateRoleForcesTenantCustomTypeAndSelfScope(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)
	scopeDept := int64(2)

	if _, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "r", Code: "r", RoleType: RoleTypePreset, DataScope: RoleDataScopeAll, ScopeDeptID: &scopeDept}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if repo.created == nil {
		t.Fatal("repo created is nil")
	}
	if repo.created.RoleType != RoleTypeCustom || repo.created.DataScope != RoleDataScopeSelf || repo.created.ScopeDeptID != nil {
		t.Fatalf("created = (type %d, scope %d, scopeDept %+v), want (custom, self, nil)",
			repo.created.RoleType, repo.created.DataScope, repo.created.ScopeDeptID)
	}
}

func TestRoleUsecaseCreateRoleBlankNameRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	_, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "  ", Code: "r"})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestRoleUsecaseCreateRoleMissingCodeRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	_, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "role"})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestRoleUsecaseCreateRoleOverlongFieldsRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)
	longName := make([]rune, roleNameMaxRunes+1)
	longCode := make([]rune, roleCodeMaxRunes+1)
	longRemark := make([]rune, roleRemarkMaxRunes+1)
	remark := string(longRemark)

	for name, tc := range map[string]*Role{
		"name over max":   {Name: string(longName), Code: "r"},
		"code over max":   {Name: "role", Code: string(longCode)},
		"remark over max": {Name: "role", Code: "r", Remark: &remark},
	} {
		_, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, tc)
		if !errors.Is(err, ErrRoleInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrRoleInvalidArgument", name, err)
		}
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestRoleUsecaseCreateRoleInvalidStatusRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	_, err := uc.CreateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{Name: "role", Code: "r", Status: 2})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestRoleUsecaseUpdateRoleForwardsMutableFields(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)
	remark := "updated"

	out, err := uc.UpdateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{ID: 5, Status: RoleStatusDisabled, Name: "new", Remark: &remark})
	if err != nil {
		t.Fatalf("update role: %v", err)
	}
	if out.ID != 5 {
		t.Fatalf("id = %d, want 5", out.ID)
	}
	if repo.updated == nil || repo.updated.ID != 5 || repo.updated.Status != RoleStatusDisabled || repo.updated.Name != "new" {
		t.Fatalf("repo updated = %+v, want (5, disabled, new)", repo.updated)
	}
}

func TestRoleUsecaseUpdateRoleMissingIDRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	_, err := uc.UpdateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{ID: 0, Status: RoleStatusEnabled, Name: "x"})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo updated = %+v, want untouched", repo.updated)
	}
}

func TestRoleUsecaseUpdateRoleInvalidStatusRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	_, err := uc.UpdateRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Role{ID: 5, Status: 3, Name: "x"})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo updated = %+v, want untouched", repo.updated)
	}
}

func TestRoleUsecaseDeleteRoleForwardsID(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if err := uc.DeleteRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 7); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 7 {
		t.Fatalf("repo deleted = %v, want [7]", repo.deleted)
	}
}

func TestRoleUsecaseDeleteRoleInvalidIDRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if err := uc.DeleteRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo deleted = %v, want untouched", repo.deleted)
	}
}

func TestRoleUsecaseListRolesPassesThrough(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if _, err := uc.ListRoles(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &dynq.Query{}); err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if !repo.listed {
		t.Fatal("repo list not called")
	}
}

func TestRoleUsecaseGetRoleInvalidIDRejected(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if _, err := uc.GetRole(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
}

func TestRoleUsecaseSetRoleDataScopeRejectsOutOfRangeDataScope(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	for _, ds := range []int32{0, 8, -1} {
		_, err := uc.SetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1},
			&RoleDataScope{RoleID: 1, DataScope: ds})
		if !errors.Is(err, ErrRoleInvalidArgument) {
			t.Fatalf("data_scope %d: err = %v, want ErrRoleInvalidArgument", ds, err)
		}
	}
	if repo.scopeSet != nil {
		t.Fatalf("repo scopeSet = %+v, want untouched", repo.scopeSet)
	}
}

func TestRoleUsecaseSetRoleDataScopeRejectsBadEntriesAndDepts(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)
	badScope := int64(0)

	cases := map[string]*RoleDataScope{
		"missing role id":      {RoleID: 0, DataScope: RoleDataScopeSelf},
		"non-positive dept":    {RoleID: 1, DataScope: RoleDataScopeSelf, ScopeDeptID: &badScope},
		"entry type unknown":   {RoleID: 1, DataScope: RoleDataScopeUsers, IDScopes: []RoleIDScopeEntry{{Type: 4, DataID: 1}}},
		"entry data id empty":  {RoleID: 1, DataScope: RoleDataScopeUsers, IDScopes: []RoleIDScopeEntry{{Type: RoleIDScopeTypeUser, DataID: 0}}},
		"allowed dept invalid": {RoleID: 1, DataScope: RoleDataScopeSelf, AllowedDeptIDs: []int64{0}},
	}
	for name, cfg := range cases {
		_, err := uc.SetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1}, cfg)
		if !errors.Is(err, ErrRoleInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrRoleInvalidArgument", name, err)
		}
	}
	if repo.scopeSet != nil {
		t.Fatalf("repo scopeSet = %+v, want untouched", repo.scopeSet)
	}
}

func TestRoleUsecaseSetRoleDataScopeDedupsAndSortsInput(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	out, err := uc.SetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &RoleDataScope{
		RoleID:         3,
		DataScope:      RoleDataScopeDepts,
		IDScopes:       []RoleIDScopeEntry{{Type: RoleIDScopeTypeDept, DataID: 7}, {Type: RoleIDScopeTypeUser, DataID: 5}, {Type: RoleIDScopeTypeDept, DataID: 7}},
		AllowedDeptIDs: []int64{9, 2, 9},
	})
	if err != nil {
		t.Fatalf("set data scope: %v", err)
	}
	wantEntries := []RoleIDScopeEntry{{Type: RoleIDScopeTypeUser, DataID: 5}, {Type: RoleIDScopeTypeDept, DataID: 7}}
	if len(out.IDScopes) != 2 || out.IDScopes[0] != wantEntries[0] || out.IDScopes[1] != wantEntries[1] {
		t.Fatalf("id scopes = %+v, want %v", out.IDScopes, wantEntries)
	}
	if len(out.AllowedDeptIDs) != 2 || out.AllowedDeptIDs[0] != 2 || out.AllowedDeptIDs[1] != 9 {
		t.Fatalf("allowed dept ids = %v, want [2 9]", out.AllowedDeptIDs)
	}
	if repo.scopeSet == nil || repo.scopeSet.RoleID != 3 {
		t.Fatalf("repo scopeSet = %+v, want role 3", repo.scopeSet)
	}
}

func TestRoleUsecaseSetRoleDataScopePropagatesRepoError(t *testing.T) {
	repo := &mockRoleRepo{scopeSetErr: ErrRoleInvalidArgument}
	uc := NewRoleUsecase(repo)

	_, err := uc.SetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&RoleDataScope{RoleID: 1, DataScope: RoleDataScopeSelf})
	if !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
}

func TestRoleUsecaseSetRoleMenusDedupsSortsAndAllowsEmptySet(t *testing.T) {
	repo := &mockRoleRepo{menusResult: []int64{2, 5}}
	uc := NewRoleUsecase(repo)

	out, err := uc.SetRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 4, []int64{9, 2, 9})
	if err != nil {
		t.Fatalf("set menus: %v", err)
	}
	if len(repo.menusSet) != 2 || repo.menusSet[0] != 2 || repo.menusSet[1] != 9 {
		t.Fatalf("repo menus = %v, want [2 9]", repo.menusSet)
	}
	if repo.menusSetID != 4 {
		t.Fatalf("repo role id = %d, want 4", repo.menusSetID)
	}
	if len(out) != 2 || out[0] != 2 || out[1] != 5 {
		t.Fatalf("result = %v, want [2 5]", out)
	}

	// 空集合合法：清空全部授权。
	if _, err := uc.SetRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 4, nil); err != nil {
		t.Fatalf("set menus empty: %v", err)
	}
	if len(repo.menusSet) != 0 {
		t.Fatalf("repo menus = %v, want empty", repo.menusSet)
	}
}

func TestRoleUsecaseSetRoleMenusRejectsInvalidInput(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if _, err := uc.SetRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, []int64{1}); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("missing role id: err = %v, want ErrRoleInvalidArgument", err)
	}
	if _, err := uc.SetRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 1, []int64{1, 0}); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("non-positive menu id: err = %v, want ErrRoleInvalidArgument", err)
	}
	if repo.menusSetID != 0 {
		t.Fatalf("repo menusSetID = %d, want untouched", repo.menusSetID)
	}
}

func TestRoleUsecaseGetRoleDataScopeAndMenusForwardID(t *testing.T) {
	repo := &mockRoleRepo{}
	uc := NewRoleUsecase(repo)

	if _, err := uc.GetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 6); err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if repo.scopeReadID != 6 {
		t.Fatalf("scope read id = %d, want 6", repo.scopeReadID)
	}
	if _, err := uc.ListRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 6); err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if repo.menusReadID != 6 {
		t.Fatalf("menus read id = %d, want 6", repo.menusReadID)
	}
	if _, err := uc.GetRoleDataScope(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if _, err := uc.ListRoleMenus(context.Background(), Identity{TenantID: "t-1", UserID: 1}, -1); !errors.Is(err, ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
}
