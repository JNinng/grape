package service

import (
	"context"
	"errors"
	"testing"

	permv1 "grape/api/perm/v1"
	"grape/internal/biz"
)

// fakeMenuRepo 捕获用例转发的菜单 DO 与入参，供断言转换与身份透传。
type fakeMenuRepo struct {
	biz.MenuRepo

	gotAuth    biz.Identity
	callsCount int

	getID    int64
	listed   []*biz.Menu
	created  *biz.Menu
	updated  *biz.Menu
	movedID  int64
	movedTo  *int64
	deleted  []int64
	getErr   error
	parentID int64 // GetByID 返回的菜单 id
}

func (f *fakeMenuRepo) GetByID(_ context.Context, auth biz.Identity, id int64) (*biz.Menu, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &biz.Menu{ID: id, ParentID: &f.parentID, Name: "parent", PermissionCode: "parent:code", MenuType: biz.MenuTypeDirectory, Status: biz.MenuStatusEnabled}, nil
}

func (f *fakeMenuRepo) ListAll(_ context.Context, auth biz.Identity) ([]*biz.Menu, error) {
	f.callsCount++
	f.gotAuth = auth
	if f.listed == nil {
		p := int64(1)
		return []*biz.Menu{
			{ID: 1, Name: "root", PermissionCode: "root:code", MenuType: biz.MenuTypeDirectory, Status: biz.MenuStatusEnabled, SortOrder: 1},
			{ID: 2, Name: "child", PermissionCode: "child:code", ParentID: &p, MenuType: biz.MenuTypePage, Status: biz.MenuStatusEnabled, SortOrder: 2},
		}, nil
	}
	return f.listed, nil
}

func (f *fakeMenuRepo) CreateSuper(_ context.Context, auth biz.Identity, m *biz.Menu) (*biz.Menu, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = m
	out := *m
	out.ID = 9
	return &out, nil
}

func (f *fakeMenuRepo) UpdateSuper(_ context.Context, auth biz.Identity, m *biz.Menu) (*biz.Menu, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = m
	return m, nil
}

func (f *fakeMenuRepo) MoveSuper(_ context.Context, auth biz.Identity, id int64, parentID *int64) (*biz.Menu, error) {
	f.callsCount++
	f.gotAuth = auth
	f.movedID = id
	f.movedTo = parentID
	return &biz.Menu{ID: id, ParentID: parentID, Name: "moved", PermissionCode: "m:code", MenuType: biz.MenuTypePage, Status: biz.MenuStatusEnabled}, nil
}

func (f *fakeMenuRepo) SoftDeleteSuper(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deleted = append(f.deleted, id)
	return nil
}

func TestMenuServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetMenu(biz.WithIdentity(context.Background(), auth), &permv1.GetMenuRequest{Id: 5})
	if err != nil {
		t.Fatalf("get menu: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("menu id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestMenuServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))

	if _, err := svc.GetMenu(context.Background(), &permv1.GetMenuRequest{Id: 5}); !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestMenuServiceGetMenuTreeConvertsDTO(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))

	out, err := svc.GetMenuTree(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 1}), &permv1.GetMenuTreeRequest{})
	if err != nil {
		t.Fatalf("get menu tree: %v", err)
	}
	if out.Total != 2 || len(out.Roots) != 1 {
		t.Fatalf("tree = (total %d, roots %d), want (2, 1)", out.Total, len(out.Roots))
	}
	root := out.Roots[0]
	if root.Menu.Id != 1 || root.Menu.PermissionCode != "root:code" || root.Menu.MenuType != int32(biz.MenuTypeDirectory) {
		t.Fatalf("root = %+v, want menu 1 with root:code directory", root.Menu)
	}
	if len(root.Children) != 1 || root.Children[0].Menu.Id != 2 || root.Children[0].Menu.ParentId == nil || *root.Children[0].Menu.ParentId != 1 {
		t.Fatalf("root children = %+v, want menu 2 with parent 1", root.Children)
	}
}

func TestMenuServiceCreateMenuSuperMapsOptionalFieldsAndNormalizesStatus(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))
	parent := int64(3)
	route := "/system/new"
	status := int32(0)

	out, err := svc.CreateMenuSuper(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}),
		&permv1.CreateMenuSuperRequest{Menu: &permv1.Menu{
			Name: "new-menu", PermissionCode: "sys:new", ParentId: &parent, MenuType: int32(biz.MenuTypePage),
			RoutePath: &route, Status: &status, SortOrder: 5,
		}})
	if err != nil {
		t.Fatalf("create menu super: %v", err)
	}
	if out.GetId() != 9 {
		t.Fatalf("id = %d, want 9", out.GetId())
	}
	created := repo.created
	if created == nil {
		t.Fatal("repo created is nil")
	}
	// 显式 0 状态原样落库为禁用，不得并入缺省启用分支。
	if created.Status != biz.MenuStatusDisabled {
		t.Fatalf("status = %d, want disabled", created.Status)
	}
	if created.PermissionCode != "sys:new" || created.MenuType != int32(biz.MenuTypePage) || created.SortOrder != 5 {
		t.Fatalf("created = %+v, want forwarded identity fields", created)
	}
	if created.ParentID == nil || *created.ParentID != 3 || created.RoutePath == nil || *created.RoutePath != "/system/new" {
		t.Fatalf("created = %+v, want forwarded optional fields", created)
	}
}

func TestMenuServiceCreateMenuSuperMissingStatusDefaultsToEnabledAtRepo(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))

	if _, err := svc.CreateMenuSuper(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}),
		&permv1.CreateMenuSuperRequest{Menu: &permv1.Menu{Name: "m", PermissionCode: "p", MenuType: int32(biz.MenuTypeButton)}}); err != nil {
		t.Fatalf("create menu super: %v", err)
	}
	// 未指定哨兵在用例内落缺省启用，仓储只见最终取值。
	if repo.created.Status != biz.MenuStatusEnabled {
		t.Fatalf("status = %d, want enabled", repo.created.Status)
	}
}

func TestMenuServiceUpdateMenuSuperRequiresStatusAndIgnoresIdentityColumns(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	// status 缺失拒绝（optional 传达禁用语义）。
	if _, err := svc.UpdateMenuSuper(biz.WithIdentity(context.Background(), auth),
		&permv1.UpdateMenuSuperRequest{Menu: &permv1.Menu{Id: 5, Name: "x"}}); !errors.Is(err, biz.ErrMenuInvalidArgument) {
		t.Fatalf("missing status: err = %v, want ErrMenuInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}

	// 身份/类型/结构列在输入中被忽略：permission_code/menu_type/parent_id 不转发。
	code := "should-be-ignored"
	parent := int64(3)
	mtype := int32(biz.MenuTypeButton)
	status := int32(1)
	if _, err := svc.UpdateMenuSuper(biz.WithIdentity(context.Background(), auth),
		&permv1.UpdateMenuSuperRequest{Menu: &permv1.Menu{
			Id: 5, Status: &status, Name: "renamed", PermissionCode: code, MenuType: mtype, ParentId: &parent,
		}}); err != nil {
		t.Fatalf("update menu super: %v", err)
	}
	updated := repo.updated
	if updated.ID != 5 || updated.Name != "renamed" || updated.Status != biz.MenuStatusEnabled {
		t.Fatalf("updated = %+v, want (5, renamed, enabled)", updated)
	}
	if updated.PermissionCode != "" || updated.MenuType != 0 || updated.ParentID != nil {
		t.Fatalf("updated = %+v, want identity columns zeroed", updated)
	}
}

func TestMenuServiceMoveMenuSuperMapsParentSemantics(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	parent := int64(2) // 假仓储 ListAll 内存在的菜单 ID（父级存在性经用例校验）

	// parent_id 设置：转发新父级。
	if _, err := svc.MoveMenuSuper(biz.WithIdentity(context.Background(), auth),
		&permv1.MoveMenuSuperRequest{Id: 5, ParentId: &parent}); err != nil {
		t.Fatalf("move menu super: %v", err)
	}
	if repo.movedID != 5 || repo.movedTo == nil || *repo.movedTo != 2 {
		t.Fatalf("repo move = (%d, %+v), want (5, 2)", repo.movedID, repo.movedTo)
	}

	// parent_id 未设置：移为根（nil）。
	if _, err := svc.MoveMenuSuper(biz.WithIdentity(context.Background(), auth),
		&permv1.MoveMenuSuperRequest{Id: 5}); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	if repo.movedID != 5 || repo.movedTo != nil {
		t.Fatalf("repo move = (%d, %+v), want (5, nil)", repo.movedID, repo.movedTo)
	}
}

func TestMenuServiceDeleteMenuSuperForwardsID(t *testing.T) {
	repo := &fakeMenuRepo{}
	svc := NewMenuService(biz.NewMenuUsecase(repo))

	if _, err := svc.DeleteMenuSuper(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}),
		&permv1.DeleteMenuSuperRequest{Id: 8}); err != nil {
		t.Fatalf("delete menu super: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 8 {
		t.Fatalf("repo deleted = %v, want [8]", repo.deleted)
	}
}

func TestMenuServiceGetMenuGetErrorPropagates(t *testing.T) {
	repo := &fakeMenuRepo{getErr: biz.ErrMenuNotFound}
	svc := NewMenuService(biz.NewMenuUsecase(repo))

	if _, err := svc.GetMenu(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}),
		&permv1.GetMenuRequest{Id: 5}); !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("err = %v, want ErrMenuNotFound", err)
	}
}
