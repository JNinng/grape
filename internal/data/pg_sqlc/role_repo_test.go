package pg_sqlc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// 角色仓储集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* DSN 时跳过）：覆盖
// sys_role 固定 CRUD、动态查询、数据范围明细与分发部门的差量同步、菜单授权
// 整组替换、RLS 租户边界与 fn_can_assign_role 回归。种子见 sqlc/test_seed.sql，
// 每次重建数据库。

const (
	// roleTenantSeeded 是种子角色所在的租户。
	roleTenantSeeded = "t-test-01"
	// roleTenantEmpty 是无角色种子的租户，供新建角色的用例使用。
	roleTenantEmpty = "t-test-02"
)

// tenantOperators 是各测试租户内有效（启用且未删除）的种子操作者：
// 角色可见性判定与归属校验都要求有效身份——t-test-01 取 11（testadmin，
// data_scope=7）、t-test-02 取 5（租户管理员，data_scope=7）、
// t-super-01 取 1（admin）。
var tenantOperators = map[string]int64{
	"t-test-01":  11,
	"t-test-02":  5,
	"t-super-01": 1,
}

// roleAuth 返回指定租户的种子操作者身份。
func roleAuth(tenant string) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: tenantOperators[tenant]}
}

// mustCreateRole 在指定租户内创建角色，失败即终止用例。
func mustCreateRole(t *testing.T, d *Data, auth biz.Identity, r *biz.Role) *biz.Role {
	t.Helper()
	out, err := NewRoleRepo(d).Create(context.Background(), auth, r)
	if err != nil {
		t.Fatalf("create role %s: %v", r.Name, err)
	}
	return out
}

// mustSetDataScope 设置角色数据范围配置，失败即终止用例。
func mustSetDataScope(t *testing.T, d *Data, auth biz.Identity, cfg *biz.RoleDataScope) *biz.RoleDataScope {
	t.Helper()
	out, err := NewRoleRepo(d).SetDataScope(context.Background(), auth, cfg)
	if err != nil {
		t.Fatalf("set data scope for role %d: %v", cfg.RoleID, err)
	}
	return out
}

// mustCanAssignRole 执行既有分配判定查询，失败即终止用例。
func mustCanAssignRole(t *testing.T, d *Data, auth biz.Identity, operator, roleID, target int64) bool {
	t.Helper()
	var ok bool
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		got, err := s.Q().CanAssignRole(context.Background(), gen.CanAssignRoleParams{Operator: operator, Role: roleID, Target: target})
		if err != nil {
			return err
		}
		ok = got
		return nil
	})
	if err != nil {
		t.Fatalf("can assign role: %v", err)
	}
	return ok
}

// roleIDScopeRows 读取角色的数据范围明细活跃行（type, data_id 对），按
// 查询自身的排序返回；物理行校验用。
func roleIDScopeRows(t *testing.T, d *Data, auth biz.Identity, roleID int64) []biz.RoleIDScopeEntry {
	t.Helper()
	var out []biz.RoleIDScopeEntry
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListSysRoleIDScopes(context.Background(), roleID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, biz.RoleIDScopeEntry{Type: int32(r.Type), DataID: r.DataID})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read id scope rows: %v", err)
	}
	return out
}

// roleAllowedDeptRows 读取角色的分发部门活跃行；物理行校验用。
func roleAllowedDeptRows(t *testing.T, d *Data, auth biz.Identity, roleID int64) []int64 {
	t.Helper()
	var out []int64
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListSysRoleAllowedDepts(context.Background(), roleID)
		if err != nil {
			return err
		}
		out = append(out, rows...)
		return nil
	})
	if err != nil {
		t.Fatalf("read allowed dept rows: %v", err)
	}
	return out
}

// roleMenuRowsWithCreator 读取角色-菜单映射物理行（含授权人），按 menu_id
// 升序返回 (menu_id, created_by) 对。
func roleMenuRowsWithCreator(t *testing.T, d *Data, auth biz.Identity, roleID int64) []struct {
	MenuID    int64
	CreatedBy int64
} {
	t.Helper()
	var out []struct {
		MenuID    int64
		CreatedBy int64
	}
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Tx().Query(context.Background(),
			`SELECT menu_id, created_by FROM public.sys_role_menu WHERE role_id = $1 ORDER BY menu_id`, roleID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var menuID, createdBy int64
			if err := rows.Scan(&menuID, &createdBy); err != nil {
				return err
			}
			out = append(out, struct {
				MenuID    int64
				CreatedBy int64
			}{menuID, createdBy})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("read role menu rows: %v", err)
	}
	return out
}

// softDeleteRoleRow 直接置位指定角色的 deleted_at（构造软删事实的辅助，
// 与走仓储路径的用例区分开）。
func setRoleStatus(t *testing.T, d *Data, auth biz.Identity, id int64, status int16) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`UPDATE public.sys_role SET status = $1 WHERE id = $2`, status, id)
		return err
	})
	if err != nil {
		t.Fatalf("set role %d status %d: %v", id, status, err)
	}
}

// setMenuStatus 置位指定菜单的 status（构造失活事实的辅助）：sys_menu 已
// 对运行角色 REVOKE 直写（D2），经菜单域的 SECURITY DEFINER 受控写函数
// fn_menu_update 以固定口径停用。
func setMenuStatus(t *testing.T, d *Data, auth biz.Identity, id int64, status int16) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		var name, icon, routePath, component, expr, remark string
		var sortOrder int32
		if err := s.Tx().QueryRow(context.Background(),
			`SELECT "name", COALESCE(icon,''), COALESCE(route_path,''), COALESCE(component,''),
			        COALESCE(permission_expr,''), sort_order, COALESCE(remark,'')
			 FROM public.sys_menu WHERE id = $1`, id).
			Scan(&name, &icon, &routePath, &component, &expr, &sortOrder, &remark); err != nil {
			return err
		}
		_, err := s.Tx().Exec(context.Background(),
			`SELECT public.fn_menu_update($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
			        NULLIF($7,''), $8, NULLIF($9,''), $10)`,
			id, status, name, icon, routePath, component, expr, sortOrder, remark, auth.UserID)
		return err
	})
	if err != nil {
		t.Fatalf("set menu %d status %d: %v", id, status, err)
	}
}

// softDeleteMenuRow 置位指定菜单的 deleted_at（构造软删事实的辅助）：
// 经菜单域受控写函数 fn_menu_delete 软删（菜单 3237 为叶子按钮，无子菜单）。
func softDeleteMenuRow(t *testing.T, d *Data, auth biz.Identity, id int64) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`SELECT public.fn_menu_delete($1, $2)`, id, auth.UserID)
		return err
	})
	if err != nil {
		t.Fatalf("soft delete menu %d: %v", id, err)
	}
}

// userHasPermission 经 expose.sql 的 fn_has_permission 判定功能权限链
// （角色 → 菜单 → 权限码），供软删角色后的判定走向断言。
func userHasPermission(t *testing.T, d *Data, auth biz.Identity, userID int64, code string) bool {
	t.Helper()
	var ok bool
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		got, err := s.Q().HasPermission(context.Background(), gen.HasPermissionParams{Operator: userID, Code: code})
		if err != nil {
			return err
		}
		ok = got
		return nil
	})
	if err != nil {
		t.Fatalf("has permission: %v", err)
	}
	return ok
}

func TestRoleRepo_create_writes_fields_and_defaults(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantEmpty)
	remark := "ops role"

	// 用例层已把缺省值落定（status=1、role_type=2、data_scope=1），仓储原样落库。
	created := mustCreateRole(t, d, auth, &biz.Role{Name: "运维角色", Code: "ops", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf, Remark: &remark})
	got, err := NewRoleRepo(d).GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get created role: %v", err)
	}
	if got.Name != "运维角色" || got.Code != "ops" || got.Status != biz.RoleStatusEnabled {
		t.Fatalf("row = (%s, %s, %d), want (运维角色, ops, enabled)", got.Name, got.Code, got.Status)
	}
	if got.RoleType != biz.RoleTypeCustom || got.DataScope != biz.RoleDataScopeSelf || got.ScopeDeptID != nil {
		t.Fatalf("defaults = (type %d, scope %d, scopeDept %+v), want (custom, self, nil)", got.RoleType, got.DataScope, got.ScopeDeptID)
	}
	if got.Remark == nil || *got.Remark != "ops role" {
		t.Fatalf("remark = %+v, want ops role", got.Remark)
	}
	if got.CreatedBy != auth.UserID || got.UpdatedBy != auth.UserID || got.DeletedAt != nil {
		t.Fatalf("audit = (by %d/%d, deleted %+v), want (%d/%d, nil)", got.CreatedBy, got.UpdatedBy, got.DeletedAt, auth.UserID, auth.UserID)
	}
}

func TestRoleRepo_create_duplicate_code_maps_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 仓储直建不经用例缺省，data_scope/role_type 显式携带合法值（列 CHECK 限定）。
	_, err := NewRoleRepo(d).Create(context.Background(), roleAuth(roleTenantSeeded), &biz.Role{Name: "新角色", Code: "dept_admin", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if !errors.Is(err, biz.ErrRoleAlreadyExists) {
		t.Fatalf("err = %v, want ErrRoleAlreadyExists", err)
	}
}

func TestRoleRepo_create_duplicate_name_maps_already_exists(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).Create(context.Background(), roleAuth(roleTenantSeeded), &biz.Role{Name: "部门管理员", Code: "brand_new", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if !errors.Is(err, biz.ErrRoleAlreadyExists) {
		t.Fatalf("err = %v, want ErrRoleAlreadyExists", err)
	}
}

func TestRoleRepo_get_by_id_hides_cross_tenant_row(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	// 角色 101 属 t-super-01（seed 包生产默认）；t-test-02 的身份经
	// RLS 不可见，等同不存在。
	other := roleAuth(roleTenantEmpty)

	_, err := NewRoleRepo(d).GetByID(context.Background(), other, 101)
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_get_by_id_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).GetByID(context.Background(), roleAuth(roleTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_list_filters_by_code_prefix_with_total(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 种子演示角色 106-109 的编码均以 demo_scope 开头。
	list, err := NewRoleRepo(d).List(context.Background(), roleAuth(roleTenantSeeded),
		&dynq.Query{Where: []dynq.Condition{{Field: "code", Op: "prefix", Value: "demo_scope"}}})
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(list.Items) != 4 || list.Total == nil || *list.Total != 4 {
		t.Fatalf("items/total = (%d, %+v), want (4, 4)", len(list.Items), list.Total)
	}
}

func TestRoleRepo_list_hides_cross_tenant_rows(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// t-test-02 仅角色 105；t-test-01 的 102-110 与 t-super-01 的 101
	// 经 RLS 不出现。
	list, err := NewRoleRepo(d).List(context.Background(), roleAuth(roleTenantEmpty), &dynq.Query{})
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != 105 {
		t.Fatalf("items = %+v, want only role 105", list.Items)
	}
}

func TestRoleRepo_list_rejects_unknown_field(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).List(context.Background(), roleAuth(roleTenantSeeded),
		&dynq.Query{Where: []dynq.Condition{{Field: "ghost", Op: "eq", Value: 1}}})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
}

func TestRoleRepo_update_changes_name_status_remark(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantEmpty)
	repo := NewRoleRepo(d)

	created := mustCreateRole(t, d, auth, &biz.Role{Name: "before", Code: "r", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	remark := "after-remark"
	created.Name = "after"
	created.Status = biz.RoleStatusDisabled
	created.Remark = &remark
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("update role: %v", err)
	}
	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Name != "after" || got.Status != biz.RoleStatusDisabled || got.Remark == nil || *got.Remark != "after-remark" {
		t.Fatalf("row = (%s, %d, %+v), want (after, disabled, after-remark)", got.Name, got.Status, got.Remark)
	}
}

func TestRoleRepo_update_keeps_code_immutable(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantEmpty)
	repo := NewRoleRepo(d)

	created := mustCreateRole(t, d, auth, &biz.Role{Name: "r", Code: "keep-code", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	created.Code = "hacked"
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("update role: %v", err)
	}
	got, err := repo.GetByID(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Code != "keep-code" {
		t.Fatalf("code = %q, want keep-code (update must not touch identity column)", got.Code)
	}
}

func TestRoleRepo_update_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).Update(context.Background(), roleAuth(roleTenantSeeded),
		&biz.Role{ID: 424242, Name: "ghost", Code: "ghost", Status: biz.RoleStatusEnabled})
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_delete_missing_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	err := NewRoleRepo(d).SoftDelete(context.Background(), roleAuth(roleTenantSeeded), 424242)
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_delete_preset_role_rejected_without_changes(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	// 角色 101 为系统预置（role_type=1，seed 包生产默认数据）：删除被
	// 拒绝且角色仍可见。
	auth := roleAuth("t-super-01")
	repo := NewRoleRepo(d)
	err := repo.SoftDelete(context.Background(), auth, 101)
	if !errors.Is(err, biz.ErrRolePresetUndeletable) {
		t.Fatalf("err = %v, want ErrRolePresetUndeletable", err)
	}
	if _, err := repo.GetByID(context.Background(), auth, 101); err != nil {
		t.Fatalf("preset role must stay visible after rejected delete: %v", err)
	}
}

func TestRoleRepo_delete_soft_deletes_and_code_reusable(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantEmpty)
	repo := NewRoleRepo(d)

	created := mustCreateRole(t, d, auth, &biz.Role{Name: "临时角色", Code: "tmp", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if err := repo.SoftDelete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if _, err := repo.GetByID(context.Background(), auth, created.ID); !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("get after delete = %v, want ErrRoleNotFound", err)
	}
	// 唯一约束限定未删除行间：软删后 code/name 可复用。
	recreated := mustCreateRole(t, d, auth, &biz.Role{Name: "临时角色", Code: "tmp", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if recreated.ID == 0 || recreated.ID == created.ID {
		t.Fatalf("recreate with freed code = %+v, want new row", recreated)
	}
}

func TestRoleRepo_delete_soft_deleted_role_keeps_menu_residue_and_breaks_permission_chain(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 用户 2 持角色 103（映射菜单 3201/3202/3203/3236/3237）：删角色前持有
	// sys:user:query；删后映射行物理残留，但功能权限链断开。
	if !userHasPermission(t, d, auth, 2, "sys:user:query") {
		t.Fatal("user 2 must hold sys:user:query before role delete")
	}
	if err := repo.SoftDelete(context.Background(), auth, 103); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	rows := roleMenuRowsWithCreator(t, d, auth, 103)
	if len(rows) != 5 {
		t.Fatalf("mapping rows after delete = %d, want 5 (physical residue)", len(rows))
	}
	if userHasPermission(t, d, auth, 2, "sys:user:query") {
		t.Fatal("soft deleted role must not grant permission anymore")
	}
}

func TestRoleRepo_get_data_scope_reads_seed_config(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 角色 106（data_scope=4 指定人）种子明细为用户 3/4；无分发部门。
	got, err := repo.GetDataScope(context.Background(), auth, 106)
	if err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if got.RoleID != 106 || got.DataScope != biz.RoleDataScopeUsers {
		t.Fatalf("scalar = (role %d, ds %d), want (106, users)", got.RoleID, got.DataScope)
	}
	if got.ScopeDeptID != nil {
		t.Fatalf("scope dept = %+v, want nil", got.ScopeDeptID)
	}
	want := []biz.RoleIDScopeEntry{{Type: biz.RoleIDScopeTypeUser, DataID: 3}, {Type: biz.RoleIDScopeTypeUser, DataID: 4}}
	if len(got.IDScopes) != 2 || got.IDScopes[0] != want[0] || got.IDScopes[1] != want[1] {
		t.Fatalf("id scopes = %+v, want %v", got.IDScopes, want)
	}
	if len(got.AllowedDeptIDs) != 0 {
		t.Fatalf("allowed depts = %v, want empty", got.AllowedDeptIDs)
	}

	// 角色 102（本部门及子部门）种子带 scope_dept_id=2 与分发部门 {2,5}。
	got, err = repo.GetDataScope(context.Background(), auth, 102)
	if err != nil {
		t.Fatalf("get data scope 102: %v", err)
	}
	if got.DataScope != biz.RoleDataScopeDeptSubtree || got.ScopeDeptID == nil || *got.ScopeDeptID != 2 {
		t.Fatalf("scalar = (ds %d, scopeDept %+v), want (subtree, 2)", got.DataScope, got.ScopeDeptID)
	}
	if !slices.Equal(got.AllowedDeptIDs, []int64{2, 5}) {
		t.Fatalf("allowed depts = %v, want [2 5]", got.AllowedDeptIDs)
	}
}

func TestRoleRepo_get_data_scope_missing_and_cross_tenant_map_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	if _, err := NewRoleRepo(d).GetDataScope(context.Background(), roleAuth(roleTenantSeeded), 424242); !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("missing role err = %v, want ErrRoleNotFound", err)
	}
	if _, err := NewRoleRepo(d).GetDataScope(context.Background(), roleAuth(roleTenantEmpty), 106); !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("cross-tenant err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_set_data_scope_replaces_both_tables_consistently(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 引用的用户/部门/用户组都在种子租户内，角色用 newID 发号新建、不与
	// 种子主键冲突。
	created := mustCreateRole(t, d, auth, &biz.Role{Name: "cfg", Code: "cfg", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})

	// 第一组配置：指定人（用户 2/3）+ 指定部门维度明细 + 分发部门 {2,7}。
	// 仓储回显写入的入参（用例负责归一化），读取面按 SQL 排序返回。
	first := &biz.RoleDataScope{
		RoleID:    created.ID,
		DataScope: biz.RoleDataScopeUsers,
		IDScopes: []biz.RoleIDScopeEntry{
			{Type: biz.RoleIDScopeTypeDept, DataID: 5},
			{Type: biz.RoleIDScopeTypeUser, DataID: 2},
			{Type: biz.RoleIDScopeTypeUser, DataID: 3},
		},
		AllowedDeptIDs: []int64{7, 2},
	}
	got := mustSetDataScope(t, d, auth, first)
	if len(got.IDScopes) != 3 || got.IDScopes[0] != first.IDScopes[0] || got.IDScopes[2] != first.IDScopes[2] {
		t.Fatalf("reply id scopes = %+v, want echo of written set", got.IDScopes)
	}
	read, err := repo.GetDataScope(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if read.DataScope != biz.RoleDataScopeUsers || !slices.Equal(read.AllowedDeptIDs, []int64{2, 7}) {
		t.Fatalf("read = (ds %d, allowed %v), want (users, [2 7])", read.DataScope, read.AllowedDeptIDs)
	}
	if len(read.IDScopes) != 3 || read.IDScopes[2].Type != biz.RoleIDScopeTypeDept || read.IDScopes[2].DataID != 5 {
		t.Fatalf("read id scopes = %+v, want dept 5 last", read.IDScopes)
	}

	// 第二组配置（子集 + 换档）：旧明细/旧分发部门整体被替换，不残留。
	second := &biz.RoleDataScope{
		RoleID:         created.ID,
		DataScope:      biz.RoleDataScopeGroups,
		IDScopes:       []biz.RoleIDScopeEntry{{Type: biz.RoleIDScopeTypeGroup, DataID: 1}},
		AllowedDeptIDs: []int64{3},
	}
	mustSetDataScope(t, d, auth, second)
	read, err = repo.GetDataScope(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("get data scope after replace: %v", err)
	}
	if read.DataScope != biz.RoleDataScopeGroups {
		t.Fatalf("data scope = %d, want groups", read.DataScope)
	}
	if len(read.IDScopes) != 1 || read.IDScopes[0] != (biz.RoleIDScopeEntry{Type: biz.RoleIDScopeTypeGroup, DataID: 1}) {
		t.Fatalf("id scopes = %+v, want [(group, 1)]", read.IDScopes)
	}
	if !slices.Equal(read.AllowedDeptIDs, []int64{3}) {
		t.Fatalf("allowed depts = %v, want [3]", read.AllowedDeptIDs)
	}
	// 物理行校验：不存在软删/残留行。
	if rows := roleIDScopeRows(t, d, auth, created.ID); len(rows) != 1 {
		t.Fatalf("physical id scope rows = %+v, want single active row", rows)
	}
	if rows := roleAllowedDeptRows(t, d, auth, created.ID); len(rows) != 1 || rows[0] != 3 {
		t.Fatalf("physical allowed dept rows = %v, want [3]", rows)
	}
}

func TestRoleRepo_set_data_scope_preserves_retained_rows_audit(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	// 角色 102 种子分发部门 {2,5}（created_by=1）：保留 2、移除 5、新增 4。
	// 保留行的 created_by 不变（差量同步不重写既有授权），新行记新配置人。
	scopeDept := int64(2)
	mustSetDataScope(t, d, auth, &biz.RoleDataScope{
		RoleID:         102,
		DataScope:      biz.RoleDataScopeDeptSubtree,
		ScopeDeptID:    &scopeDept,
		AllowedDeptIDs: []int64{2, 4},
	})
	rows := roleAllowedDeptRows(t, d, auth, 102)
	if !slices.Equal(rows, []int64{2, 4}) {
		t.Fatalf("allowed dept rows = %v, want [2 4]", rows)
	}
	var createdBy2, createdBy4 int64
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT created_by FROM public.sys_role_allowed_dept WHERE role_id = 102 AND dept_id = 2`).Scan(&createdBy2)
	})
	if err != nil {
		t.Fatalf("read retained row creator: %v", err)
	}
	err = d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT created_by FROM public.sys_role_allowed_dept WHERE role_id = 102 AND dept_id = 4`).Scan(&createdBy4)
	})
	if err != nil {
		t.Fatalf("read added row creator: %v", err)
	}
	if createdBy2 != 1 || createdBy4 != auth.UserID {
		t.Fatalf("creators = (retained %d, added %d), want (1, %d)", createdBy2, createdBy4, auth.UserID)
	}
}

func TestRoleRepo_set_data_scope_unknown_user_ref_rejects_and_writes_nothing(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 用户 424242 不存在（跨租户等同不存在）：整组拒绝，配置保持种子原样。
	_, err := repo.SetDataScope(context.Background(), auth, &biz.RoleDataScope{
		RoleID:    106,
		DataScope: biz.RoleDataScopeUsers,
		IDScopes:  []biz.RoleIDScopeEntry{{Type: biz.RoleIDScopeTypeUser, DataID: 2}, {Type: biz.RoleIDScopeTypeUser, DataID: 424242}},
	})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	got, err := repo.GetDataScope(context.Background(), auth, 106)
	if err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if len(got.IDScopes) != 2 {
		t.Fatalf("id scopes after rejection = %+v, want seed unchanged", got.IDScopes)
	}
}

func TestRoleRepo_set_data_scope_unknown_allowed_dept_rejects(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	_, err := NewRoleRepo(d).SetDataScope(context.Background(), auth, &biz.RoleDataScope{
		RoleID:         102,
		DataScope:      biz.RoleDataScopeDeptSubtree,
		AllowedDeptIDs: []int64{2, 424242},
	})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	if rows := roleAllowedDeptRows(t, d, auth, 102); !slices.Equal(rows, []int64{2, 5}) {
		t.Fatalf("allowed dept rows after rejection = %v, want seed [2 5]", rows)
	}
}

func TestRoleRepo_set_data_scope_unknown_scope_dept_rejects(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	ghost := int64(424242)

	_, err := NewRoleRepo(d).SetDataScope(context.Background(), auth, &biz.RoleDataScope{
		RoleID:      107,
		DataScope:   biz.RoleDataScopeDepts,
		ScopeDeptID: &ghost,
	})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	got, err := NewRoleRepo(d).GetDataScope(context.Background(), auth, 107)
	if err != nil {
		t.Fatalf("get data scope: %v", err)
	}
	if got.ScopeDeptID != nil {
		t.Fatalf("scope dept after rejection = %+v, want nil (seed unchanged)", got.ScopeDeptID)
	}
}

func TestRoleRepo_set_data_scope_cross_tenant_role_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).SetDataScope(context.Background(), roleAuth(roleTenantEmpty),
		&biz.RoleDataScope{RoleID: 106, DataScope: biz.RoleDataScopeSelf})
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_list_menus_returns_seed_set_sorted(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	// 角色 103 种子授权 {3201,3202,3203,3236,3237}（32xx 为 t-test-01 的
	// 菜单副本）。
	menus, err := NewRoleRepo(d).ListMenus(context.Background(), roleAuth(roleTenantSeeded), 103)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if !slices.Equal(menus, []int64{3201, 3202, 3203, 3236, 3237}) {
		t.Fatalf("menus = %v, want [3201 3202 3203 3236 3237]", menus)
	}
}

func TestRoleRepo_list_menus_missing_and_cross_tenant_map_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	if _, err := NewRoleRepo(d).ListMenus(context.Background(), roleAuth(roleTenantSeeded), 424242); !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("missing role err = %v, want ErrRoleNotFound", err)
	}
	if _, err := NewRoleRepo(d).ListMenus(context.Background(), roleAuth(roleTenantEmpty), 103); !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("cross-tenant err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_set_menus_replaces_grants_and_preserves_retained_creator(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 种子 {3201,3202,3203,3236,3237} → 收缩为 {3202,3203}（3201/3236/3237
	// 物理移除）。
	menus, err := repo.SetMenus(context.Background(), auth, 103, []int64{3203, 3202})
	if err != nil {
		t.Fatalf("set menus: %v", err)
	}
	if !slices.Equal(menus, []int64{3202, 3203}) {
		t.Fatalf("menus after set = %v, want [3202 3203]", menus)
	}
	// 再次设置并追加 3204：既有授权保留原授权人（种子 created_by=1），
	// 新增授权记当前操作者。
	menus, err = repo.SetMenus(context.Background(), auth, 103, []int64{3203, 3202, 3204})
	if err != nil {
		t.Fatalf("set menus again: %v", err)
	}
	if !slices.Equal(menus, []int64{3202, 3203, 3204}) {
		t.Fatalf("menus after second set = %v, want [3202 3203 3204]", menus)
	}
	rows := roleMenuRowsWithCreator(t, d, auth, 103)
	if len(rows) != 3 {
		t.Fatalf("mapping rows = %d, want 3", len(rows))
	}
	for _, row := range rows {
		wantCreator := int64(1)
		if row.MenuID == 3204 {
			wantCreator = auth.UserID // 新增授权记当前操作者
		}
		if row.CreatedBy != wantCreator {
			t.Fatalf("menu %d creator = %d, want %d (retained rows keep original grantor)", row.MenuID, row.CreatedBy, wantCreator)
		}
	}
}

func TestRoleRepo_set_menus_idempotent_same_set_changes_nothing(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	before := roleMenuRowsWithCreator(t, d, auth, 103)
	menus, err := repo.SetMenus(context.Background(), auth, 103, []int64{3236, 3201, 3202, 3203, 3237})
	if err != nil {
		t.Fatalf("set menus: %v", err)
	}
	if !slices.Equal(menus, []int64{3201, 3202, 3203, 3236, 3237}) {
		t.Fatalf("menus = %v, want seed set unchanged", menus)
	}
	after := roleMenuRowsWithCreator(t, d, auth, 103)
	if len(after) != len(before) {
		t.Fatalf("mapping rows = %d, want %d (idempotent)", len(after), len(before))
	}
	for i := range after {
		if after[i] != before[i] {
			t.Fatalf("row %d = %+v, want %+v (retained untouched)", i, after[i], before[i])
		}
	}
}

func TestRoleRepo_set_menus_empty_clears_all_grants(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	menus, err := NewRoleRepo(d).SetMenus(context.Background(), auth, 103, nil)
	if err != nil {
		t.Fatalf("set menus empty: %v", err)
	}
	if len(menus) != 0 {
		t.Fatalf("menus = %v, want empty", menus)
	}
	if rows := roleMenuRowsWithCreator(t, d, auth, 103); len(rows) != 0 {
		t.Fatalf("mapping rows = %d, want 0 (physical delete)", len(rows))
	}
}

func TestRoleRepo_set_menus_unknown_menu_rejects_and_writes_nothing(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	_, err := NewRoleRepo(d).SetMenus(context.Background(), auth, 103, []int64{3201, 424242})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
	menus, err := NewRoleRepo(d).ListMenus(context.Background(), auth, 103)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if !slices.Equal(menus, []int64{3201, 3202, 3203, 3236, 3237}) {
		t.Fatalf("menus after rejection = %v, want seed unchanged", menus)
	}
}

func TestRoleRepo_set_menus_cross_tenant_menu_rejects(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	// 菜单 1201 属 t-test-02：RLS 之外等同不存在，存在性判定整批拒绝。
	_, err := NewRoleRepo(d).SetMenus(context.Background(), auth, 103, []int64{3201, 1201})
	if !errors.Is(err, biz.ErrRoleInvalidArgument) {
		t.Fatalf("err = %v, want ErrRoleInvalidArgument", err)
	}
}

func TestRoleRepo_set_menus_cross_tenant_role_maps_not_found(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)

	_, err := NewRoleRepo(d).SetMenus(context.Background(), roleAuth(roleTenantEmpty), 103, []int64{1201})
	if !errors.Is(err, biz.ErrRoleNotFound) {
		t.Fatalf("err = %v, want ErrRoleNotFound", err)
	}
}

func TestRoleRepo_list_menus_hides_inactive_menu_grant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	// 角色 103 种子授权 {3201,3202,3203,3236,3237}：菜单 3237 停用后不出现在
	// 回显集合（有效授权集口径），其余四项不受影响。
	setMenuStatus(t, d, auth, 3237, 0)
	menus, err := NewRoleRepo(d).ListMenus(context.Background(), auth, 103)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if !slices.Equal(menus, []int64{3201, 3202, 3203, 3236}) {
		t.Fatalf("menus = %v, want [3201 3202 3203 3236] (disabled grant hidden)", menus)
	}
}

func TestRoleRepo_set_menus_roundtrips_active_echo_and_drops_inactive_grant(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 菜单 3237 软删：回显集合排除其残留授权；把回显集合原样 Set 回去不被
	// 存在性判定拒绝（读-改-写回路），且失活授权随差量物理清除。
	softDeleteMenuRow(t, d, auth, 3237)
	echo, err := repo.ListMenus(context.Background(), auth, 103)
	if err != nil {
		t.Fatalf("list menus: %v", err)
	}
	if !slices.Equal(echo, []int64{3201, 3202, 3203, 3236}) {
		t.Fatalf("echo = %v, want [3201 3202 3203 3236]", echo)
	}
	menus, err := repo.SetMenus(context.Background(), auth, 103, echo)
	if err != nil {
		t.Fatalf("set menus with active echo: %v", err)
	}
	if !slices.Equal(menus, []int64{3201, 3202, 3203, 3236}) {
		t.Fatalf("menus after set = %v, want [3201 3202 3203 3236]", menus)
	}
	if rows := roleMenuRowsWithCreator(t, d, auth, 103); len(rows) != 4 {
		t.Fatalf("mapping rows = %d, want 4 (inactive grant physically dropped)", len(rows))
	}
}

func TestRoleRepo_can_assign_role_tracks_role_lifecycle(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)

	// 新建角色（无分发部门、无层级限定）：操作者 11（testadmin，全租户）
	// 可分配给种子租户内的用户 2。
	created := mustCreateRole(t, d, auth, &biz.Role{Name: "cycle", Code: "cycle", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if !mustCanAssignRole(t, d, auth, 11, created.ID, 2) {
		t.Fatal("new role must be assignable by tenant-wide operator")
	}
	// 停用后判定为拒绝（判定函数过滤 status=1）。
	created.Status = biz.RoleStatusDisabled
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("disable role: %v", err)
	}
	if mustCanAssignRole(t, d, auth, 11, created.ID, 2) {
		t.Fatal("disabled role must not be assignable")
	}
	// 恢复启用后判定恢复。
	created.Status = biz.RoleStatusEnabled
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("enable role: %v", err)
	}
	if !mustCanAssignRole(t, d, auth, 11, created.ID, 2) {
		t.Fatal("re-enabled role must be assignable again")
	}
	// 软删后判定为拒绝（判定函数过滤 deleted_at IS NULL）。
	if err := repo.SoftDelete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if mustCanAssignRole(t, d, auth, 11, created.ID, 2) {
		t.Fatal("soft deleted role must not be assignable")
	}
}

func TestRoleRepo_can_assign_role_allowed_dept_mapping_changes_judgment(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	// 种子基线：角色 102 scope_dept_id=2、分发部门 {2,5}；目标部门须同时
	// 落入 M 与 subtree(S)，用户 10 只命中 M → 拒绝。
	if !mustCanAssignRole(t, d, auth, 11, 102, 2) {
		t.Fatal("target dept in both mapping and scope subtree must be assignable")
	}
	if mustCanAssignRole(t, d, auth, 11, 102, 10) {
		t.Fatal("target outside scope subtree must be rejected even if dept hits mapping")
	}
	// 操作者 7（部门 2，持有分配权限码）：可见部门 {2,7,8} 与映射交集
	// 非空（A 成立），同样受目标范围交集约束。
	if !mustCanAssignRole(t, d, auth, 7, 102, 2) {
		t.Fatal("operator 7 with mapping hit must stay allowed for in-range target")
	}
	// 改配分发部门为 {7}：目标 6（部门 7）∈ M ∩ subtree(2) → 可分配；
	// 目标 2（部门 2）不在收窄后的映射内 → 拒绝。（SetDataScope 对
	// scope_dept_id 整字段覆盖，须显式保留种子层级限定 2）
	scopeDept := int64(2)
	mustSetDataScope(t, d, auth, &biz.RoleDataScope{
		RoleID:         102,
		DataScope:      biz.RoleDataScopeDeptSubtree,
		ScopeDeptID:    &scopeDept,
		AllowedDeptIDs: []int64{7},
	})
	if !mustCanAssignRole(t, d, auth, 7, 102, 6) {
		t.Fatal("target dept 7 in narrowed mapping and subtree must be assignable")
	}
	if mustCanAssignRole(t, d, auth, 7, 102, 2) {
		t.Fatal("target dept 2 outside narrowed mapping must be rejected")
	}
	// 全租户操作者 11 同样受目标范围交集约束。
	if !mustCanAssignRole(t, d, auth, 11, 102, 6) {
		t.Fatal("tenant-wide operator is bounded by the same target range")
	}
	// 恢复种子映射 {2,5}：判定恢复基线（scope_dept_id 显式保留 2）。
	mustSetDataScope(t, d, auth, &biz.RoleDataScope{
		RoleID:         102,
		DataScope:      biz.RoleDataScopeDeptSubtree,
		ScopeDeptID:    &scopeDept,
		AllowedDeptIDs: []int64{2, 5},
	})
	if !mustCanAssignRole(t, d, auth, 11, 102, 2) {
		t.Fatal("restored mapping must restore baseline judgment")
	}
}

func TestRoleRepo_can_assign_role_scope_dept_subtree_changes_judgment(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := roleAuth(roleTenantSeeded)

	// 新建角色未配层级限定：任何有效目标都可分配（操作者 11 全租户可见）。
	created := mustCreateRole(t, d, auth, &biz.Role{Name: "subtree", Code: "subtree", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if !mustCanAssignRole(t, d, auth, 11, created.ID, 3) {
		t.Fatal("role without scope dept must be assignable to user 3")
	}
	// 配 scope_dept_id=2（技术部子树）：用户 3（产品部）落到子树外被判拒绝，
	// 用户 2（技术部本部）仍在子树内可分配。
	scopeDept := int64(2)
	mustSetDataScope(t, d, auth, &biz.RoleDataScope{
		RoleID:      created.ID,
		DataScope:   biz.RoleDataScopeSelf,
		ScopeDeptID: &scopeDept,
	})
	if mustCanAssignRole(t, d, auth, 11, created.ID, 3) {
		t.Fatal("target outside scope dept subtree must be rejected")
	}
	if !mustCanAssignRole(t, d, auth, 11, created.ID, 2) {
		t.Fatal("target inside scope dept subtree must stay allowed")
	}
	// 清空层级限定（NULL=全租户可分配）：判定恢复。
	mustSetDataScope(t, d, auth, &biz.RoleDataScope{RoleID: created.ID, DataScope: biz.RoleDataScopeSelf})
	if !mustCanAssignRole(t, d, auth, 11, created.ID, 3) {
		t.Fatal("cleared scope dept must restore assignability")
	}
}
