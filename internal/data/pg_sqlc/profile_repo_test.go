package pg_sqlc

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"
)

// 个人中心存储边界测试：目标恒取认证身份，不判行级归属。种子事实
// （test_seed.sql）：用户 9（zhengshi）持角色 107（data_scope=5，指定
// 部门 5），其所在部门 6 不在可见集合内，正常读面看不见自己；个人中心
// 路径仍可读/写本人行。
func TestUserProfile_self_path_ignores_row_scope(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewUserRepo(d)
	self := biz.Identity{TenantID: "t-test-01", UserID: 9}

	if _, err := repo.GetByID(ctx, self, self.UserID); !errors.Is(err, biz.ErrUserNotFound) {
		t.Fatalf("scoped get self = %v, want ErrUserNotFound", err)
	}

	got, err := repo.GetMyProfile(ctx, self)
	if err != nil {
		t.Fatalf("get my profile: %v", err)
	}
	if got.ID != self.UserID || got.Name != "zhengshi" {
		t.Fatalf("profile = (id %d name %q), want (9, zhengshi)", got.ID, got.Name)
	}
}

func TestUserProfile_update_and_password(t *testing.T) {
	t.Parallel()
	d := setupTestDB(t)
	ctx := context.Background()
	repo := NewUserRepo(d)
	self := biz.Identity{TenantID: "t-test-01", UserID: 9}
	email := "zhengshi-new@example.com"

	updated, err := repo.UpdateMyProfile(ctx, self, &biz.User{
		Nickname: "zhengshi-new",
		Email:    &email,
		Locale:   "en-US",
	})
	if err != nil {
		t.Fatalf("update my profile: %v", err)
	}
	if updated.Nickname != "zhengshi-new" || updated.Email == nil || *updated.Email != email ||
		updated.Phone != nil || updated.Remark != nil || updated.Locale != "en-US" {
		t.Fatalf("updated profile = %+v, want nickname/email/locale and nulled phone/remark", updated)
	}

	hash, err := repo.GetMyPasswordHash(ctx, self)
	if err != nil || hash == "" {
		t.Fatalf("get my password hash = %q, %v", hash, err)
	}
	newHash, err := biz.HashPassword("new-pass-1")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := repo.SetMyPassword(ctx, self, newHash); err != nil {
		t.Fatalf("set my password: %v", err)
	}
	stored, err := repo.GetMyPasswordHash(ctx, self)
	if err != nil {
		t.Fatalf("read back password hash: %v", err)
	}
	ok, _, err := biz.VerifyPassword(stored, "new-pass-1")
	if err != nil || !ok {
		t.Fatalf("stored hash does not verify new password: ok=%v err=%v", ok, err)
	}
}
