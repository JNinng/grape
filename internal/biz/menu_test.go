package biz

import (
	"context"
	"errors"
	"testing"
)

// mockMenuRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与领域
// 校验，不触真实存储。
type mockMenuRepo struct {
	MenuRepo

	gotAuth   Identity
	getByIDID int64
	getErr    error
	listed    []int64 // ListAll 返回的菜单 ID 顺序
	listErr   error
	created   *Menu
	updated   *Menu
	movedID   int64
	movedTo   *int64
	deleted   []int64
	deleteErr error
}

func (m *mockMenuRepo) GetByID(_ context.Context, auth Identity, id int64) (*Menu, error) {
	m.gotAuth = auth
	m.getByIDID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &Menu{ID: id, Name: "parent", PermissionCode: "parent:code", MenuType: MenuTypeDirectory, Status: MenuStatusEnabled}, nil
}

func (m *mockMenuRepo) ListAll(_ context.Context, auth Identity) ([]*Menu, error) {
	m.gotAuth = auth
	if m.listErr != nil {
		return nil, m.listErr
	}
	byID := map[int64]*Menu{
		1: {ID: 1, Name: "root-a", MenuType: MenuTypeDirectory, Status: MenuStatusEnabled},
		2: {ID: 2, Name: "child", ParentID: int64Ptr(1), MenuType: MenuTypePage, Status: MenuStatusEnabled},
		3: {ID: 3, Name: "root-b", MenuType: MenuTypeDirectory, Status: MenuStatusEnabled},
	}
	out := make([]*Menu, 0, len(m.listed))
	for _, id := range m.listed {
		out = append(out, byID[id])
	}
	return out, nil
}

func (m *mockMenuRepo) CreateSuper(_ context.Context, auth Identity, mnu *Menu) (*Menu, error) {
	m.gotAuth = auth
	m.created = mnu
	out := *mnu
	out.ID = 100
	return &out, nil
}

func (m *mockMenuRepo) UpdateSuper(_ context.Context, auth Identity, mnu *Menu) (*Menu, error) {
	m.gotAuth = auth
	m.updated = mnu
	return mnu, nil
}

func (m *mockMenuRepo) MoveSuper(_ context.Context, auth Identity, id int64, parentID *int64) (*Menu, error) {
	m.gotAuth = auth
	m.movedID = id
	m.movedTo = parentID
	return &Menu{ID: id, ParentID: parentID, Name: "moved", MenuType: MenuTypePage, Status: MenuStatusEnabled}, nil
}

func (m *mockMenuRepo) SoftDeleteSuper(_ context.Context, auth Identity, id int64) error {
	m.gotAuth = auth
	m.deleted = append(m.deleted, id)
	return m.deleteErr
}

func int64Ptr(v int64) *int64 { return &v }

func TestMenuUsecaseCreateMenuSuperDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	out, err := uc.CreateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{Name: "menu", PermissionCode: "sys:menu:create", MenuType: MenuTypeButton, Status: MenuStatusUnspecified})
	if err != nil {
		t.Fatalf("create menu: %v", err)
	}
	if out.Status != MenuStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.PermissionCode != "sys:menu:create" {
		t.Fatalf("repo created = %+v, want permission code sys:menu:create", repo.created)
	}
}

func TestMenuUsecaseCreateMenuSuperWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	if _, err := uc.CreateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{Name: "paused", PermissionCode: "p", MenuType: MenuTypeButton, Status: MenuStatusDisabled}); err != nil {
		t.Fatalf("create menu: %v", err)
	}
	if repo.created == nil || repo.created.Status != MenuStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
}

func TestMenuUsecaseCreateMenuSuperUnknownParentRejectedAndNothingWritten(t *testing.T) {
	repo := &mockMenuRepo{getErr: ErrMenuNotFound}
	uc := NewMenuUsecase(repo)
	parent := int64Ptr(404)

	_, err := uc.CreateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{Name: "orphan", PermissionCode: "p", MenuType: MenuTypePage, ParentID: parent})
	if !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("err = %v, want ErrMenuInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestMenuUsecaseCreateMenuSuperParentLookupErrorPropagates(t *testing.T) {
	// 父级点查的非 NotFound 错误（如存储故障）原样透传，不吞成参数错误。
	repo := &mockMenuRepo{getErr: errors.New("storage down")}
	uc := NewMenuUsecase(repo)
	parent := int64Ptr(9)

	_, err := uc.CreateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{Name: "m", PermissionCode: "p", MenuType: MenuTypePage, ParentID: parent})
	if err == nil || errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("err = %v, want raw storage error", err)
	}
}

func TestMenuUsecaseCreateMenuSuperInvalidFieldsRejected(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)
	longName := string(make([]rune, menuNameMaxRunes+1))
	longCode := string(make([]rune, menuPermissionCodeMaxRunes+1))
	remark := string(make([]rune, menuRemarkMaxRunes+1))

	cases := map[string]*Menu{
		"blank name":        {Name: "  ", PermissionCode: "p", MenuType: MenuTypeButton},
		"missing code":      {Name: "menu", MenuType: MenuTypeButton},
		"blank code":        {Name: "menu", PermissionCode: " ", MenuType: MenuTypeButton},
		"name over max":     {Name: longName, PermissionCode: "p", MenuType: MenuTypeButton},
		"code over max":     {Name: "menu", PermissionCode: longCode, MenuType: MenuTypeButton},
		"remark over max":   {Name: "menu", PermissionCode: "p", MenuType: MenuTypeButton, Remark: &remark},
		"menu type zero":    {Name: "menu", PermissionCode: "p", MenuType: 0},
		"menu type unknown": {Name: "menu", PermissionCode: "p", MenuType: 4},
		"status invalid":    {Name: "menu", PermissionCode: "p", MenuType: MenuTypeButton, Status: 2},
		"nil input":         nil,
	}
	for name, m := range cases {
		if _, err := uc.CreateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, m); !errors.Is(err, ErrMenuInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrMenuInvalidArgument", name, err)
		}
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want untouched", repo.created)
	}
}

func TestMenuUsecaseUpdateMenuSuperForwardsMutableFields(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)
	icon := "icon-new"
	sortOrder := int32(7)

	out, err := uc.UpdateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{ID: 5, Status: MenuStatusDisabled, Name: "renamed", MenuType: MenuTypeButton, Icon: &icon, SortOrder: sortOrder})
	if err != nil {
		t.Fatalf("update menu: %v", err)
	}
	if out.ID != 5 {
		t.Fatalf("id = %d, want 5", out.ID)
	}
	if repo.updated == nil || repo.updated.ID != 5 || repo.updated.Status != MenuStatusDisabled || repo.updated.Name != "renamed" || repo.updated.Icon == nil || *repo.updated.Icon != "icon-new" || repo.updated.SortOrder != 7 {
		t.Fatalf("repo updated = %+v, want forwarded mutable fields", repo.updated)
	}
}

func TestMenuUsecaseUpdateMenuSuperInvalidInputRejected(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	if _, err := uc.UpdateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{ID: 0, Status: MenuStatusEnabled, Name: "x", MenuType: MenuTypeButton}); !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("missing id: err = %v, want ErrMenuInvalidArgument", err)
	}
	if _, err := uc.UpdateMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1},
		&Menu{ID: 5, Status: 3, Name: "x", MenuType: MenuTypeButton}); !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("bad status: err = %v, want ErrMenuInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo updated = %+v, want untouched", repo.updated)
	}
}

func TestMenuUsecaseMoveMenuSuperRejectsSelfParentUnknownParentAndCycle(t *testing.T) {
	// 树：1(root-a) ← 2(child)。把 1 移到 2 之下即移入自身子树。
	repo := &mockMenuRepo{listed: []int64{1, 2, 3}}
	uc := NewMenuUsecase(repo)
	self := int64Ptr(1)
	unknown := int64Ptr(404)
	child := int64Ptr(2)

	cases := map[string]*int64{
		"self parent":    self,
		"unknown parent": unknown,
		"cycle subtree":  child,
	}
	for name, parent := range cases {
		if _, err := uc.MoveMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 1, parent); !errors.Is(err, ErrMenuInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrMenuInvalidArgument", name, err)
		}
	}
	if repo.movedID != 0 {
		t.Fatalf("repo moved id = %d, want untouched", repo.movedID)
	}
}

func TestMenuUsecaseMoveMenuSuperAllowsCrossBranchAndRoot(t *testing.T) {
	repo := &mockMenuRepo{listed: []int64{1, 2, 3}}
	uc := NewMenuUsecase(repo)
	rootB := int64Ptr(3)

	// 2（root-a 的子节点）移到 root-b 之下：跨枝移动放行。
	if _, err := uc.MoveMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, rootB); err != nil {
		t.Fatalf("move cross branch: %v", err)
	}
	if repo.movedID != 2 || repo.movedTo == nil || *repo.movedTo != 3 {
		t.Fatalf("repo move = (%d, %+v), want (2, 3)", repo.movedID, repo.movedTo)
	}

	// parent 为 nil 表示移为根，直接放行。
	repo2 := &mockMenuRepo{listed: []int64{1, 2, 3}}
	uc2 := NewMenuUsecase(repo2)
	if _, err := uc2.MoveMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, nil); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	if repo2.movedID != 2 || repo2.movedTo != nil {
		t.Fatalf("repo move = (%d, %+v), want (2, nil)", repo2.movedID, repo2.movedTo)
	}
}

func TestMenuUsecaseMoveMenuSuperInvalidIDRejected(t *testing.T) {
	repo := &mockMenuRepo{listed: []int64{1, 2, 3}}
	uc := NewMenuUsecase(repo)

	if _, err := uc.MoveMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, nil); !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("err = %v, want ErrMenuInvalidArgument", err)
	}
	if repo.movedID != 0 {
		t.Fatalf("repo moved id = %d, want untouched", repo.movedID)
	}
}

func TestMenuUsecaseDeleteMenuSuperForwardsID(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	if err := uc.DeleteMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 7); err != nil {
		t.Fatalf("delete menu: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 7 {
		t.Fatalf("repo deleted = %v, want [7]", repo.deleted)
	}
}

func TestMenuUsecaseDeleteMenuSuperInvalidIDRejected(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	if err := uc.DeleteMenuSuper(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("err = %v, want ErrMenuInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("repo deleted = %v, want untouched", repo.deleted)
	}
}

func TestMenuUsecaseGetMenuTreeAssemblesNestedAndCountsOrphanAsRoot(t *testing.T) {
	// 平铺 [1 root-a, 2 child(of 1), 3 root-b]：树应为 root-a→child 与
	// root-b 两个根；child 缺父时（此处父存在）按平铺顺序嵌套。
	repo := &mockMenuRepo{listed: []int64{1, 2, 3}}
	uc := NewMenuUsecase(repo)

	roots, total, err := uc.GetMenuTree(context.Background(), Identity{TenantID: "t-1", UserID: 1})
	if err != nil {
		t.Fatalf("get menu tree: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(roots) != 2 || roots[0].Menu.ID != 1 || roots[1].Menu.ID != 3 {
		t.Fatalf("roots = %d nodes, want [1 3]", len(roots))
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Menu.ID != 2 {
		t.Fatalf("root-a children = %+v, want [2]", roots[0].Children)
	}
}

func TestMenuUsecaseGetMenuTreeOrphanFallsBackToRoot(t *testing.T) {
	// 父引用缺失的行（parent 指向不存在的 9）按根挂载：防御性兜底而非新语义。
	repo := &mockMenuRepo{listed: []int64{2}}
	uc := NewMenuUsecase(repo)

	roots, total, err := uc.GetMenuTree(context.Background(), Identity{TenantID: "t-1", UserID: 1})
	if err != nil {
		t.Fatalf("get menu tree: %v", err)
	}
	if total != 1 || len(roots) != 1 || roots[0].Menu.ID != 2 {
		t.Fatalf("tree = (total %d, roots %+v), want orphan 2 as single root", total, roots)
	}
}

func TestMenuUsecaseGetMenuInvalidIDRejected(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	if _, err := uc.GetMenu(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0); !errors.Is(err, ErrMenuInvalidArgument) {
		t.Fatalf("err = %v, want ErrMenuInvalidArgument", err)
	}
	if repo.getByIDID != 0 {
		t.Fatalf("repo get id = %d, want untouched", repo.getByIDID)
	}
}

func TestMenuUsecaseGetMenuForwardsID(t *testing.T) {
	repo := &mockMenuRepo{}
	uc := NewMenuUsecase(repo)

	out, err := uc.GetMenu(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 6)
	if err != nil {
		t.Fatalf("get menu: %v", err)
	}
	if out.ID != 6 {
		t.Fatalf("id = %d, want 6", out.ID)
	}
	if repo.getByIDID != 6 {
		t.Fatalf("repo get id = %d, want 6", repo.getByIDID)
	}
}
