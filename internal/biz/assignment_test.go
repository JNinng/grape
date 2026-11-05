package biz

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// fakeAssignmentRepo 捕获用例转发到仓储的入参，并可返回预设结论。
type fakeAssignmentRepo struct {
	assignableTarget int64
	assignableQuery  OptionQuery
	userRolesID      int64
	setInput         SetUserRolesInput
	ownerKind        string
	ownerQuery       OptionQuery
	calls            int
}

func (f *fakeAssignmentRepo) ListAssignableRoles(_ context.Context, _ Identity, target int64, q OptionQuery) (*OptionSet, error) {
	f.calls++
	f.assignableTarget = target
	f.assignableQuery = q
	return &OptionSet{Page: q.Page}, nil
}

func (f *fakeAssignmentRepo) ListUserRoles(_ context.Context, _ Identity, userID int64) (*UserRoleSet, error) {
	f.calls++
	f.userRolesID = userID
	return &UserRoleSet{}, nil
}

func (f *fakeAssignmentRepo) SetUserRoles(_ context.Context, _ Identity, in SetUserRolesInput) (*UserRoleSet, error) {
	f.calls++
	f.setInput = in
	return &UserRoleSet{Version: in.ExpectedVersion}, nil
}

func (f *fakeAssignmentRepo) ListOwnerUserOptions(_ context.Context, _ Identity, q OptionQuery) (*OptionSet, error) {
	f.calls++
	f.ownerKind = "user"
	f.ownerQuery = q
	return &OptionSet{Page: q.Page}, nil
}

func (f *fakeAssignmentRepo) ListOwnerDeptOptions(_ context.Context, _ Identity, q OptionQuery) (*OptionSet, error) {
	f.calls++
	f.ownerKind = "dept"
	f.ownerQuery = q
	return &OptionSet{Page: q.Page}, nil
}

func (f *fakeAssignmentRepo) ListOwnerGroupOptions(_ context.Context, _ Identity, q OptionQuery) (*OptionSet, error) {
	f.calls++
	f.ownerKind = "group"
	f.ownerQuery = q
	return &OptionSet{Page: q.Page}, nil
}

// TestNormalizeOptionQuery 校验选项查询归一化：关键字去空白、页码下限、
// 页大小缺省与上限。
func TestNormalizeOptionQuery(t *testing.T) {
	cases := []struct {
		name string
		in   OptionQuery
		want OptionQuery
	}{
		{"zero", OptionQuery{}, OptionQuery{Page: 1, PageSize: OptionDefaultPageSize}},
		{"negative page", OptionQuery{Page: -3, PageSize: 5}, OptionQuery{Page: 1, PageSize: 5}},
		{"oversize page", OptionQuery{Page: 2, PageSize: OptionMaxPageSize + 1}, OptionQuery{Page: 2, PageSize: OptionMaxPageSize}},
		{"trim keyword", OptionQuery{Keyword: "  abc  ", Page: 2, PageSize: 10}, OptionQuery{Keyword: "abc", Page: 2, PageSize: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeOptionQuery(tc.in); got != tc.want {
				t.Fatalf("normalize(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestUserRoleSetVersion 校验版本 hash 的口径：用户与 ID 集合共同决定、
// 同集合与顺序无关、集合变化即变化。
func TestUserRoleSetVersion(t *testing.T) {
	a := UserRoleSetVersion(7, []int64{3, 1, 2})
	b := UserRoleSetVersion(7, []int64{1, 2, 3, 2})
	if a != b {
		t.Fatalf("version must ignore input order/duplicates: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("version length = %d, want 64 hex chars", len(a))
	}
	if UserRoleSetVersion(8, []int64{1, 2, 3}) == a {
		t.Fatal("version must differ across users")
	}
	if UserRoleSetVersion(7, []int64{1, 2}) == a {
		t.Fatal("version must differ across role sets")
	}
	if UserRoleSetVersion(7, nil) != UserRoleSetVersion(7, []int64{}) {
		t.Fatal("nil and empty role sets must hash identically")
	}
}

// TestAssignmentUsecaseRejectsInvalidInput 校验入参校验在用例层 fail-fast，
// 非法入参不触仓储。
func TestAssignmentUsecaseRejectsInvalidInput(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	uc := NewAssignmentUsecase(repo)
	ctx := context.Background()
	auth := Identity{TenantID: "t-1", UserID: 1}

	if _, err := uc.ListAssignableRoles(ctx, auth, 0, OptionQuery{}); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("zero target err = %v, want ErrUserInvalidArgument", err)
	}
	if _, err := uc.ListUserRoles(ctx, auth, 0); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("zero user err = %v, want ErrUserInvalidArgument", err)
	}
	if _, err := uc.SetUserRoles(ctx, auth, SetUserRolesInput{UserID: 0, ExpectedVersion: "v"}); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("zero user set err = %v, want ErrUserInvalidArgument", err)
	}
	if _, err := uc.SetUserRoles(ctx, auth, SetUserRolesInput{UserID: 1, RoleIDs: []int64{1}, ExpectedVersion: "  "}); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("empty version err = %v, want ErrUserInvalidArgument", err)
	}
	if _, err := uc.SetUserRoles(ctx, auth, SetUserRolesInput{UserID: 1, RoleIDs: []int64{0}, ExpectedVersion: "v"}); !errors.Is(err, ErrUserInvalidArgument) {
		t.Fatalf("non-positive role err = %v, want ErrUserInvalidArgument", err)
	}
	if repo.calls != 0 {
		t.Fatalf("repo calls = %d, want 0 for invalid input", repo.calls)
	}
}

// TestAssignmentUsecaseForwardsNormalized 校验合法入参的归一化与转发：
// 角色 ID 去重升序、选项查询归一化、身份不变。
func TestAssignmentUsecaseForwardsNormalized(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	uc := NewAssignmentUsecase(repo)
	ctx := context.Background()
	auth := Identity{TenantID: "t-1", UserID: 9}

	if _, err := uc.ListAssignableRoles(ctx, auth, 5, OptionQuery{Keyword: " k ", Page: 0, PageSize: 999}); err != nil {
		t.Fatalf("list assignable: %v", err)
	}
	if repo.assignableTarget != 5 || repo.assignableQuery != (OptionQuery{Keyword: "k", Page: 1, PageSize: OptionMaxPageSize}) {
		t.Fatalf("assignable forward = (%d, %+v)", repo.assignableTarget, repo.assignableQuery)
	}

	out, err := uc.SetUserRoles(ctx, auth, SetUserRolesInput{
		UserID: 5, RoleIDs: []int64{9, 3, 9, 1}, ExpectedVersion: "ver",
	})
	if err != nil {
		t.Fatalf("set roles: %v", err)
	}
	if out.Version != "ver" {
		t.Fatalf("version = %q, want ver", out.Version)
	}
	if !slices.Equal(repo.setInput.RoleIDs, []int64{1, 3, 9}) {
		t.Fatalf("role ids = %v, want deduped ascending", repo.setInput.RoleIDs)
	}
	if repo.setInput.UserID != 5 || repo.setInput.ExpectedVersion != "ver" {
		t.Fatalf("set input = %+v", repo.setInput)
	}
}

// TestAssignmentUsecaseOwnerOptionsForward 校验三个归属候选端点各自转发
// 到对应仓储方法并归一化查询。
func TestAssignmentUsecaseOwnerOptionsForward(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	uc := NewAssignmentUsecase(repo)
	ctx := context.Background()
	auth := Identity{TenantID: "t-1", UserID: 9}
	want := OptionQuery{Page: 1, PageSize: OptionDefaultPageSize}

	if _, err := uc.ListOwnerUserOptions(ctx, auth, OptionQuery{}); err != nil || repo.ownerKind != "user" {
		t.Fatalf("owner user forward = (%q, %v)", repo.ownerKind, err)
	}
	if repo.ownerQuery != want {
		t.Fatalf("owner query = %+v, want %+v", repo.ownerQuery, want)
	}
	if _, err := uc.ListOwnerDeptOptions(ctx, auth, OptionQuery{}); err != nil || repo.ownerKind != "dept" {
		t.Fatalf("owner dept forward = (%q, %v)", repo.ownerKind, err)
	}
	if _, err := uc.ListOwnerGroupOptions(ctx, auth, OptionQuery{}); err != nil || repo.ownerKind != "group" {
		t.Fatalf("owner group forward = (%q, %v)", repo.ownerKind, err)
	}
}

// TestUserRoleSetChangedIsConflict 校验版本冲突错误的语义：reason 为
// USER_ROLE_SET_CHANGED、code 为 409。
func TestUserRoleSetChangedIsConflict(t *testing.T) {
	if got := ErrUserRoleSetChanged.Code; got != 409 {
		t.Fatalf("code = %d, want 409", got)
	}
	if ErrUserRoleSetChanged.Reason != "USER_ROLE_SET_CHANGED" {
		t.Fatalf("reason = %q, want USER_ROLE_SET_CHANGED", ErrUserRoleSetChanged.Reason)
	}
}
