package pg_sqlc

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 部门仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* DSN 时跳过）：覆盖
// sys_dept 的 CRUD、动态查询、RLS 租户边界与闭包表触发器的建立/移动重写/
// 软删清理纪律。setupTestDB 每次重建数据库。

const (
	// testTenantSeeded 是种子部门树所在的租户（1=总公司、2=技术部、
	// 7/8=技术部的子组）。
	testTenantSeeded = "t-test-01"
	// testTenantEmpty 是无部门种子的租户，供新建树的用例使用。
	testTenantEmpty = "t-test-02"
)

// deptAuth 返回指定租户的种子操作者身份（tenantOperators 定义见
// role_repo_test.go）；归属字段校验（fn_can_set_owner）要求有效身份。
func deptAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: tenantOperators[tenant]}
}

// mustCreateDept 在指定租户内创建部门，失败即终止用例。
func mustCreateDept(t *testing.T, d *Data, auth biz.Identity, dept *biz.Dept) *biz.Dept {
	t.Helper()
	out, err := NewDeptRepo(d).Create(context.Background(), auth, dept)
	if err != nil {
		t.Fatalf("create dept %s: %v", dept.Name, err)
	}
	return out
}

// closureSet 读取当前租户内全部闭包路径，返回 "ancestor->descendant" → depth。
func closureSet(t *testing.T, d *Data, auth biz.Identity) map[string]int64 {
	t.Helper()
	paths := map[string]int64{}
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Tx().Query(context.Background(),
			`SELECT ancestor_id, descendant_id, depth FROM public.sys_dept_closure`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a, de, depth int64
			if err := rows.Scan(&a, &de, &depth); err != nil {
				return err
			}
			paths[strconv.FormatInt(a, 10)+"->"+strconv.FormatInt(de, 10)] = depth
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read closure: %v", err)
	}
	return paths
}

func closureKey(ancestor, descendant int64) string {
	return strconv.FormatInt(ancestor, 10) + "->" + strconv.FormatInt(descendant, 10)
}

// softDeleteState 读取指定部门行的 deleted_at 是否已置位（软删行仍物理存在）。
func softDeleteState(t *testing.T, d *Data, auth biz.Identity, id int64) bool {
	t.Helper()
	var deleted bool
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT deleted_at IS NOT NULL FROM public.sys_dept WHERE id = $1`, id).Scan(&deleted)
	})
	if err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	return deleted
}

func TestDeptRepo_create_root_builds_self_closure_path(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)

	created := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-0", OwnerUserID: 5})
	paths := closureSet(t, d, auth)
	if len(paths) != 1 || paths[closureKey(created.ID, created.ID)] != 0 {
		t.Fatalf("closure = %v, want only self path depth 0 for %d", paths, created.ID)
	}
}

func TestDeptRepo_create_child_links_ancestor_paths(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)

	root := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-1", OwnerUserID: 5})
	parent := root.ID
	child := mustCreateDept(t, d, auth, &biz.Dept{Name: "child-1", OwnerUserID: 5, ParentID: &parent})
	grand := child.ID
	leaf := mustCreateDept(t, d, auth, &biz.Dept{Name: "leaf-1", OwnerUserID: 5, ParentID: &grand})

	paths := closureSet(t, d, auth)
	want := map[string]int64{
		closureKey(root.ID, root.ID):   0,
		closureKey(child.ID, child.ID): 0,
		closureKey(leaf.ID, leaf.ID):   0,
		closureKey(root.ID, child.ID):  1,
		closureKey(child.ID, leaf.ID):  1,
		closureKey(root.ID, leaf.ID):   2,
	}
	if len(paths) != len(want) {
		t.Fatalf("closure = %v, want %v", paths, want)
	}
	for key, depth := range want {
		if paths[key] != depth {
			t.Fatalf("closure[%s] = %d, want %d (all: %v)", key, paths[key], depth, paths)
		}
	}
}

func TestDeptRepo_create_with_unknown_parent_fails_and_writes_nothing(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	ghost := int64(987654321)

	repo := NewDeptRepo(d)
	if _, err := repo.Create(context.Background(), auth, &biz.Dept{Name: "orphan", OwnerUserID: 5, ParentID: &ghost}); err == nil {
		t.Fatal("create with unknown parent = nil error, want trigger rejection")
	}
	if _, err := repo.GetByID(context.Background(), auth, ghost); !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("get unknown parent = %v, want ErrDeptNotFound", err)
	}
	paths := closureSet(t, d, auth)
	if len(paths) != 0 {
		t.Fatalf("closure = %v, want empty after rejected insert", paths)
	}
}

func TestDeptRepo_create_duplicate_name_maps_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantSeeded)

	// 归属人取种子操作者 11（t-test-01 内有效且对全租户操作者可见；
	// 种子行里的 owner_user_id=1 是悬空引用，Create 归属校验会先拒绝）。
	_, err := NewDeptRepo(d).Create(context.Background(), auth, &biz.Dept{Name: "总公司", OwnerUserID: 11})
	if !errors.Is(err, biz.ErrDeptAlreadyExists) {
		t.Fatalf("err = %v, want ErrDeptAlreadyExists", err)
	}
}

func TestDeptRepo_get_by_id_hides_cross_tenant_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	// 部门 1 属 t-test-01；t-test-02 的身份经 RLS 不可见，等同不存在。
	other := deptAuth(testTenantEmpty)

	_, err := NewDeptRepo(d).GetByID(context.Background(), other, 1)
	if !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrDeptNotFound", err)
	}
}

func TestDeptRepo_get_by_id_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewDeptRepo(d).GetByID(context.Background(), deptAuth(testTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("err = %v, want ErrDeptNotFound", err)
	}
}

func TestDeptRepo_list_all_scopes_to_current_tenant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := NewDeptRepo(d)

	empty, err := repo.ListAll(context.Background(), deptAuth(testTenantEmpty))
	if err != nil {
		t.Fatalf("list all in empty tenant: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty tenant rows = %d, want 0", len(empty))
	}
	seeded, err := repo.ListAll(context.Background(), deptAuth(testTenantSeeded))
	if err != nil {
		t.Fatalf("list all in seeded tenant: %v", err)
	}
	if len(seeded) != 8 {
		t.Fatalf("seeded tenant rows = %d, want 8 (seed depts 1..8)", len(seeded))
	}
	if seeded[0].Name != "总公司" {
		t.Fatalf("first row = %s, want 总公司 (created_at ASC ordering)", seeded[0].Name)
	}
}

func TestDeptRepo_list_filters_by_parent_id_with_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantSeeded)
	parent := int64(2)

	params, err := gen.ParseSysDeptQuery(&dynq.Query{Where: []dynq.Condition{{Field: "parentId", Op: "eq", Value: parent}}})
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	var names []string
	var total int64
	err = d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		res, err := gen.ListSysDept(context.Background(), s.Tx(), params)
		if err != nil {
			return err
		}
		for i := range res.Items {
			names = append(names, res.Items[i].Name)
		}
		total, err = gen.CountSysDept(context.Background(), s.Tx(), params)
		return err
	})
	if err != nil {
		t.Fatalf("list depts: %v", err)
	}
	if len(names) != 2 || total != 2 {
		t.Fatalf("rows = %v total = %d, want 2 rows and total 2", names, total)
	}
	if !((names[0] == "前端组" && names[1] == "后端组") || (names[0] == "后端组" && names[1] == "前端组")) {
		t.Fatalf("rows = %v, want 前端组 and 后端组", names)
	}
}

func TestDeptRepo_list_rejects_unknown_field(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewDeptRepo(d).List(context.Background(), deptAuth(testTenantSeeded),
		&dynq.Query{Where: []dynq.Condition{{Field: "ghost", Op: "eq", Value: 1}}})
	if !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
}

func TestDeptRepo_update_changes_name_and_status(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	created := mustCreateDept(t, d, auth, &biz.Dept{Name: "before", OwnerUserID: 5, Status: biz.DeptStatusEnabled})
	created.Name = "after"
	created.Status = biz.DeptStatusDisabled
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("update dept: %v", err)
	}
	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Status != biz.DeptStatusDisabled {
		t.Fatalf("row = (%s, %d), want (after, disabled)", got.Name, got.Status)
	}
}

func TestDeptRepo_update_owner_and_parent_rewrites_closure(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	rootA := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-a", OwnerUserID: 5})
	rootB := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-b", OwnerUserID: 5})
	child := mustCreateDept(t, d, auth, &biz.Dept{Name: "child", OwnerUserID: 5, ParentID: &rootA.ID})

	// 归属用户改为 t-test-02 内的有效用户（5）并移动父级：归属校验
	//（fn_can_set_owner）通过，闭包路径由触发器重写。
	child.ParentID = &rootB.ID
	child.OwnerUserID = 5
	updated, err := repo.Update(context.Background(), auth, child)
	if err != nil {
		t.Fatalf("update dept: %v", err)
	}
	if updated.OwnerUserID != 5 || updated.OwnerUserGroupID != nil {
		t.Fatalf("owner = (%d, %v), want (5, nil)", updated.OwnerUserID, updated.OwnerUserGroupID)
	}
	if updated.ParentID == nil || *updated.ParentID != rootB.ID {
		t.Fatalf("parent = %v, want %d", updated.ParentID, rootB.ID)
	}
	paths := closureSet(t, d, auth)
	if _, ok := paths[closureKey(rootB.ID, child.ID)]; !ok {
		t.Fatalf("closure missing new parent path: %v", paths)
	}
	if _, ok := paths[closureKey(rootA.ID, child.ID)]; ok {
		t.Fatalf("closure kept stale parent path: %v", paths)
	}
}

func TestDeptRepo_update_missing_dept_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewDeptRepo(d).Update(context.Background(), deptAuth(testTenantSeeded),
		&biz.Dept{ID: 424242, Name: "ghost", Status: biz.DeptStatusEnabled, OwnerUserID: 1})
	if !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("err = %v, want ErrDeptNotFound", err)
	}
}

func TestDeptRepo_move_under_other_root_rewrites_closure(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	rootA := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-a", OwnerUserID: 5})
	rootB := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-b", OwnerUserID: 5})
	pa := rootA.ID
	mid := mustCreateDept(t, d, auth, &biz.Dept{Name: "mid", OwnerUserID: 5, ParentID: &pa})
	pm := mid.ID
	leaf := mustCreateDept(t, d, auth, &biz.Dept{Name: "leaf", OwnerUserID: 5, ParentID: &pm})

	// mid 子树整体移到 root-b 之下。
	if _, err := repo.Move(context.Background(), auth, mid.ID, &rootB.ID); err != nil {
		t.Fatalf("move subtree: %v", err)
	}
	paths := closureSet(t, d, auth)
	want := map[string]int64{
		closureKey(rootA.ID, rootA.ID): 0,
		closureKey(rootB.ID, rootB.ID): 0,
		closureKey(mid.ID, mid.ID):     0,
		closureKey(leaf.ID, leaf.ID):   0,
		closureKey(rootB.ID, mid.ID):   1,
		closureKey(mid.ID, leaf.ID):    1,
		closureKey(rootB.ID, leaf.ID):  2,
	}
	if len(paths) != len(want) {
		t.Fatalf("closure = %v, want exactly %v", paths, want)
	}
	for key, depth := range want {
		if paths[key] != depth {
			t.Fatalf("closure[%s] = %d, want %d (all: %v)", key, paths[key], depth, paths)
		}
	}
	// 旧祖先 root-a 的全部路径断开。
	if _, ok := paths[closureKey(rootA.ID, mid.ID)]; ok {
		t.Fatalf("closure still links root-a -> mid: %v", paths)
	}
	if _, ok := paths[closureKey(rootA.ID, leaf.ID)]; ok {
		t.Fatalf("closure still links root-a -> leaf: %v", paths)
	}
}

func TestDeptRepo_move_to_root_clears_ancestor_paths(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	root := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-r", OwnerUserID: 5})
	pr := root.ID
	child := mustCreateDept(t, d, auth, &biz.Dept{Name: "child-r", OwnerUserID: 5, ParentID: &pr})

	// child 移为根部门。
	if _, err := repo.Move(context.Background(), auth, child.ID, nil); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	paths := closureSet(t, d, auth)
	want := map[string]int64{
		closureKey(root.ID, root.ID):   0,
		closureKey(child.ID, child.ID): 0,
	}
	if len(paths) != len(want) {
		t.Fatalf("closure = %v, want exactly %v", paths, want)
	}
	for key, depth := range want {
		if paths[key] != depth {
			t.Fatalf("closure[%s] = %d, want %d (all: %v)", key, paths[key], depth, paths)
		}
	}
}

func TestDeptRepo_move_into_own_subtree_rejected_and_rolled_back(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	root := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-c", OwnerUserID: 5})
	pr := root.ID
	child := mustCreateDept(t, d, auth, &biz.Dept{Name: "child-c", OwnerUserID: 5, ParentID: &pr})

	// 根移到其直接子节点之下：触发器拒绝，整事务回滚。
	if _, err := repo.Move(context.Background(), auth, root.ID, &child.ID); err == nil {
		t.Fatal("move into own subtree = nil error, want trigger rejection")
	}
	got, err := repo.GetByID(context.Background(), auth, root.ID)
	if err != nil {
		t.Fatalf("get after rejected move: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("parent after rejected move = %+v, want nil (rolled back)", got.ParentID)
	}
	paths := closureSet(t, d, auth)
	if paths[closureKey(root.ID, child.ID)] != 1 || len(paths) != 3 {
		t.Fatalf("closure after rejected move = %v, want unchanged 3 paths", paths)
	}
}

func TestDeptRepo_move_missing_dept_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantSeeded)
	target := int64(1)

	_, err := NewDeptRepo(d).Move(context.Background(), auth, 424242, &target)
	if !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("err = %v, want ErrDeptNotFound", err)
	}
}

func TestDeptRepo_delete_leaf_soft_deletes_and_cleans_closure(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantEmpty)
	repo := NewDeptRepo(d)

	root := mustCreateDept(t, d, auth, &biz.Dept{Name: "root-d", OwnerUserID: 5})
	pr := root.ID
	child := mustCreateDept(t, d, auth, &biz.Dept{Name: "child-d", OwnerUserID: 5, ParentID: &pr})

	if err := repo.SoftDelete(context.Background(), auth, child.ID); err != nil {
		t.Fatalf("delete leaf: %v", err)
	}
	// 软删行等同不存在；闭包路径被清理；物理行保留（deleted_at 置位）。
	if _, err := repo.GetByID(context.Background(), auth, child.ID); !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("get after delete = %v, want ErrDeptNotFound", err)
	}
	paths := closureSet(t, d, auth)
	if len(paths) != 1 || paths[closureKey(root.ID, root.ID)] != 0 {
		t.Fatalf("closure = %v, want only root self path", paths)
	}
	if !softDeleteState(t, d, auth, child.ID) {
		t.Fatalf("row %d still active, want soft-deleted", child.ID)
	}
}

func TestDeptRepo_delete_with_active_children_maps_has_children(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantSeeded)
	repo := NewDeptRepo(d)

	// 种子部门 2（技术部）下有未删除的 7/8。
	if err := repo.SoftDelete(context.Background(), auth, 2); !errors.Is(err, biz.ErrDeptHasChildren) {
		t.Fatalf("err = %v, want ErrDeptHasChildren", err)
	}
	if _, err := repo.GetByID(context.Background(), auth, 2); err != nil {
		t.Fatalf("get after rejected delete = %v, want row intact", err)
	}
	paths := closureSet(t, d, auth)
	if paths[closureKey(2, 7)] != 1 {
		t.Fatalf("closure[2->7] = %d, want 1 (deleted row rolled back)", paths[closureKey(2, 7)])
	}
}

func TestDeptRepo_delete_missing_dept_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewDeptRepo(d).SoftDelete(context.Background(), deptAuth(testTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrDeptNotFound) {
		t.Fatalf("err = %v, want ErrDeptNotFound", err)
	}
}

func TestDeptRepo_owner_reference_validated(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := deptAuth(testTenantSeeded)
	repo := NewDeptRepo(d)

	// 归属用户悬空（不存在/跨租户等同不存在）→ 整组拒绝，无写入。
	if _, err := repo.Create(context.Background(), auth, &biz.Dept{Name: "bad-owner", OwnerUserID: 424242}); !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("unknown owner user err = %v, want ErrDeptInvalidArgument", err)
	}
	// 归属用户组悬空同口径。
	group := int64(424242)
	if _, err := repo.Create(context.Background(), auth, &biz.Dept{Name: "bad-group", OwnerUserID: 2, OwnerUserGroupID: &group}); !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("unknown owner group err = %v, want ErrDeptInvalidArgument", err)
	}
	// 禁用归属对象同样拒绝（fn_can_set_owner 要求存在且启用）。
	execIdentityTx(t, d, auth, `UPDATE public.sys_user SET status = 0 WHERE id = 9`)
	if _, err := repo.Create(context.Background(), auth, &biz.Dept{Name: "disabled-owner", OwnerUserID: 9}); !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("disabled owner user err = %v, want ErrDeptInvalidArgument", err)
	}
	// 更新路径同口径：悬空归属引用拒绝且行不落变更。
	if _, err := repo.Update(context.Background(), auth, &biz.Dept{ID: 1, Name: "总公司", Status: biz.DeptStatusEnabled, OwnerUserID: 424242}); !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("unknown owner on update err = %v, want ErrDeptInvalidArgument", err)
	}
}
