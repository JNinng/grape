package biz

import (
	"context"
	"testing"
)

// mockUserRepo 捕获创建入参：biz 只验证归属缺省编排，不触真实存储。
type mockUserRepo struct {
	UserRepo
	created *User
}

func (m *mockUserRepo) Create(_ context.Context, _ Identity, u *User) (*User, error) {
	m.created = u
	out := *u
	out.ID = 100
	return &out, nil
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
