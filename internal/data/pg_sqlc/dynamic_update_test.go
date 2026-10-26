package pg_sqlc

import (
	"context"
	"errors"
	"testing"
	"time"

	"grape/internal/data/pg_sqlc/gen"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// typed Update 面（@updatable 声明生成的动态更新）集成测试：以真实
// PostgreSQL 验证 SET 白名单、@pk 定位、@filter 不变量与守卫语义。
// 断言基于 sqlc/seed.sql 的种子事实（用户 1-10 属 t-super-01，用户 5
// 属 t-demo-01，basic 行 1-10 属 t-super-01）。环境变量未设置时跳过
// （同 session_test.go）。
func TestDynamicUpdate(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	super := Identity{TenantID: "t-super-01", UserID: 1}

	// updateSysUser 在 super 身份事务内执行一次动态更新。
	updateSysUser := func(t *testing.T, opts ...gen.SysUserUpdateOption) (int64, error) {
		t.Helper()
		var n int64
		err := d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			var err error
			n, err = gen.UpdateSysUser(ctx, s.Tx(), opts...)
			return err
		})
		return n, err
	}
	// softDeleteSysUser 就地软删种子行，供需要 deleted_at IS NOT NULL
	// 前提的子测试自备副作用（不依赖其他子测试的执行顺序）。
	softDeleteSysUser := func(t *testing.T, id int64) {
		t.Helper()
		err := d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			_, err := s.Tx().Exec(ctx, "UPDATE public.sys_user SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1", id)
			return err
		})
		if err != nil {
			t.Fatalf("soft delete user %d: %v", id, err)
		}
	}
	// getSysUser 同事务回读一行用户。
	getSysUser := func(t *testing.T, id int64) *gen.SysUser {
		t.Helper()
		var po *gen.SysUser
		err := d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			row, err := gen.New(s.Tx()).GetSysUserByID(ctx, id)
			if err != nil {
				return err
			}
			po = &row
			return nil
		})
		if err != nil {
			t.Fatalf("get sys_user %d: %v", id, err)
		}
		return po
	}

	t.Run("pk_where_single_set_updates_row", func(t *testing.T) {
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("updated by typed update")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(3),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		po := getSysUser(t, 3)
		if po.Remark == nil || *po.Remark != "updated by typed update" {
			t.Fatalf("remark = %v, want updated by typed update", po.Remark)
		}
		if po.UpdatedBy != 9 {
			t.Fatalf("updated_by = %d, want 9", po.UpdatedBy)
		}
	})

	t.Run("pk_where_multi_set_updates_all_columns", func(t *testing.T) {
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetStatus(0),
			gen.SysUserUpdate.SetLocale("en-US"),
			gen.SysUserUpdate.SetNickname("renamed"),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(4),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		po := getSysUser(t, 4)
		if po.Status != 0 {
			t.Fatalf("status = %d, want 0", po.Status)
		}
		if po.Locale != "en-US" {
			t.Fatalf("locale = %q, want en-US", po.Locale)
		}
		if po.Nickname != "renamed" {
			t.Fatalf("nickname = %q, want renamed", po.Nickname)
		}
	})

	t.Run("soft_deleted_row_skipped_without_bypass", func(t *testing.T) {
		softDeleteSysUser(t, 2)
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("should not land")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(2),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 0 {
			t.Fatalf("affected rows on soft-deleted row = %d, want 0 (filter invariant)", n)
		}
	})

	t.Run("soft_deleted_row_updated_with_bypass_filter", func(t *testing.T) {
		softDeleteSysUser(t, 2)
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("bypassed")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(2),
			gen.SysUserUpdate.WithoutFilter(),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows with bypass = %d, want 1", n)
		}
	})

	t.Run("other_tenant_row_skipped_by_rls", func(t *testing.T) {
		// 用户 5 属 t-demo-01；t-super-01 事务内按 id 点改必须 0 行。
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("cross tenant")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(5),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 0 {
			t.Fatalf("affected rows in other tenant = %d, want 0 (RLS)", n)
		}
	})

	t.Run("unset_columns_keep_original_values", func(t *testing.T) {
		// 部分更新语义：SET 白名单外的列不被触碰。updated_at 不参与
		// 断言：trg_touch_updated_at 对任何 UPDATE 兜底刷新（与静态
		// 更新一致），属库级行为而非动态更新面语义。
		// 用户 8 不被其他子测试改动。
		before := getSysUser(t, 8)
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("only remark moves")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(8),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		after := getSysUser(t, 8)
		if after.Status != before.Status {
			t.Fatalf("status = %d, want untouched %d", after.Status, before.Status)
		}
		if after.Name != before.Name {
			t.Fatalf("name = %q, want untouched %q", after.Name, before.Name)
		}
		if (after.Email == nil) != (before.Email == nil) || (after.Email != nil && *after.Email != *before.Email) {
			t.Fatalf("email = %v, want untouched %v", after.Email, before.Email)
		}
	})

	t.Run("bypass_filter_without_where_still_rejected", func(t *testing.T) {
		// WithoutFilter 只绕过 @filter 不变量，不豁免零 where 守卫。
		_, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("full table")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WithoutFilter(),
		)
		var de *dynq.Error
		if !errors.As(err, &de) || de.Kind != dynq.KindWhere {
			t.Fatalf("want dynq where-kind error, got %v", err)
		}
	})

	t.Run("empty_set_rejected", func(t *testing.T) {
		_, err := updateSysUser(t, gen.SysUserUpdate.WhereIDEq(3))
		var de *dynq.Error
		if !errors.As(err, &de) || de.Kind != dynq.KindSet {
			t.Fatalf("want dynq set-kind error, got %v", err)
		}
	})

	t.Run("zero_where_rejected_even_with_filter", func(t *testing.T) {
		// @filter 谓词恒叠加使最终 WHERE 非空，但零调用方条件必须拒绝。
		_, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("full table")),
			gen.SysUserUpdate.SetUpdatedBy(9),
		)
		var de *dynq.Error
		if !errors.As(err, &de) || de.Kind != dynq.KindWhere {
			t.Fatalf("want dynq where-kind error, got %v", err)
		}
	})

	t.Run("duplicate_set_slot_rejected", func(t *testing.T) {
		_, err := updateSysUser(t,
			gen.SysUserUpdate.SetRemark(dynq.Ptr("first")),
			gen.SysUserUpdate.SetRemark(dynq.Ptr("second")),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(3),
		)
		var de *dynq.Error
		if !errors.As(err, &de) || de.Kind != dynq.KindSet {
			t.Fatalf("want dynq set-kind error, got %v", err)
		}
	})

	t.Run("nil_pointer_clears_nullable_column", func(t *testing.T) {
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetEmail(nil),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(3),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		if po := getSysUser(t, 3); po.Email != nil {
			t.Fatalf("email = %v, want NULL", po.Email)
		}
	})

	t.Run("nullable_time_column_accepts_explicit_value", func(t *testing.T) {
		// 确定性：时间值由测试注入，不依赖数据库时钟。
		at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetLastLoginAt(dynq.Ptr(at)),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(3),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		po := getSysUser(t, 3)
		if !po.LastLoginAt.Valid || !po.LastLoginAt.Time.Equal(at) {
			t.Fatalf("last_login_at = %v, want %v", po.LastLoginAt, at)
		}
	})

	t.Run("internal_column_is_writable", func(t *testing.T) {
		// @internal 与 @updatable 正交：password 协议不可见但服务端可写。
		const newHash = "{scrypt}$e0801$new$hash"
		n, err := updateSysUser(t,
			gen.SysUserUpdate.SetPassword(newHash),
			gen.SysUserUpdate.SetUpdatedBy(9),
			gen.SysUserUpdate.WhereIDEq(3),
		)
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		if po := getSysUser(t, 3); po.Password != newHash {
			t.Fatalf("password = %q, want %q", po.Password, newHash)
		}
	})

	t.Run("boolean_column_toggled_on_flag_row", func(t *testing.T) {
		// 非 int/string 形态冒烟：bool 列（NOT NULL）走白名单 Set。
		var n int64
		err := d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			var err error
			n, err = gen.UpdateSysFeatureFlag(ctx, s.Tx(),
				gen.SysFeatureFlagUpdate.SetIsEnabled(false),
				gen.SysFeatureFlagUpdate.SetUpdatedBy(9),
				gen.SysFeatureFlagUpdate.WhereIDEq(1),
			)
			return err
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		var enabled bool
		err = d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			return s.Tx().QueryRow(ctx, "SELECT is_enabled FROM public.sys_feature_flag WHERE id = 1").Scan(&enabled)
		})
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if enabled {
			t.Fatal("is_enabled = true, want false")
		}
	})

	t.Run("basic_row_status_updated", func(t *testing.T) {
		var n int64
		err := d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			var err error
			n, err = gen.UpdateBasic(ctx, s.Tx(),
				gen.BasicUpdate.SetStatus(0),
				gen.BasicUpdate.SetUpdatedBy(9),
				gen.BasicUpdate.WhereIDEq(1),
			)
			return err
		})
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		if n != 1 {
			t.Fatalf("affected rows = %d, want 1", n)
		}
		var status int64
		err = d.IdentityTx(ctx, super, func(s *IdentityTx) error {
			return s.Tx().QueryRow(ctx, "SELECT status FROM public.basic WHERE id = 1").Scan(&status)
		})
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if status != 0 {
			t.Fatalf("status = %d, want 0", status)
		}
	})
}
