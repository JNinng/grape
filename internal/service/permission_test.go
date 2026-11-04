package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	permv1 "grape/api/perm/v1"
	"grape/internal/biz"
)

// fakePermissionRepo 捕获用例转发的身份并返回预设权限码集合。
type fakePermissionServiceRepo struct {
	gotAuth biz.Identity
	codes   []string
	err     error
}

func (f *fakePermissionServiceRepo) HasPermission(context.Context, biz.Identity, string) (bool, error) {
	return false, errors.New("not used by codes endpoint")
}

func (f *fakePermissionServiceRepo) ListCodes(_ context.Context, auth biz.Identity) ([]string, error) {
	f.gotAuth = auth
	if f.err != nil {
		return nil, f.err
	}
	return f.codes, nil
}

// TestPermissionService_reads_identity_and_returns_codes 验证身份取自认
// 证上下文并透传仓储，返回去重升序的权限码集合。
func TestPermissionService_reads_identity_and_returns_codes(t *testing.T) {
	repo := &fakePermissionServiceRepo{codes: []string{"sys:user:create", "sys:user:query"}}
	svc := NewPermissionService(biz.NewPermissionUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetMyPermissions(biz.WithIdentity(context.Background(), auth), &permv1.GetMyPermissionsRequest{})
	if err != nil {
		t.Fatalf("GetMyPermissions: %v", err)
	}
	if repo.gotAuth != auth {
		t.Fatalf("repo got auth %+v, want %+v", repo.gotAuth, auth)
	}
	if want := []string{"sys:user:create", "sys:user:query"}; !reflect.DeepEqual(out.Codes, want) {
		t.Fatalf("codes = %v, want %v", out.Codes, want)
	}
}

// TestPermissionService_returns_empty_slice_when_user_holds_nothing 验证
// 空集合语义：用户不持有任何权限码返回空数组（非 nil），是正常响应。
func TestPermissionService_returns_empty_slice_when_user_holds_nothing(t *testing.T) {
	repo := &fakePermissionServiceRepo{codes: []string{}}
	svc := NewPermissionService(biz.NewPermissionUsecase(repo))

	out, err := svc.GetMyPermissions(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.GetMyPermissionsRequest{})
	if err != nil {
		t.Fatalf("GetMyPermissions: %v", err)
	}
	if out.Codes == nil || len(out.Codes) != 0 {
		t.Fatalf("codes = %v, want empty non-nil slice", out.Codes)
	}
}

// TestPermissionService_fails_closed_without_identity 验证未认证（装配
// 错误或绕过中间件）按 fail-closed 拒绝，仓储零调用。
func TestPermissionService_fails_closed_without_identity(t *testing.T) {
	repo := &fakePermissionServiceRepo{}
	svc := NewPermissionService(biz.NewPermissionUsecase(repo))

	_, err := svc.GetMyPermissions(context.Background(), &permv1.GetMyPermissionsRequest{})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.gotAuth != (biz.Identity{}) {
		t.Fatalf("repo must not be called, got auth %+v", repo.gotAuth)
	}
}

// TestPermissionService_propagates_repo_error 验证仓储错误原样传播。
func TestPermissionService_propagates_repo_error(t *testing.T) {
	repoErr := errors.New("codes lookup failed")
	repo := &fakePermissionServiceRepo{err: repoErr}
	svc := NewPermissionService(biz.NewPermissionUsecase(repo))

	_, err := svc.GetMyPermissions(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &permv1.GetMyPermissionsRequest{})
	if !errors.Is(err, repoErr) {
		t.Fatalf("err = %v, want repo error", err)
	}
}
