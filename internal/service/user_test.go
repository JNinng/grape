package service

import (
	"context"
	"errors"
	"testing"

	userv1 "grape/api/user/v1"
	"grape/internal/biz"
)

// fakeUserRepo 捕获用例收到的身份与入参事实；其余经内嵌接口兜底。
type fakeUserRepo struct {
	biz.UserRepo

	gotAuth    biz.Identity
	callsCount int
	updated    *biz.User

	passwordHash    string
	setPasswordHash string
}

func (f *fakeUserRepo) GetByID(_ context.Context, auth biz.Identity, _ int64) (*biz.User, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.User{ID: 5}, nil
}

func (f *fakeUserRepo) GetMyProfile(_ context.Context, auth biz.Identity) (*biz.User, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.User{ID: auth.UserID, Name: "alice", Nickname: "Alice", Locale: "zh-CN"}, nil
}

func (f *fakeUserRepo) UpdateMyProfile(_ context.Context, auth biz.Identity, u *biz.User) (*biz.User, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = u
	out := *u
	out.ID = auth.UserID
	return &out, nil
}

func (f *fakeUserRepo) GetMyPasswordHash(_ context.Context, auth biz.Identity) (string, error) {
	f.gotAuth = auth
	return f.passwordHash, nil
}

func (f *fakeUserRepo) SetMyPassword(_ context.Context, auth biz.Identity, hashedPassword string) error {
	f.gotAuth = auth
	f.setPasswordHash = hashedPassword
	return nil
}

func TestUserServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo), biz.NewAssignmentUsecase(nil))
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

func TestUserServiceUpdateMyProfileMapsEditableFields(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo), biz.NewAssignmentUsecase(nil))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}
	email := "alice@example.com"

	out, err := svc.UpdateMyProfile(biz.WithIdentity(context.Background(), auth), &userv1.UpdateMyProfileRequest{
		Nickname: "Alice",
		Email:    &email,
		Locale:   " zh-CN ",
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if out.GetId() != auth.UserID || out.GetEmail() != email || out.GetLocale() != "zh-CN" {
		t.Fatalf("profile = %+v, want id %d email %q locale zh-CN", out, auth.UserID, email)
	}
	if repo.updated == nil || repo.updated.Phone != nil || repo.updated.Remark != nil {
		t.Fatalf("blank optional fields must clear: %+v", repo.updated)
	}
}

func TestUserServiceChangeMyPasswordRejectsMismatch(t *testing.T) {
	oldHash, err := biz.HashPassword("old-pass")
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	repo := &fakeUserRepo{passwordHash: oldHash}
	svc := NewUserService(biz.NewUserUsecase(repo), biz.NewAssignmentUsecase(nil))

	_, err = svc.ChangeMyPassword(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}),
		&userv1.ChangeMyPasswordRequest{OldPassword: "wrong-pass", NewPassword: "new-pass"})
	if !errors.Is(err, biz.ErrUserPasswordMismatch) {
		t.Fatalf("err = %v, want ErrUserPasswordMismatch", err)
	}
	if repo.setPasswordHash != "" {
		t.Fatal("password must not be written on mismatch")
	}
}

func TestUserServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeUserRepo{}
	svc := NewUserService(biz.NewUserUsecase(repo), biz.NewAssignmentUsecase(nil))

	_, err := svc.GetUser(context.Background(), &userv1.GetUserRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}
