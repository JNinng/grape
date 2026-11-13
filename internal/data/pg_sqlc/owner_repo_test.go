package pg_sqlc

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"
)

// 数据归属与角色判定函数的存储边界集成测试：覆盖 fn_can_set_owner（创建
// 归属三元组的正/负例）、fn_can_transfer_owner 的新归属存在性/启用校验、
// fn_can_see_role（角色可见性 A AND G）与 fn_can_revoke_role（放宽角色/
// 目标状态校验）。种子事实见 sqlc/test_seed.sql（同 role_repo_test.go）。

// mustCreateUser 以给定归属三元组创建用户，返回错误而非终止（供负例断言）。
func tryCreateUser(d *Data, auth biz.Identity, name string, owner biz.OwnerTriple) (*biz.User, error) {
	u := &biz.User{
		Name:             name,
		Nickname:         name + "-nick",
		Password:         "hashed-" + name,
		Locale:           "zh-CN",
		Status:           biz.UserStatusEnabled,
		OwnerUserID:      owner.UserID,
		OwnerDeptID:      owner.DeptID,
		OwnerUserGroupID: owner.GroupID,
	}
	return NewUserRepo(d).Create(context.Background(), auth, u)
}

func TestCreateUser_owner_triple_validated(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	auth := assignmentAuth() // 用户 11（data_scope=7，全租户可见）
	repo := NewUserRepo(d)

	t.Run("valid owner triple is accepted", func(t *testing.T) {
		dept := int64(5)
		group := int64(1)
		owner := int64(2)
		u, err := tryCreateUser(d, auth, "owner_ok", biz.OwnerTriple{UserID: &owner, DeptID: &dept, GroupID: &group})
		if err != nil {
			t.Fatalf("create with valid owners: %v", err)
		}
		got, err := repo.GetByID(ctx, auth, u.ID)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if got.OwnerUserID == nil || *got.OwnerUserID != owner ||
			got.OwnerDeptID == nil || *got.OwnerDeptID != dept ||
			got.OwnerUserGroupID == nil || *got.OwnerUserGroupID != group {
			t.Fatalf("owners = (%v, %v, %v), want (%d, %d, %d)",
				got.OwnerUserID, got.OwnerDeptID, got.OwnerUserGroupID, owner, dept, group)
		}
	})

	t.Run("dangling owner user is rejected", func(t *testing.T) {
		ghost := int64(424242)
		if _, err := tryCreateUser(d, auth, "owner_user_ghost", biz.OwnerTriple{UserID: &ghost}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
	})

	t.Run("dangling owner dept and group are rejected", func(t *testing.T) {
		ghost := int64(424242)
		if _, err := tryCreateUser(d, auth, "owner_dept_ghost", biz.OwnerTriple{DeptID: &ghost}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
		if _, err := tryCreateUser(d, auth, "owner_group_ghost", biz.OwnerTriple{GroupID: &ghost}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
	})

	t.Run("disabled owner user is rejected", func(t *testing.T) {
		execIdentityTx(t, d, auth, `UPDATE public.sys_user SET status = 0 WHERE id = 9`)
		disabled := int64(9)
		if _, err := tryCreateUser(d, auth, "owner_user_disabled", biz.OwnerTriple{UserID: &disabled}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
	})

	t.Run("owner outside operator scope is rejected", func(t *testing.T) {
		// 操作者 4（data_scope=3，可见用户 {4,8}）：用户 3 存在但不可见
		// → 新归属不可达，拒绝。
		invisible := int64(3)
		if _, err := tryCreateUser(d, scopedAuth(), "owner_user_invisible", biz.OwnerTriple{UserID: &invisible}); !errors.Is(err, biz.ErrUserAccessDenied) {
			t.Fatalf("err = %v, want ErrUserAccessDenied", err)
		}
	})
}

func TestTransferUserOwner_new_owner_validated(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	auth := assignmentAuth()
	repo := NewUserRepo(d)

	// 新归属部门悬空（不存在/跨租户等同不存在）→ 新归属不可达，拒绝。
	ghost := int64(424242)
	if _, err := repo.TransferOwner(ctx, auth, 6, biz.OwnerTriple{DeptID: &ghost}); !errors.Is(err, biz.ErrUserAccessDenied) {
		t.Fatalf("err = %v, want ErrUserAccessDenied", err)
	}
	// 受限操作者的可见范围同样生效：操作者 4 对用户 6 不可操作
	//（旧归属判定即拒绝），先证可见性路径未破坏。
	if _, err := repo.TransferOwner(ctx, scopedAuth(), 6, biz.OwnerTriple{}); !errors.Is(err, biz.ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

func TestCanSeeRole_visibility_core(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	see := func(auth biz.Identity, roleID int64) bool {
		t.Helper()
		var ok bool
		if err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
			got, err := s.Q().CanSeeRole(context.Background(), gen.CanSeeRoleParams{Operator: auth.UserID, Role: roleID})
			if err != nil {
				return err
			}
			ok = got
			return nil
		}); err != nil {
			t.Fatalf("can see role: %v", err)
		}
		return ok
	}

	if !see(assignmentAuth(), 102) {
		t.Fatal("full tenant operator must see role 102")
	}
	if see(assignmentAuth(), 105) {
		t.Fatal("cross-tenant role must be invisible")
	}
	if see(scopedAuth(), 103) {
		t.Fatal("S-null role must be invisible to non-full-tenant operator (G)")
	}
	if see(scopedAuth(), 102) {
		t.Fatal("role 102 mapping {2,5} must not hit operator 4 visible depts {4} (A)")
	}

	// 停用角色仍可见（角色管理面需要看到停用角色），软删角色不可见。
	auth := roleAuth(roleTenantSeeded)
	repo := NewRoleRepo(d)
	created := mustCreateRole(t, d, auth, &biz.Role{Name: "cycle-see", Code: "cycle-see", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
	if !see(auth, created.ID) {
		t.Fatal("created role must be visible to full tenant operator")
	}
	created.Status = biz.RoleStatusDisabled
	if _, err := repo.Update(context.Background(), auth, created); err != nil {
		t.Fatalf("disable role: %v", err)
	}
	if !see(auth, created.ID) {
		t.Fatal("disabled role must stay visible (management face)")
	}
	if err := repo.SoftDelete(context.Background(), auth, created.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	if see(auth, created.ID) {
		t.Fatal("soft deleted role must be invisible")
	}
}

func TestCanRevokeRole_relaxed_statuses(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	auth := assignmentAuth()
	revoke := func(operator biz.Identity, roleID, target int64) bool {
		t.Helper()
		var ok bool
		if err := d.IdentityTx(context.Background(), pgAuth(operator), func(s *IdentityTx) error {
			got, err := s.Q().CanRevokeRole(context.Background(), gen.CanRevokeRoleParams{Operator: operator.UserID, Role: roleID, Target: target})
			if err != nil {
				return err
			}
			ok = got
			return nil
		}); err != nil {
			t.Fatalf("can revoke role: %v", err)
		}
		return ok
	}

	t.Run("disabled target user is revocable", func(t *testing.T) {
		execIdentityTx(t, d, auth, `UPDATE public.sys_user SET status = 0 WHERE id = 10`)
		if !revoke(auth, 103, 10) {
			t.Fatal("disabled target user must stay revocable")
		}
	})

	t.Run("disabled and soft deleted role is revocable", func(t *testing.T) {
		created := mustCreateRole(t, d, auth, &biz.Role{Name: "revoke-cycle", Code: "revoke-cycle", Status: biz.RoleStatusEnabled, RoleType: biz.RoleTypeCustom, DataScope: biz.RoleDataScopeSelf})
		created.Status = biz.RoleStatusDisabled
		if _, err := NewRoleRepo(d).Update(context.Background(), auth, created); err != nil {
			t.Fatalf("disable role: %v", err)
		}
		if !revoke(auth, created.ID, 2) {
			t.Fatal("disabled role mapping must stay revocable")
		}
		if err := NewRoleRepo(d).SoftDelete(context.Background(), auth, created.ID); err != nil {
			t.Fatalf("delete role: %v", err)
		}
		if !revoke(auth, created.ID, 2) {
			t.Fatal("soft deleted role mapping must stay revocable")
		}
	})

	t.Run("out of range target is not revocable", func(t *testing.T) {
		// 角色 102 的目标范围 = M {2,5} ∩ subtree(2)：目标 10（部门 5）
		// 落在子树外 → 不可撤销（与分配同口径）。
		if revoke(auth, 102, 10) {
			t.Fatal("out of range target must not be revocable")
		}
		if !revoke(auth, 102, 2) {
			t.Fatal("in range target must stay revocable")
		}
	})
}

func TestSetUserRoles_revoke_on_disabled_target(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	repo := newAssignmentRepo(d)
	ctx := context.Background()
	auth := assignmentAuth()

	// 禁用目标用户后，其既有映射的撤销不受目标状态阻碍（fn_can_revoke_role
	// 放宽目标状态校验）：整组替换清空成功、映射软删留痕。
	insertMapping(t, d, auth, 800401, 10, 103)
	execIdentityTx(t, d, auth, `UPDATE public.sys_user SET status = 0 WHERE id = 10`)

	base, err := repo.ListUserRoles(ctx, auth, 10)
	if err != nil {
		t.Fatalf("list base: %v", err)
	}
	if _, err := repo.SetUserRoles(ctx, auth, biz.SetUserRolesInput{
		UserID: 10, RoleIDs: nil, ExpectedVersion: base.Version,
	}); err != nil {
		t.Fatalf("revoke on disabled target: %v", err)
	}
	if got := scanMappings(t, d, auth, 10); len(got) != 0 {
		t.Fatalf("mappings after revoke = %v, want empty", got)
	}
	var revokedBy int64
	if err := d.IdentityTx(context.Background(), pgAuth(auth), func(s *IdentityTx) error {
		return s.Tx().QueryRow(context.Background(),
			`SELECT deleted_by FROM public.sys_user_role_mapping WHERE user_id = 10 AND role_id = 103`).Scan(&revokedBy)
	}); err != nil {
		t.Fatalf("read revoked row: %v", err)
	}
	if revokedBy != auth.UserID {
		t.Fatalf("deleted_by = %d, want %d (revoke audit)", revokedBy, auth.UserID)
	}
}
