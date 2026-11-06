package biz

import (
	"context"
	"errors"
	"testing"
)

// mockUserRepo 捕获用例入参：biz 只验证编排与领域校验，不触真实存储。
type mockUserRepo struct {
	UserRepo

	created *User

	profile    *User
	profileErr error
	updated    *User
	updateErr  error

	passwordHash     string
	passwordHashErr  error
	setPasswordHash  string
	setPasswordErr   error
	setPasswordCalls int
}

func (m *mockUserRepo) Create(_ context.Context, _ Identity, u *User) (*User, error) {
	m.created = u
	out := *u
	out.ID = 100
	return &out, nil
}

func (m *mockUserRepo) GetMyProfile(_ context.Context, _ Identity) (*User, error) {
	return m.profile, m.profileErr
}

func (m *mockUserRepo) UpdateMyProfile(_ context.Context, _ Identity, u *User) (*User, error) {
	m.updated = u
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return u, nil
}

func (m *mockUserRepo) GetMyPasswordHash(_ context.Context, _ Identity) (string, error) {
	return m.passwordHash, m.passwordHashErr
}

func (m *mockUserRepo) SetMyPassword(_ context.Context, _ Identity, hashedPassword string) error {
	m.setPasswordCalls++
	m.setPasswordHash = hashedPassword
	return m.setPasswordErr
}

func TestUserUsecaseCreateUserDefaultsOwnerToCreator(t *testing.T) {
	repo := &mockUserRepo{}
	uc := NewUserUsecase(repo)
	auth := Identity{TenantID: "t-1", UserID: 7}

	if _, err := uc.CreateUser(context.Background(), auth, &User{
		Name: "alice", Nickname: "Alice", Password: "plain",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if repo.created.OwnerUserID == nil || *repo.created.OwnerUserID != auth.UserID {
		t.Fatalf("owner user id = %v, want creator %d", repo.created.OwnerUserID, auth.UserID)
	}
}

func TestUserUsecaseCreateUserKeepsExplicitOwner(t *testing.T) {
	repo := &mockUserRepo{}
	uc := NewUserUsecase(repo)
	auth := Identity{TenantID: "t-1", UserID: 7}
	owner := int64(9)
	dept := int64(3)
	group := int64(5)

	if _, err := uc.CreateUser(context.Background(), auth, &User{
		Name: "alice", Nickname: "Alice", Password: "plain",
		OwnerUserID: &owner, OwnerDeptID: &dept, OwnerUserGroupID: &group,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got := repo.created
	if got.OwnerUserID == nil || *got.OwnerUserID != owner ||
		got.OwnerDeptID == nil || *got.OwnerDeptID != dept ||
		got.OwnerUserGroupID == nil || *got.OwnerUserGroupID != group {
		t.Fatalf("owners = (%v, %v, %v), want (%d, %d, %d)",
			got.OwnerUserID, got.OwnerDeptID, got.OwnerUserGroupID, owner, dept, group)
	}
}

func TestUserUsecaseUpdateMyProfileDefaultsLocaleAndValidatesNickname(t *testing.T) {
	repo := &mockUserRepo{}
	uc := NewUserUsecase(repo)
	auth := Identity{TenantID: "t-1", UserID: 7}

	out, err := uc.UpdateMyProfile(context.Background(), auth, &User{Nickname: "Alice"})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if out.Locale != "zh-CN" || repo.updated.Locale != "zh-CN" {
		t.Fatalf("locale = %q, want zh-CN", out.Locale)
	}
	if _, err := uc.UpdateMyProfile(context.Background(), auth, &User{Nickname: "   "}); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("blank nickname err = %v, want ErrUserInvalidArgument", err)
	}
}

func TestUserUsecaseChangeMyPasswordVerifiesCurrentPassword(t *testing.T) {
	oldHash, err := HashPassword("old-pass")
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	repo := &mockUserRepo{passwordHash: oldHash}
	uc := NewUserUsecase(repo)

	if err := uc.ChangeMyPassword(context.Background(), Identity{TenantID: "t-1", UserID: 7}, "old-pass", "new-pass"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if repo.setPasswordCalls != 1 {
		t.Fatalf("set password calls = %d, want 1", repo.setPasswordCalls)
	}
	ok, _, err := VerifyPassword(repo.setPasswordHash, "new-pass")
	if err != nil || !ok {
		t.Fatalf("stored hash does not verify new password: ok=%v err=%v", ok, err)
	}
}

func TestUserUsecaseChangeMyPasswordRejectsMismatch(t *testing.T) {
	oldHash, err := HashPassword("old-pass")
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	repo := &mockUserRepo{passwordHash: oldHash}
	uc := NewUserUsecase(repo)

	err = uc.ChangeMyPassword(context.Background(), Identity{TenantID: "t-1", UserID: 7}, "wrong-pass", "new-pass")
	if !errors.Is(err, ErrUserPasswordMismatch) {
		t.Fatalf("err = %v, want ErrUserPasswordMismatch", err)
	}
	if repo.setPasswordCalls != 0 {
		t.Fatalf("set password calls = %d, want 0", repo.setPasswordCalls)
	}
}

func TestUserUsecaseChangeMyPasswordRejectsEmptyInput(t *testing.T) {
	repo := &mockUserRepo{}
	uc := NewUserUsecase(repo)

	for _, tc := range []struct{ oldPassword, newPassword string }{
		{"", "new-pass"},
		{"old-pass", ""},
	} {
		if err := uc.ChangeMyPassword(context.Background(), Identity{TenantID: "t-1", UserID: 7}, tc.oldPassword, tc.newPassword); !errors.Is(err, ErrUserInvalidArgument) {
			t.Fatalf("err = %v, want ErrUserInvalidArgument", err)
		}
	}
	if repo.setPasswordCalls != 0 {
		t.Fatalf("set password calls = %d, want 0", repo.setPasswordCalls)
	}
}
