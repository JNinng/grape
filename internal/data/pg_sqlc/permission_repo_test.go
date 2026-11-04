package pg_sqlc

import (
	"context"
	"reflect"
	"testing"

	"grape/internal/biz"
)

// 功能权限码仓储集成测试：以真实 PostgreSQL 验证 fn_has_permission 判定
// 链路与 GetPermissionCodes 枚举（入口层判定中间件与权限码查询端点的
// 存储底座）。种子事实（sqlc/seed.sql + sqlc/99_perm_seed.sql）：
//   - 用户 1（admin，t-super-01）持角色 101+102，角色 101 授予全部菜单；
//   - 用户 3（lisi，t-super-01）持角色 103，菜单 201/202/203/236/237；
//   - 用户 2（zhangsan，t-super-01）持角色 103+108，角色 108 无菜单授权；
//   - 用户 5（zhaoliu，t-demo-01）持角色 105，菜单为 12xx 演示副本。
// 各用例经 setupTestDB 重建数据库实现隔离；sys_role_menu 的种子外授权
// 变更用直接 SQL 写入（grape_u 对该表有 DML 授权，RoleService.SetMenus
// 的仓储原语同型），trg_bump_perm_version_tenant 随之 bump 租户级版本。

// permAuth 返回指定租户与用户的操作者身份。
func permAuth(tenant string, userID int64) biz.Identity {
	return biz.Identity{TenantID: tenant, UserID: userID}
}

// mustHasPermission 执行功能权限码判定，失败即终止用例。
func mustHasPermission(t *testing.T, d *Data, auth biz.Identity, code string) bool {
	t.Helper()
	ok, err := NewPermissionRepo(d).HasPermission(context.Background(), auth, code)
	if err != nil {
		t.Fatalf("has permission %s: %v", code, err)
	}
	return ok
}

// mustPermissionCodes 枚举权限码集合，失败即终止用例。
func mustPermissionCodes(t *testing.T, d *Data, auth biz.Identity) []string {
	t.Helper()
	codes, err := NewPermissionRepo(d).ListCodes(context.Background(), auth)
	if err != nil {
		t.Fatalf("list permission codes: %v", err)
	}
	return codes
}

// permTenantVersion 读取租户级范围版本（user_id=0 行）。
func permTenantVersion(t *testing.T, d *Data, auth biz.Identity) int64 {
	t.Helper()
	var version int64
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT version FROM public.sys_perm_version WHERE user_id = 0`).Scan(&version)
	})
	if err != nil {
		t.Fatalf("read tenant perm version: %v", err)
	}
	return version
}

// grantRoleMenu / revokeRoleMenu 以运行角色直写 sys_role_menu（增删一条
// 角色-菜单授权），失败即终止用例。
func grantRoleMenu(t *testing.T, d *Data, auth biz.Identity, roleID, menuID int64) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`INSERT INTO public.sys_role_menu (role_id, menu_id, tenant_id, created_by) VALUES ($1, $2, $3, $4)`,
			roleID, menuID, auth.TenantID, auth.UserID)
		return err
	})
	if err != nil {
		t.Fatalf("grant role menu (%d, %d): %v", roleID, menuID, err)
	}
}

func revokeRoleMenu(t *testing.T, d *Data, auth biz.Identity, roleID, menuID int64) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`DELETE FROM public.sys_role_menu WHERE role_id = $1 AND menu_id = $2`, roleID, menuID)
		return err
	})
	if err != nil {
		t.Fatalf("revoke role menu (%d, %d): %v", roleID, menuID, err)
	}
}

// setUserStatus 置位/恢复用户状态（0 禁用、1 启用），失败即终止用例。
func setUserStatus(t *testing.T, d *Data, auth biz.Identity, userID int64, status int) {
	t.Helper()
	err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(),
			`UPDATE public.sys_user SET status = $1 WHERE id = $2`, status, userID)
		return err
	})
	if err != nil {
		t.Fatalf("set user %d status %d: %v", userID, status, err)
	}
}

func TestPermissionRepo_held_code_judged_granted(t *testing.T) {
	d := setupTestDB(t)

	// 用户 1 持角色 101（全部菜单）+102：用户管理的查询与创建码均命中。
	if !mustHasPermission(t, d, permAuth("t-super-01", 1), "sys:user:create") {
		t.Fatal("admin must hold sys:user:create")
	}
	if !mustHasPermission(t, d, permAuth("t-super-01", 1), "sys:user:query") {
		t.Fatal("admin must hold sys:user:query")
	}
	// 超面写码（M3-2 受控写面）与转移归属新码同样命中。
	if !mustHasPermission(t, d, permAuth("t-super-01", 1), "sys:menu:create") {
		t.Fatal("admin must hold sys:menu:create")
	}
	if !mustHasPermission(t, d, permAuth("t-super-01", 1), "sys:user:transfer-owner") {
		t.Fatal("admin must hold sys:user:transfer-owner")
	}
}

func TestPermissionRepo_unheld_code_judged_denied(t *testing.T) {
	d := setupTestDB(t)

	// 用户 3 持角色 103（仅查询/部门查询码）：创建码不命中、查询码命中。
	if mustHasPermission(t, d, permAuth("t-super-01", 3), "sys:user:create") {
		t.Fatal("user 3 must not hold sys:user:create")
	}
	if !mustHasPermission(t, d, permAuth("t-super-01", 3), "sys:user:query") {
		t.Fatal("user 3 must hold sys:user:query")
	}
	// 未知权限码等同不持有。
	if mustHasPermission(t, d, permAuth("t-super-01", 1), "sys:nonexistent:code") {
		t.Fatal("unknown code must be denied")
	}
}

func TestPermissionRepo_cross_tenant_grant_invisible_to_other_tenant(t *testing.T) {
	d := setupTestDB(t)

	// sys:menu:query 只存在于 t-super-01 的菜单树（214 子树）：t-demo-01
	// 的用户 5 即便码值存在也判 false（RLS 租户联结不可见跨租户授权）。
	if mustHasPermission(t, d, permAuth("t-demo-01", 5), "sys:menu:query") {
		t.Fatal("cross-tenant code must be denied for t-demo-01 user")
	}
	// 同一码在授予方租户内命中（t-demo-01 自身的 sys:user:create 副本 1204）。
	if !mustHasPermission(t, d, permAuth("t-demo-01", 5), "sys:user:create") {
		t.Fatal("tenant-local code must be granted within its tenant")
	}
}

func TestPermissionRepo_disabled_user_holds_nothing(t *testing.T) {
	d := setupTestDB(t)
	auth := permAuth("t-super-01", 2)

	if !mustHasPermission(t, d, auth, "sys:user:query") {
		t.Fatal("enabled user must hold seeded code")
	}
	setUserStatus(t, d, auth, 2, 0)
	if mustHasPermission(t, d, auth, "sys:user:query") {
		t.Fatal("disabled user must hold nothing")
	}
	if codes := mustPermissionCodes(t, d, auth); len(codes) != 0 {
		t.Fatalf("disabled user codes = %v, want empty", codes)
	}
	setUserStatus(t, d, auth, 2, 1)
	if !mustHasPermission(t, d, auth, "sys:user:query") {
		t.Fatal("re-enabled user must hold seeded code again")
	}
}

func TestPermissionRepo_codes_enumerated_deduped_and_sorted(t *testing.T) {
	d := setupTestDB(t)

	// 用户 3 仅持角色 103：码集合 = 其菜单授权的权限码并集（去重升序）。
	want := []string{"sys:dept", "sys:dept:query", "sys:manage", "sys:user", "sys:user:query"}
	got := mustPermissionCodes(t, d, permAuth("t-super-01", 3))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}

	// 用户 2 持角色 103+108（多角色并集），108 无菜单授权：集合与单角色
	// 用户 3 完全一致（并集不引入多余项）。
	got2 := mustPermissionCodes(t, d, permAuth("t-super-01", 2))
	if !reflect.DeepEqual(got2, want) {
		t.Fatalf("codes = %v, want %v", got2, want)
	}
}

func TestPermissionRepo_grant_change_flips_verdict_and_bumps_version(t *testing.T) {
	d := setupTestDB(t)
	auth := permAuth("t-super-01", 2)

	if mustHasPermission(t, d, auth, "sys:user:create") {
		t.Fatal("user 2 must not hold sys:user:create before grant")
	}
	before := permTenantVersion(t, d, auth)

	// 授予：角色 103 挂上菜单 204（sys:user:create）→ 判定立即翻转，
	// trg_bump_perm_version_tenant bump 租户级版本（外部缓存失效信号）。
	grantRoleMenu(t, d, auth, 103, 204)
	if !mustHasPermission(t, d, auth, "sys:user:create") {
		t.Fatal("user 2 must hold sys:user:create after grant")
	}
	after := permTenantVersion(t, d, auth)
	if after <= before {
		t.Fatalf("tenant version after grant = %d, want > %d", after, before)
	}

	// 撤销：物理删除映射行 → 判定回落，版本继续递增。
	revokeRoleMenu(t, d, auth, 103, 204)
	if mustHasPermission(t, d, auth, "sys:user:create") {
		t.Fatal("user 2 must not hold sys:user:create after revoke")
	}
	if v := permTenantVersion(t, d, auth); v <= after {
		t.Fatalf("tenant version after revoke = %d, want > %d", v, after)
	}
}
