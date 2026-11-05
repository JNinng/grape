package pg_sqlc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"grape/internal/biz"

	"github.com/jackc/pgx/v5/pgconn"
)

// 菜单仓储集成测试：以真实 PostgreSQL 验证 sys_menu 读面（RLS 租户边界、
// 树列表）与受控写面——grape_u 对 sys_menu 无直写权限（role.sql REVOKE），
// 全部写入经 98_menu_write.sql 的 SECURITY DEFINER 固定口径写函数
//（fn_menu_create/update/move/delete：租户边界复位、父级校验、防环、软删
// 口径收在函数内）。复用根包套件的 setupTestDB（deploy 全清单装载，
// 种子数据见 sqlc/seed.sql 与 sqlc/99_perm_seed.sql：t-super-01 有菜单树
// 201-265 + 257/266-273 共 73 行（246-249/254-256/258-265/257/266-273 由
// 99_perm_seed.sql 预铺），
// t-demo-01 有菜单树 1201-1237 共 9 行）。各用例经
// setupTestDB 重建数据库实现隔离；新建菜单用 newID() 发号 + 独立权限码，
// 不与种子冲突。

const (
	// menuTenantSeeded 是种子菜单树 201-265 所在租户。
	menuTenantSeeded = "t-super-01"
	// menuTenantOther 是种子菜单树 1201-1237 所在租户，供新建子树的用例使用。
	menuTenantOther = "t-demo-01"
)

// menuAuth 返回指定租户的操作者身份；菜单表无 @scope，操作者仅落入审计列。
func menuAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: 1}
}

// mustCreateMenu 在指定租户内经受控写函数创建菜单，失败即终止用例。
func mustCreateMenu(t *testing.T, d *Data, auth biz.Identity, m *biz.Menu) *biz.Menu {
	t.Helper()
	out, err := NewMenuRepo(d).CreateSuper(context.Background(), auth, m)
	if err != nil {
		t.Fatalf("create menu %s: %v", m.Name, err)
	}
	return out
}

// menuCount 统计当前租户内全部未删除菜单数。
func menuCount(t *testing.T, d *Data, auth biz.Identity) int {
	t.Helper()
	rows, err := NewMenuRepo(d).ListAll(context.Background(), auth)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	return len(rows)
}

// menuParentOf 读取指定菜单的父级（经 RLS 读面）。
func menuParentOf(t *testing.T, d *Data, auth biz.Identity, id int64) *int64 {
	t.Helper()
	m, err := NewMenuRepo(d).GetByID(context.Background(), auth, id)
	if err != nil {
		t.Fatalf("get menu %d: %v", id, err)
	}
	return m.ParentID
}

// menuSoftDeleted 读取指定菜单行的 deleted_at 是否已置位（软删行仍物理存在）。
func menuSoftDeleted(t *testing.T, d *Data, auth biz.Identity, id int64) bool {
	t.Helper()
	var deleted bool
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT deleted_at IS NOT NULL FROM public.sys_menu WHERE id = $1`, id).Scan(&deleted)
	})
	if err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	return deleted
}

func TestMenuRepo_create_super_writes_via_controlled_function(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)

	created := mustCreateMenu(t, d, auth, &biz.Menu{
		Name: "new-root", PermissionCode: "test:new-root", MenuType: biz.MenuTypeDirectory,
		Status: biz.MenuStatusEnabled,
	})
	if created.ID == 0 || created.CreatedBy != auth.UserID || created.UpdatedBy != auth.UserID {
		t.Fatalf("created = (%d, by %d/%d), want positive id and operator audit columns",
			created.ID, created.CreatedBy, created.UpdatedBy)
	}
	if menuSoftDeleted(t, d, auth, created.ID) {
		t.Fatal("fresh row already soft-deleted")
	}

	got, err := NewMenuRepo(d).GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get created menu: %v", err)
	}
	if got.Name != "new-root" || got.PermissionCode != "test:new-root" || got.MenuType != biz.MenuTypeDirectory {
		t.Fatalf("row = (%s, %s, %d), want (new-root, test:new-root, directory)", got.Name, got.PermissionCode, got.MenuType)
	}
}

func TestMenuRepo_direct_write_denied_for_runtime_role(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)

	// role.sql 对 grape_u REVOKE sys_menu 写授权：直写被权限拒绝（42501），
	// 受控写函数是唯一写路径。
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`INSERT INTO public.sys_menu (id, status, "name", permission_code, menu_type, created_by, updated_by, tenant_id)
			 VALUES ($1, 1, 'smuggled', 'test:smuggled', 1, 1, 1, current_setting('app.tenant_id'))`, newID())
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("direct insert err = %v, want permission denied (42501)", err)
	}
	if !strings.Contains(pgErr.Message, "permission denied") {
		t.Fatalf("pg message = %q, want permission denied", pgErr.Message)
	}
}

func TestMenuRepo_create_with_unknown_parent_fails_and_writes_nothing(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	ghost := int64(987654321)
	before := menuCount(t, d, auth)

	if _, err := NewMenuRepo(d).CreateSuper(context.Background(), auth, &biz.Menu{
		Name: "orphan", PermissionCode: "test:orphan", MenuType: biz.MenuTypePage, ParentID: &ghost,
	}); err == nil {
		t.Fatal("create with unknown parent = nil error, want function rejection")
	}
	if after := menuCount(t, d, auth); after != before {
		t.Fatalf("menu count after rejected create = %d, want unchanged %d", after, before)
	}
}

func TestMenuRepo_create_with_cross_tenant_parent_rejected(t *testing.T) {
	d := setupTestDB(t)
	// 菜单 201 属 t-super-01；t-demo-01 的写路径经受控函数只见本租户行，
	// 跨租户父级等同不存在。
	auth := menuAuth(menuTenantOther)
	seededParent := int64(201)
	before := menuCount(t, d, auth)

	if _, err := NewMenuRepo(d).CreateSuper(context.Background(), auth, &biz.Menu{
		Name: "smuggled-child", PermissionCode: "test:cross-tenant", MenuType: biz.MenuTypePage, ParentID: &seededParent,
	}); err == nil {
		t.Fatal("create with cross-tenant parent = nil error, want function rejection")
	}
	if after := menuCount(t, d, auth); after != before {
		t.Fatalf("menu count after rejected create = %d, want unchanged %d", after, before)
	}
}

func TestMenuRepo_create_duplicate_permission_code_maps_already_exists(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	mustCreateMenu(t, d, auth, &biz.Menu{Name: "first", PermissionCode: "test:dup", MenuType: biz.MenuTypeDirectory})
	if _, err := repo.CreateSuper(context.Background(), auth, &biz.Menu{
		Name: "second", PermissionCode: "test:dup", MenuType: biz.MenuTypeDirectory,
	}); !errors.Is(err, biz.ErrMenuAlreadyExists) {
		t.Fatalf("err = %v, want ErrMenuAlreadyExists", err)
	}
}

func TestMenuRepo_list_all_scopes_to_current_tenant(t *testing.T) {
	d := setupTestDB(t)
	repo := NewMenuRepo(d)

	other, err := repo.ListAll(context.Background(), menuAuth(menuTenantOther))
	if err != nil {
		t.Fatalf("list menus in t-demo-01: %v", err)
	}
	if len(other) != 9 {
		t.Fatalf("t-demo-01 rows = %d, want 9 (seed menus 1201-1237)", len(other))
	}
	if other[0].ID != 1201 {
		t.Fatalf("first row id = %d, want 1201 (sort_order ASC, id ASC)", other[0].ID)
	}

	seeded, err := repo.ListAll(context.Background(), menuAuth(menuTenantSeeded))
	if err != nil {
		t.Fatalf("list menus in t-super-01: %v", err)
	}
	if len(seeded) != 73 {
		t.Fatalf("t-super-01 rows = %d, want 73 (seed menus 201-265 + 权限码种子 257/266-273)", len(seeded))
	}
}

func TestMenuRepo_list_all_orders_children_after_parents_by_sort_order(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	root := mustCreateMenu(t, d, auth, &biz.Menu{Name: "order-root", PermissionCode: "test:order-root", MenuType: biz.MenuTypeDirectory, SortOrder: 1000})
	rid := root.ID
	mustCreateMenu(t, d, auth, &biz.Menu{Name: "order-child", PermissionCode: "test:order-child", MenuType: biz.MenuTypePage, ParentID: &rid, SortOrder: 1001})

	rows, err := repo.ListAll(context.Background(), auth)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	// 新建子树整体排在种子之后（sort_order 1000/1001 高于种子全部排序号，
	// 同序时 id ASC 稳定）；断言 root 紧邻 child 之前。
	found := -1
	for i, m := range rows {
		if m.ID == root.ID {
			found = i
			break
		}
	}
	if found < 0 || found+1 >= len(rows) || rows[found+1].PermissionCode != "test:order-child" {
		t.Fatalf("rows around new root = %v, want child immediately after root", rows[max(0, found-1):min(found+3, len(rows))])
	}
}

func TestMenuRepo_get_by_id_hides_cross_tenant_row(t *testing.T) {
	d := setupTestDB(t)
	// 菜单 201 属 t-super-01；t-demo-01 的身份经 RLS 不可见，等同不存在。
	if _, err := NewMenuRepo(d).GetByID(context.Background(), menuAuth(menuTenantOther), 201); !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrMenuNotFound", err)
	}
}

func TestMenuRepo_get_by_id_missing_maps_not_found(t *testing.T) {
	d := setupTestDB(t)

	if _, err := NewMenuRepo(d).GetByID(context.Background(), menuAuth(menuTenantSeeded), 424242); !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("err = %v, want ErrMenuNotFound", err)
	}
}

func TestMenuRepo_update_changes_mutable_fields(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	created := mustCreateMenu(t, d, auth, &biz.Menu{Name: "before", PermissionCode: "test:update", MenuType: biz.MenuTypePage, Status: biz.MenuStatusEnabled})
	created.Name = "after"
	created.Status = biz.MenuStatusDisabled
	icon := "shield"
	created.Icon = &icon
	created.SortOrder = 9
	if _, err := repo.UpdateSuper(context.Background(), auth, created); err != nil {
		t.Fatalf("update menu: %v", err)
	}

	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Status != biz.MenuStatusDisabled || got.Icon == nil || *got.Icon != "shield" || got.SortOrder != 9 {
		t.Fatalf("row = %+v, want updated mutable fields", got)
	}
	// 身份/类型/结构列不经更新路径变更。
	if got.PermissionCode != "test:update" || got.MenuType != biz.MenuTypePage {
		t.Fatalf("row = (%s, %d), want identity columns intact", got.PermissionCode, got.MenuType)
	}
}

func TestMenuRepo_update_missing_maps_not_found(t *testing.T) {
	d := setupTestDB(t)

	_, err := NewMenuRepo(d).UpdateSuper(context.Background(), menuAuth(menuTenantSeeded),
		&biz.Menu{ID: 424242, Status: biz.MenuStatusEnabled, Name: "ghost", MenuType: biz.MenuTypePage})
	if !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("err = %v, want ErrMenuNotFound", err)
	}
}

func TestMenuRepo_move_under_other_root_rewrites_parent(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	rootA := mustCreateMenu(t, d, auth, &biz.Menu{Name: "move-root-a", PermissionCode: "test:mv-a", MenuType: biz.MenuTypeDirectory})
	rootB := mustCreateMenu(t, d, auth, &biz.Menu{Name: "move-root-b", PermissionCode: "test:mv-b", MenuType: biz.MenuTypeDirectory})
	pa := rootA.ID
	child := mustCreateMenu(t, d, auth, &biz.Menu{Name: "move-child", PermissionCode: "test:mv-c", MenuType: biz.MenuTypePage, ParentID: &pa})
	before := menuCount(t, d, auth)

	if _, err := repo.MoveSuper(context.Background(), auth, child.ID, &rootB.ID); err != nil {
		t.Fatalf("move subtree: %v", err)
	}
	if got := menuParentOf(t, d, auth, child.ID); got == nil || *got != rootB.ID {
		t.Fatalf("child parent = %+v, want %d", got, rootB.ID)
	}
	if after := menuCount(t, d, auth); after != before {
		t.Fatalf("menu count after move = %d, want unchanged %d", after, before)
	}
}

func TestMenuRepo_move_to_root_clears_parent(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	root := mustCreateMenu(t, d, auth, &biz.Menu{Name: "root-r", PermissionCode: "test:rt", MenuType: biz.MenuTypeDirectory})
	pr := root.ID
	child := mustCreateMenu(t, d, auth, &biz.Menu{Name: "child-r", PermissionCode: "test:rc", MenuType: biz.MenuTypePage, ParentID: &pr})

	if _, err := repo.MoveSuper(context.Background(), auth, child.ID, nil); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	if got := menuParentOf(t, d, auth, child.ID); got != nil {
		t.Fatalf("child parent = %+v, want nil (root)", got)
	}
}

func TestMenuRepo_move_into_own_subtree_rejected_and_rolled_back(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	root := mustCreateMenu(t, d, auth, &biz.Menu{Name: "root-c", PermissionCode: "test:rc-c", MenuType: biz.MenuTypeDirectory})
	pr := root.ID
	child := mustCreateMenu(t, d, auth, &biz.Menu{Name: "child-c", PermissionCode: "test:cc-c", MenuType: biz.MenuTypePage, ParentID: &pr})

	// 根移到其直接子节点之下：受控函数防环拒绝，整事务回滚。
	if _, err := repo.MoveSuper(context.Background(), auth, root.ID, &child.ID); err == nil {
		t.Fatal("move into own subtree = nil error, want function rejection")
	}
	if got := menuParentOf(t, d, auth, root.ID); got != nil {
		t.Fatalf("root parent after rejected move = %+v, want nil (rolled back)", got)
	}
	if got := menuParentOf(t, d, auth, child.ID); got == nil || *got != root.ID {
		t.Fatalf("child parent after rejected move = %+v, want %d", got, root.ID)
	}
}

func TestMenuRepo_move_missing_maps_not_found(t *testing.T) {
	d := setupTestDB(t)
	target := int64(201)

	_, err := NewMenuRepo(d).MoveSuper(context.Background(), menuAuth(menuTenantSeeded), 424242, &target)
	if !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("err = %v, want ErrMenuNotFound", err)
	}
}

func TestMenuRepo_delete_leaf_soft_deletes_and_hides_row(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	root := mustCreateMenu(t, d, auth, &biz.Menu{Name: "root-d", PermissionCode: "test:rt-d", MenuType: biz.MenuTypeDirectory})
	pr := root.ID
	child := mustCreateMenu(t, d, auth, &biz.Menu{Name: "child-d", PermissionCode: "test:cd-d", MenuType: biz.MenuTypePage, ParentID: &pr})

	if err := repo.SoftDeleteSuper(context.Background(), auth, child.ID); err != nil {
		t.Fatalf("delete leaf: %v", err)
	}
	// 软删行等同不存在；物理行保留（deleted_at 置位）。
	if _, err := repo.GetByID(context.Background(), auth, child.ID); !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("get after delete = %v, want ErrMenuNotFound", err)
	}
	if !menuSoftDeleted(t, d, auth, child.ID) {
		t.Fatalf("row %d still active, want soft-deleted", child.ID)
	}
	rows, err := repo.ListAll(context.Background(), auth)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, m := range rows {
		if m.ID == child.ID {
			t.Fatal("soft-deleted menu still in ListAll")
		}
	}
}

func TestMenuRepo_delete_with_active_children_maps_has_children(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantSeeded)
	repo := NewMenuRepo(d)

	// 种子菜单 201（系统管理）下有未删除的 202/209/214/219/224 等。
	if err := repo.SoftDeleteSuper(context.Background(), auth, 201); !errors.Is(err, biz.ErrMenuHasChildren) {
		t.Fatalf("err = %v, want ErrMenuHasChildren", err)
	}
	if _, err := repo.GetByID(context.Background(), auth, 201); err != nil {
		t.Fatalf("get after rejected delete = %v, want row intact", err)
	}
	if menuSoftDeleted(t, d, auth, 201) {
		t.Fatal("seed menu 201 soft-deleted, want intact")
	}
}

func TestMenuRepo_delete_missing_maps_not_found(t *testing.T) {
	d := setupTestDB(t)

	err := NewMenuRepo(d).SoftDeleteSuper(context.Background(), menuAuth(menuTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("err = %v, want ErrMenuNotFound", err)
	}
}

func TestMenuRepo_delete_twice_maps_not_found(t *testing.T) {
	d := setupTestDB(t)
	auth := menuAuth(menuTenantOther)
	repo := NewMenuRepo(d)

	created := mustCreateMenu(t, d, auth, &biz.Menu{Name: "once-only", PermissionCode: "test:once", MenuType: biz.MenuTypeButton})
	if err := repo.SoftDeleteSuper(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// 已软删行再删：目标未命中（0 行），映射 NotFound 而非重复置位。
	if err := repo.SoftDeleteSuper(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrMenuNotFound) {
		t.Fatalf("second delete = %v, want ErrMenuNotFound", err)
	}
}
