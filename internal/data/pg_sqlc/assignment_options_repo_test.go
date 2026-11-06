package pg_sqlc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"grape/internal/biz"
)

// 用户分配选择器与角色整组替换的存储边界集成测试：以真实 PostgreSQL
// 验证 fn_can_assign_role（可见性 + 目标范围 + 分配权限码）的候选过滤、
// locked 角色保留、版本冲突、自锁保护、fn_can_revoke_role 撤销与归属
// 候选的 fn_accessible_* 三态。种子事实见 sqlc/test_seed.sql：
// t-test-01 用户 2-11（11 为 data_scope=7 测试管理员，4 为角色 102 的
// 部门管理员），角色 102-110（105 属 t-test-02，scope_dept_id=2 且分发
// 部门 {2,5}），部门 1-8，用户组 1-2。

const (
	assignmentTenant = "t-test-01"
	// assignmentAdmin 是种子测试管理员（用户 11，角色 110 data_scope=7，
	// 持有 sys:user:assign-role）。
	assignmentAdmin = int64(11)
	// assignmentScoped 是部门管理员（用户 4，角色 102 data_scope=3，
	// 可见用户 {4,8}、可见部门 {4}，持有 sys:user:assign-role）。
	assignmentScoped = int64(4)
)

func assignmentAuth() biz.Identity {
	return biz.Identity{TenantID: assignmentTenant, UserID: assignmentAdmin}
}

func scopedAuth() biz.Identity {
	return biz.Identity{TenantID: assignmentTenant, UserID: assignmentScoped}
}

func newAssignmentRepo(d *Data) biz.AssignmentRepo {
	return NewAssignmentOptionRepo(d)
}

func optionIDs(set *biz.OptionSet) []int64 {
	ids := make([]int64, 0, len(set.Items))
	for _, o := range set.Items {
		ids = append(ids, o.Value)
	}
	slices.Sort(ids)
	return ids
}

func roleSetIDs(set *biz.UserRoleSet) []int64 {
	ids := make([]int64, 0, len(set.Items))
	for _, o := range set.Items {
		ids = append(ids, o.Value)
	}
	slices.Sort(ids)
	return ids
}

// execIdentityTx 在租户事务内执行一条原始语句（测试造数/断言用）。
func execIdentityTx(t *testing.T, d *Data, auth biz.Identity, sql string, args ...any) {
	t.Helper()
	if err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		_, err := s.Tx().Exec(context.Background(), sql, args...)
		return err
	}); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// insertMapping 造一条活跃用户-角色映射（主键 id 由调用方给定的固定
// 测试值，避免与仓储发号或其他造数冲突）。
func insertMapping(t *testing.T, d *Data, auth biz.Identity, id, userID, roleID int64) {
	t.Helper()
	execIdentityTx(t, d, auth, `INSERT INTO public.sys_user_role_mapping (id, user_id, role_id, tenant_id, created_by)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, id, userID, roleID, assignmentTenant, assignmentAdmin)
}

// scanMappings 读取用户的全部角色映射，升序返回。
func scanMappings(t *testing.T, d *Data, auth biz.Identity, userID int64) []int64 {
	t.Helper()
	var ids []int64
	if err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().ListUserRoleIDs(context.Background(), userID)
		if err != nil {
			return err
		}
		ids = rows
		return nil
	}); err != nil {
		t.Fatalf("scan mappings: %v", err)
	}
	return ids
}

// TestAssignmentRoleCandidates 覆盖可分配角色候选：新方案下候选 =
// fn_can_see_role（A AND G）∧ fn_can_assign_role（目标部门 ∈ M ∩
// subtree(S)）∧ 持有分配权限码 ∧ 未拥有。全租户操作者仍受目标范围约束；
// 非全租户操作者对 S 为 NULL 的角色不可见（G 的硬前提）。
func TestAssignmentRoleCandidates(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()

	t.Run("full tenant operator is bounded by target dept range", func(t *testing.T) {
		set, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 10, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list assignable: %v", err)
		}
		// 目标 10（部门 5）：角色 102 的目标范围 = M {2,5} ∩ subtree(2)
		// {2,7,8} = ∅ → 不可分配；其余角色无映射且无组织范围，全租户操作者可分配。
		want := []int64{103, 104, 106, 107, 108, 109, 110}
		if got := optionIDs(set); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
		if set.HasMore || set.Page != 1 || set.Total != nil {
			t.Fatalf("envelope = has_more %v page %d total %v", set.HasMore, set.Page, set.Total)
		}
	})

	t.Run("owned roles are excluded", func(t *testing.T) {
		set, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 2, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list assignable: %v", err)
		}
		// 目标 2（部门 2，技术部）：角色 102 的目标范围命中（2 ∈ M 且
		// 2 ∈ subtree(2)）；103/108 已拥有排除。
		want := []int64{102, 104, 106, 107, 109, 110}
		if got := optionIDs(set); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v (103/108 owned)", got, want)
		}
	})

	t.Run("revoked role reappears as candidate", func(t *testing.T) {
		// 撤销改软删后历史行不排除：软删用户 2 的角色 103 映射（种子
		// 900103），103 应重新出现在候选；活跃映射 108 仍排除。
		execIdentityTx(t, d, assignmentAuth(),
			`UPDATE public.sys_user_role_mapping SET deleted_at = CURRENT_TIMESTAMP, deleted_by = $1
             WHERE user_id = $2 AND role_id = $3 AND deleted_at IS NULL`,
			assignmentAdmin, int64(2), int64(103))

		set, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 2, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list assignable after revoke: %v", err)
		}
		want := []int64{102, 103, 104, 106, 107, 109, 110}
		if got := optionIDs(set); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v (103 reappears, 108 owned)", got, want)
		}
	})

	t.Run("scoped operator without full tenant sees no S-null role", func(t *testing.T) {
		// 操作者 4 非全租户（O_d={4}）：S 为 NULL 的角色 G 直接不成立；
		// 角色 102 的 M {2,5} ∩ O_d {4} = ∅，A 也不成立 → 候选为空。
		set, err := repo.ListAssignableRoles(ctx, scopedAuth(), 8, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("list assignable: %v", err)
		}
		if got := optionIDs(set); len(got) != 0 {
			t.Fatalf("candidates = %v, want empty", got)
		}
	})

	t.Run("keyword matches name and code", func(t *testing.T) {
		byName, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 10, biz.OptionQuery{Keyword: "普通", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("keyword name: %v", err)
		}
		if got := optionIDs(byName); !slices.Equal(got, []int64{103}) {
			t.Fatalf("keyword name candidates = %v, want [103]", got)
		}
		byCode, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 10, biz.OptionQuery{Keyword: "test_admin", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("keyword code: %v", err)
		}
		if got := optionIDs(byCode); !slices.Equal(got, []int64{110}) {
			t.Fatalf("keyword code candidates = %v, want [110]", got)
		}
	})

	t.Run("pagination uses limit plus one", func(t *testing.T) {
		first, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 10, biz.OptionQuery{Page: 1, PageSize: 3})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(first.Items) != 3 || !first.HasMore {
			t.Fatalf("page 1 = %d items, has_more %v", len(first.Items), first.HasMore)
		}
		last, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 10, biz.OptionQuery{Page: 3, PageSize: 3})
		if err != nil {
			t.Fatalf("page 3: %v", err)
		}
		if len(last.Items) != 1 || last.HasMore {
			t.Fatalf("page 3 = %d items, has_more %v", len(last.Items), last.HasMore)
		}
	})

	t.Run("cross-tenant and missing targets are not found", func(t *testing.T) {
		if _, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 5, biz.OptionQuery{Page: 1, PageSize: 20}); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("cross-tenant target err = %v, want ErrUserNotFound", err)
		}
		if _, err := repo.ListAssignableRoles(ctx, assignmentAuth(), 424242, biz.OptionQuery{Page: 1, PageSize: 20}); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("missing target err = %v, want ErrUserNotFound", err)
		}
	})

	t.Run("invisible-but-existing target is listable", func(t *testing.T) {
		// 新方案不要求目标可见：操作者 4 看不见用户 3，仍可查询其候选
		//（本例按部门范围判空）。
		set, err := repo.ListAssignableRoles(ctx, scopedAuth(), 3, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("invisible target list: %v", err)
		}
		if got := optionIDs(set); len(got) != 0 {
			t.Fatalf("candidates = %v, want empty (dept range)", got)
		}
	})
}

// TestAssignmentUserRoleSet 覆盖当前角色集合：managed 明细、locked 只
// 计数、禁用目标全部 locked、目标按 ID 判存在（不判可见性）。
func TestAssignmentUserRoleSet(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()

	t.Run("manageable roles with version", func(t *testing.T) {
		set, err := repo.ListUserRoles(ctx, assignmentAuth(), 2)
		if err != nil {
			t.Fatalf("list user roles: %v", err)
		}
		if got := roleSetIDs(set); !slices.Equal(got, []int64{103, 108}) {
			t.Fatalf("items = %v, want [103 108]", got)
		}
		for _, o := range set.Items {
			if !o.Assigned || !o.Enabled || o.Label == "" {
				t.Fatalf("option %+v must be assigned/enabled with label", o)
			}
		}
		if set.UnmanagedCount != 0 {
			t.Fatalf("unmanaged = %d, want 0", set.UnmanagedCount)
		}
		if set.Version != biz.UserRoleSetVersion(2, []int64{103, 108}) {
			t.Fatalf("version = %q", set.Version)
		}
	})

	t.Run("locked roles are counted not listed", func(t *testing.T) {
		// 操作者 4 非全租户：角色 106 的组织范围为 NULL（G 不成立）、
		// 角色 102 的映射 {2,5} 与其可见部门 {4} 无交集（A 不成立），
		// 两条映射对操作者 4 均为 locked，只计数不暴露明细。
		insertMapping(t, d, assignmentAuth(), 800102, 8, 102)

		set, err := repo.ListUserRoles(ctx, scopedAuth(), 8)
		if err != nil {
			t.Fatalf("list user roles: %v", err)
		}
		if len(set.Items) != 0 {
			t.Fatalf("items = %v, want empty (both locked)", roleSetIDs(set))
		}
		if set.UnmanagedCount != 2 {
			t.Fatalf("unmanaged = %d, want 2", set.UnmanagedCount)
		}
	})

	t.Run("disabled target roles are all locked", func(t *testing.T) {
		execIdentityTx(t, d, assignmentAuth(), `UPDATE public.sys_user SET status = 0 WHERE id = 10`)
		insertMapping(t, d, assignmentAuth(), 800103, 10, 103)

		set, err := repo.ListUserRoles(ctx, assignmentAuth(), 10)
		if err != nil {
			t.Fatalf("list user roles: %v", err)
		}
		if len(set.Items) != 0 || set.UnmanagedCount != 1 {
			t.Fatalf("set = %d items, unmanaged %d; want 0/1", len(set.Items), set.UnmanagedCount)
		}
		if set.Version != biz.UserRoleSetVersion(10, nil) {
			t.Fatalf("version = %q, want empty-set version", set.Version)
		}
	})

	t.Run("cross-tenant target is not found", func(t *testing.T) {
		if _, err := repo.ListUserRoles(ctx, assignmentAuth(), 5); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("err = %v, want ErrUserNotFound", err)
		}
	})

	t.Run("missing target is not found", func(t *testing.T) {
		if _, err := repo.ListUserRoles(ctx, assignmentAuth(), 424242); !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("err = %v, want ErrUserNotFound", err)
		}
	})
}

// TestSetUserRolesReplacesManageable 覆盖整组替换：新增/移除、locked 保留、
// 触及 locked 拒绝、不可分配新增拒绝、版本冲突无写入。
func TestSetUserRolesReplacesManageable(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()
	auth := assignmentAuth()

	initial, err := repo.ListUserRoles(ctx, auth, 10)
	if err != nil {
		t.Fatalf("list initial: %v", err)
	}
	v0 := initial.Version

	set, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: 10, RoleIDs: []int64{104, 103}, ExpectedVersion: v0,
	})
	if err != nil {
		t.Fatalf("add roles: %v", err)
	}
	if got := roleSetIDs(set); !slices.Equal(got, []int64{103, 104}) {
		t.Fatalf("items = %v, want [103 104]", got)
	}
	if got := scanMappings(t, d, auth, 10); !slices.Equal(got, []int64{103, 104}) {
		t.Fatalf("mappings = %v, want [103 104]", got)
	}

	set, err = repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: 10, RoleIDs: []int64{104}, ExpectedVersion: set.Version,
	})
	if err != nil {
		t.Fatalf("remove role: %v", err)
	}
	if got := scanMappings(t, d, auth, 10); !slices.Equal(got, []int64{104}) {
		t.Fatalf("mappings after remove = %v, want [104]", got)
	}

	set, err = repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: 10, RoleIDs: nil, ExpectedVersion: set.Version,
	})
	if err != nil {
		t.Fatalf("clear roles: %v", err)
	}
	if got := scanMappings(t, d, auth, 10); len(got) != 0 {
		t.Fatalf("mappings after clear = %v, want empty", got)
	}
	if len(set.Items) != 0 || set.UnmanagedCount != 0 {
		t.Fatalf("clear set = %d items, unmanaged %d", len(set.Items), set.UnmanagedCount)
	}
}

// TestSetUserRolesLockedAndForbidden 覆盖 locked 角色保留与拒绝分支：
// 触及 locked、不可分配新增、非全租户操作者分配 S 为 NULL 的角色均无写入。
func TestSetUserRolesLockedAndForbidden(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()
	auth := scopedAuth()

	insertMapping(t, d, assignmentAuth(), 800202, 8, 102)

	initial, err := repo.ListUserRoles(ctx, auth, 8)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 角色 106 的组织范围为 NULL、角色 102 的映射与操作者 4 的可见部门
	// 无交集：两条映射均 locked，不暴露明细。
	if len(roleSetIDs(initial)) != 0 || initial.UnmanagedCount != 2 {
		t.Fatalf("initial set = %v, unmanaged %d, want empty/2", roleSetIDs(initial), initial.UnmanagedCount)
	}

	t.Run("empty replace preserves locked", func(t *testing.T) {
		set, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
			UserID: 8, RoleIDs: nil, ExpectedVersion: initial.Version,
		})
		if err != nil {
			t.Fatalf("set empty: %v", err)
		}
		if got := scanMappings(t, d, auth, 8); !slices.Equal(got, []int64{102, 106}) {
			t.Fatalf("mappings = %v, want [102 106] (locked preserved)", got)
		}
		if set.UnmanagedCount != 2 || len(set.Items) != 0 {
			t.Fatalf("set = %d items, unmanaged %d, want 0/2", len(set.Items), set.UnmanagedCount)
		}
	})

	t.Run("request touching locked is forbidden", func(t *testing.T) {
		for _, roleID := range []int64{102, 106} {
			_, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
				UserID: 8, RoleIDs: []int64{roleID}, ExpectedVersion: initial.Version,
			})
			if !errors.Is(err, biz.ErrUserAccessDenied) {
				t.Fatalf("touching locked %d err = %v, want ErrUserAccessDenied", roleID, err)
			}
		}
		if got := scanMappings(t, d, auth, 8); !slices.Equal(got, []int64{102, 106}) {
			t.Fatalf("mappings after forbidden = %v, want unchanged [102 106]", got)
		}
	})

	t.Run("unassignable addition is forbidden", func(t *testing.T) {
		// 新角色 120 配置分发部门 {2}：目标 8 的部门 4 不在映射内，
		// fn_can_assign_role 恒 false（不可分配也不可撤销）。
		execIdentityTx(t, d, assignmentAuth(), `INSERT INTO public.sys_role (id, status, tenant_id, "name", code, role_type, data_scope, created_by, updated_by)
VALUES (120, 1, $1, '受限角色', 'restricted_role', 2, 1, $2, $2) ON CONFLICT DO NOTHING`, assignmentTenant, assignmentAdmin)
		execIdentityTx(t, d, assignmentAuth(), `INSERT INTO public.sys_role_allowed_dept (id, status, role_id, dept_id, tenant_id, created_by, updated_by)
VALUES (12001, 1, 120, 2, $1, $2, $2) ON CONFLICT DO NOTHING`, assignmentTenant, assignmentAdmin)

		_, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
			UserID: 8, RoleIDs: []int64{120}, ExpectedVersion: initial.Version,
		})
		if !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
		if got := scanMappings(t, d, auth, 8); !slices.Equal(got, []int64{102, 106}) {
			t.Fatalf("mappings after forbidden add = %v, want unchanged", got)
		}
	})

	t.Run("scoped operator cannot grant S-null role", func(t *testing.T) {
		// 角色 103 无组织范围：G 要求操作人全租户（O_d 为 NULL），操作者 4
		// 不满足 → 新增被拒。
		_, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
			UserID: 8, RoleIDs: []int64{103}, ExpectedVersion: initial.Version,
		})
		if !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
		if got := scanMappings(t, d, auth, 8); !slices.Equal(got, []int64{102, 106}) {
			t.Fatalf("mappings after forbidden grant = %v, want unchanged", got)
		}
	})

	t.Run("version mismatch is conflict without writes", func(t *testing.T) {
		// 目标 10 加载后由他人追加角色 103，旧 version 的提交必须 409。
		base, err := repo.ListUserRoles(ctx, assignmentAuth(), 10)
		if err != nil {
			t.Fatalf("list base: %v", err)
		}
		insertMapping(t, d, assignmentAuth(), 800210, 10, 103)
		_, err = repo.SetUserRoles(ctx, assignmentAuth(), biz.SetUserRolesInput{
			UserID: 10, RoleIDs: []int64{104}, ExpectedVersion: base.Version,
		})
		if !errors.Is(err, biz.ErrUserRoleSetChanged) {
			t.Fatalf("err = %v, want ErrUserRoleSetChanged", err)
		}
		if got := scanMappings(t, d, assignmentAuth(), 10); !slices.Equal(got, []int64{103}) {
			t.Fatalf("mappings = %v, want [103] (no 104 written)", got)
		}
	})

	t.Run("missing target is not found", func(t *testing.T) {
		_, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
			UserID: 424242, RoleIDs: nil, ExpectedVersion: biz.UserRoleSetVersion(424242, nil),
		})
		if !errors.Is(err, biz.ErrUserNotFound) {
			t.Fatalf("err = %v, want ErrUserNotFound", err)
		}
	})
}

// TestSetUserRolesSelfLock 覆盖自锁保护：移除自己最后一个带 assign-role
// 的有效角色拒绝；locked 角色兜底时放行。
func TestSetUserRolesSelfLock(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()
	auth := assignmentAuth()

	initial, err := repo.ListUserRoles(ctx, auth, assignmentAdmin)
	if err != nil {
		t.Fatalf("list self: %v", err)
	}
	if got := roleSetIDs(initial); !slices.Equal(got, []int64{110}) || initial.UnmanagedCount != 0 {
		t.Fatalf("initial self set = %v, unmanaged %d", got, initial.UnmanagedCount)
	}

	if _, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: assignmentAdmin, RoleIDs: nil, ExpectedVersion: initial.Version,
	}); !errors.Is(err, biz.ErrUserAccessDenied) {
		t.Fatalf("self-lock err = %v, want ErrUserAccessDenied", err)
	}
	if got := scanMappings(t, d, auth, assignmentAdmin); !slices.Equal(got, []int64{110}) {
		t.Fatalf("mappings after rejected self-lock = %v, want [110]", got)
	}

	// 追加角色 102（分发部门 {2,5} 不含操作者部门 1 → locked，但带
	// `sys:user:assign-role`（菜单 3208）），移除 110 后仍有兜底。
	insertMapping(t, d, auth, 800301, assignmentAdmin, 102)
	withLocked, err := repo.ListUserRoles(ctx, auth, assignmentAdmin)
	if err != nil {
		t.Fatalf("list with locked: %v", err)
	}
	if withLocked.UnmanagedCount != 1 || !slices.Equal(roleSetIDs(withLocked), []int64{110}) {
		t.Fatalf("set with locked = %v, unmanaged %d", roleSetIDs(withLocked), withLocked.UnmanagedCount)
	}
	set, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: assignmentAdmin, RoleIDs: nil, ExpectedVersion: withLocked.Version,
	})
	if err != nil {
		t.Fatalf("self removal with locked fallback: %v", err)
	}
	if got := scanMappings(t, d, auth, assignmentAdmin); !slices.Equal(got, []int64{102}) {
		t.Fatalf("mappings = %v, want [102]", got)
	}
	if len(set.Items) != 0 || set.UnmanagedCount != 1 {
		t.Fatalf("set = %d items, unmanaged %d", len(set.Items), set.UnmanagedCount)
	}
}

// TestAssignmentPermissionSeeds 校验测试种子身份具备分配选择器所需权限码：
// 用户 11（角色 110）与用户 4（角色 102）持有 assign-role/transfer-owner，
// 用户 2（角色 103）无码。
func TestAssignmentPermissionSeeds(t *testing.T) {
	d := setupTestDB(t)
	perms := NewPermissionRepo(d)
	ctx := context.Background()

	for _, code := range []string{biz.PermUserAssignRole, biz.PermUserTransferOwner} {
		for _, auth := range []biz.Identity{assignmentAuth(), scopedAuth()} {
			ok, err := perms.HasPermission(ctx, auth, code)
			if err != nil {
				t.Fatalf("has permission %s for user %d: %v", code, auth.UserID, err)
			}
			if !ok {
				t.Fatalf("user %d must hold %s", auth.UserID, code)
			}
		}
		ok, err := perms.HasPermission(ctx, biz.Identity{TenantID: assignmentTenant, UserID: 2}, code)
		if err != nil {
			t.Fatalf("has permission %s for user 2: %v", code, err)
		}
		if ok {
			t.Fatalf("user 2 (role 103) must not hold %s", code)
		}
	}
}

// TestOwnerOptionCandidates 覆盖归属候选的 fn_accessible_* 三态：全租户
// （NULL）、无有效角色（'{}'）、范围数组；以及关键字、label 与分页。
func TestOwnerOptionCandidates(t *testing.T) {
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()

	t.Run("full tenant operator sees everything", func(t *testing.T) {
		users, err := repo.ListOwnerUserOptions(ctx, assignmentAuth(), biz.OptionQuery{Page: 1, PageSize: 100})
		if err != nil {
			t.Fatalf("owner users: %v", err)
		}
		wantUsers := []int64{2, 3, 4, 6, 7, 8, 9, 10, 11}
		if got := optionIDs(users); !slices.Equal(got, wantUsers) {
			t.Fatalf("users = %v, want %v", got, wantUsers)
		}
		for _, o := range users.Items {
			if o.Value == 2 && o.Label != "张三" {
				t.Fatalf("user 2 label = %q, want nickname 张三", o.Label)
			}
			if o.Assigned || !o.Enabled {
				t.Fatalf("owner option %+v must be enabled/unassigned", o)
			}
		}
		depts, err := repo.ListOwnerDeptOptions(ctx, assignmentAuth(), biz.OptionQuery{Page: 1, PageSize: 100})
		if err != nil {
			t.Fatalf("owner depts: %v", err)
		}
		if got := optionIDs(depts); !slices.Equal(got, []int64{1, 2, 3, 4, 5, 6, 7, 8}) {
			t.Fatalf("depts = %v", got)
		}
		groups, err := repo.ListOwnerGroupOptions(ctx, assignmentAuth(), biz.OptionQuery{Page: 1, PageSize: 100})
		if err != nil {
			t.Fatalf("owner groups: %v", err)
		}
		if got := optionIDs(groups); !slices.Equal(got, []int64{1, 2}) {
			t.Fatalf("groups = %v", got)
		}
	})

	t.Run("operator without roles sees nothing", func(t *testing.T) {
		auth := biz.Identity{TenantID: "t-super-01", UserID: 12}
		users, err := repo.ListOwnerUserOptions(ctx, auth, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner users: %v", err)
		}
		depts, err := repo.ListOwnerDeptOptions(ctx, auth, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner depts: %v", err)
		}
		groups, err := repo.ListOwnerGroupOptions(ctx, auth, biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner groups: %v", err)
		}
		if len(users.Items) != 0 || len(depts.Items) != 0 || len(groups.Items) != 0 {
			t.Fatalf("empty scope candidates = %d/%d/%d, want 0", len(users.Items), len(depts.Items), len(groups.Items))
		}
	})

	t.Run("scoped operator sees accessible arrays", func(t *testing.T) {
		users, err := repo.ListOwnerUserOptions(ctx, scopedAuth(), biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner users: %v", err)
		}
		if got := optionIDs(users); !slices.Equal(got, []int64{4, 8}) {
			t.Fatalf("users = %v, want [4 8]", got)
		}
		depts, err := repo.ListOwnerDeptOptions(ctx, scopedAuth(), biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner depts: %v", err)
		}
		if got := optionIDs(depts); !slices.Equal(got, []int64{4}) {
			t.Fatalf("depts = %v, want [4]", got)
		}
		groups, err := repo.ListOwnerGroupOptions(ctx, scopedAuth(), biz.OptionQuery{Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("owner groups: %v", err)
		}
		if len(groups.Items) != 0 {
			t.Fatalf("groups = %v, want empty (data_scope=3 no groups)", optionIDs(groups))
		}
	})

	t.Run("keyword and pagination", func(t *testing.T) {
		users, err := repo.ListOwnerUserOptions(ctx, assignmentAuth(), biz.OptionQuery{Keyword: "张", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("keyword users: %v", err)
		}
		if got := optionIDs(users); !slices.Equal(got, []int64{2}) {
			t.Fatalf("keyword users = %v, want [2]", got)
		}
		depts, err := repo.ListOwnerDeptOptions(ctx, assignmentAuth(), biz.OptionQuery{Keyword: "技术", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("keyword depts: %v", err)
		}
		if got := optionIDs(depts); !slices.Equal(got, []int64{2}) {
			t.Fatalf("keyword depts = %v, want [2]", got)
		}
		groups, err := repo.ListOwnerGroupOptions(ctx, assignmentAuth(), biz.OptionQuery{Keyword: "core", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("keyword groups: %v", err)
		}
		if got := optionIDs(groups); !slices.Equal(got, []int64{1}) {
			t.Fatalf("keyword groups = %v, want [1]", got)
		}
		first, err := repo.ListOwnerUserOptions(ctx, assignmentAuth(), biz.OptionQuery{Page: 1, PageSize: 2})
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(first.Items) != 2 || !first.HasMore {
			t.Fatalf("page 1 = %d items, has_more %v", len(first.Items), first.HasMore)
		}
		last, err := repo.ListOwnerUserOptions(ctx, assignmentAuth(), biz.OptionQuery{Page: 5, PageSize: 2})
		if err != nil {
			t.Fatalf("page 5: %v", err)
		}
		if len(last.Items) != 1 || last.HasMore {
			t.Fatalf("page 5 = %d items, has_more %v", len(last.Items), last.HasMore)
		}
	})
}

// TestAssignmentTenantDiscipline 校验未设置租户的事务无法访问分配面数据
// （RLS 默认全拒的存储层兜底）。
func TestAssignmentTenantDiscipline(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	if _, err := newAssignmentRepo(d).ListUserRoles(ctx, biz.Identity{UserID: assignmentAdmin}, 2); err == nil {
		t.Fatal("empty tenant identity must be rejected")
	}
}
