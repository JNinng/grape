package service

import (
	"context"
	"errors"
	"testing"

	userv1 "grape/api/user/v1"
	"grape/internal/biz"
)

// fakeUserRepo 只为 GetByID 捕获用例收到的身份事实。
type fakeUserRepo struct {
	biz.UserRepo

	gotAuth    biz.Identity
	callsCount int
}

func (f *fakeUserRepo) GetByID(_ context.Context, auth biz.Identity, _ int64) (*biz.User, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.User{ID: 5}, nil
}

func TestUserServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetUser(biz.WithIdentity(context.Background(), auth), &userv1.GetUserRequest{Id: 5})
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("user id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestUserServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo))

	_, err := svc.GetUser(context.Background(), &userv1.GetUserRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}
