package service

import (
	"context"
	"errors"
	"testing"

	queryv1 "grape/api/query/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"
)

// fakeAssignmentRepo 捕获分配用例转发到仓储的入参，返回固定的选项/
// 角色集合供 DTO 渲染断言。
type fakeAssignmentRepo struct {
	gotAuth       biz.Identity
	assignableID  int64
	assignableQ   biz.OptionQuery
	userRolesID   int64
	setInput      biz.SetUserRolesInput
	ownerQ        biz.OptionQuery
	ownerCalls    int
	lastOwnerKind string
}

func (f *fakeAssignmentRepo) ListAssignableRoles(_ context.Context, auth biz.Identity, target int64, q biz.OptionQuery) (*biz.OptionSet, error) {
	f.gotAuth = auth
	f.assignableID = target
	f.assignableQ = q
	return &biz.OptionSet{
		Items:   []*biz.Option{{Value: 42, Label: "role-42", Enabled: true}},
		HasMore: true,
		Page:    2,
	}, nil
}

func (f *fakeAssignmentRepo) ListUserRoles(_ context.Context, auth biz.Identity, userID int64) (*biz.UserRoleSet, error) {
	f.gotAuth = auth
	f.userRolesID = userID
	return &biz.UserRoleSet{
		Items:          []*biz.Option{{Value: 7, Label: "r7", Enabled: true, Assigned: true}},
		UnmanagedCount: 3,
		Version:        "v1",
	}, nil
}

func (f *fakeAssignmentRepo) SetUserRoles(_ context.Context, auth biz.Identity, in biz.SetUserRolesInput) (*biz.UserRoleSet, error) {
	f.gotAuth = auth
	f.setInput = in
	return &biz.UserRoleSet{
		Items:          []*biz.Option{{Value: 8, Label: "r8", Enabled: true, Assigned: true}},
		UnmanagedCount: 1,
		Version:        "v2",
	}, nil
}

func (f *fakeAssignmentRepo) ListOwnerUserOptions(_ context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	f.gotAuth = auth
	f.ownerQ = q
	f.ownerCalls++
	f.lastOwnerKind = "user"
	return &biz.OptionSet{Items: []*biz.Option{{Value: 11, Label: "测试管理员", Enabled: true}}, Page: q.Page}, nil
}

func (f *fakeAssignmentRepo) ListOwnerDeptOptions(_ context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	f.gotAuth = auth
	f.ownerQ = q
	f.ownerCalls++
	f.lastOwnerKind = "dept"
	return &biz.OptionSet{Items: []*biz.Option{{Value: 2, Label: "技术部", Enabled: true}}, Page: q.Page}, nil
}

func (f *fakeAssignmentRepo) ListOwnerGroupOptions(_ context.Context, auth biz.Identity, q biz.OptionQuery) (*biz.OptionSet, error) {
	f.gotAuth = auth
	f.ownerQ = q
	f.ownerCalls++
	f.lastOwnerKind = "group"
	return &biz.OptionSet{Items: []*biz.Option{{Value: 1, Label: "核心开发组", Enabled: true}}, Page: q.Page}, nil
}

func newAssignmentService(repo biz.AssignmentRepo) *UserService {
	return NewUserService(biz.NewUserUsecase(nil), biz.NewAssignmentUsecase(repo))
}

// TestUserServiceAssignmentFailsClosedWithoutIdentity 校验六个分配端点
// 在身份缺失时按未认证拒绝且不触达仓储。
func TestUserServiceAssignmentFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	svc := newAssignmentService(repo)
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
	}{
		{"list assignable", func() error {
			_, err := svc.ListAssignableRoles(ctx, &userv1.ListAssignableRolesRequest{TargetUserId: 1})
			return err
		}},
		{"list user roles", func() error {
			_, err := svc.ListUserRoles(ctx, &userv1.ListUserRolesRequest{UserId: 1})
			return err
		}},
		{"set user roles", func() error {
			_, err := svc.SetUserRoles(ctx, &userv1.SetUserRolesRequest{UserId: 1, ExpectedVersion: "v"})
			return err
		}},
		{"owner user", func() error { _, err := svc.ListOwnerUserOptions(ctx, &userv1.ListOwnerOptionsRequest{}); return err }},
		{"owner dept", func() error { _, err := svc.ListOwnerDeptOptions(ctx, &userv1.ListOwnerOptionsRequest{}); return err }},
		{"owner group", func() error { _, err := svc.ListOwnerGroupOptions(ctx, &userv1.ListOwnerOptionsRequest{}); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, biz.ErrSessionInvalid) {
				t.Fatalf("err = %v, want ErrSessionInvalid", err)
			}
		})
	}
	if repo.ownerCalls != 0 || repo.assignableID != 0 || repo.userRolesID != 0 {
		t.Fatal("repo must not be called without identity")
	}
}

// TestListAssignableRolesMapsDTO 校验角色候选端点的查询转换与 int64 → 字符串
// 渲染、分页信封透传。
func TestListAssignableRolesMapsDTO(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	svc := newAssignmentService(repo)
	auth := biz.Identity{TenantID: "t-1", UserID: 9}
	ctx := biz.WithIdentity(context.Background(), auth)

	out, err := svc.ListAssignableRoles(ctx, &userv1.ListAssignableRolesRequest{
		TargetUserId: 5,
		Query:        &queryv1.OptionQuery{Keyword: " role ", Page: 2, PageSize: 10},
	})
	if err != nil {
		t.Fatalf("list assignable roles: %v", err)
	}
	if repo.gotAuth != auth || repo.assignableID != 5 {
		t.Fatalf("repo got (%+v, %d)", repo.gotAuth, repo.assignableID)
	}
	if repo.assignableQ != (biz.OptionQuery{Keyword: "role", Page: 2, PageSize: 10}) {
		t.Fatalf("query = %+v", repo.assignableQ)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetValue() != "42" || out.GetItems()[0].GetLabel() != "role-42" {
		t.Fatalf("items = %+v", out.GetItems())
	}
	if !out.GetHasMore() || out.GetPage() != 2 {
		t.Fatalf("envelope = has_more %v page %d", out.GetHasMore(), out.GetPage())
	}
}

// TestListUserRolesMapsDTO 校验当前角色集合的 DTO 渲染：值转字符串、
// assigned 保留、unmanaged_count 与 version 透传。
func TestListUserRolesMapsDTO(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	svc := newAssignmentService(repo)
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 9})

	out, err := svc.ListUserRoles(ctx, &userv1.ListUserRolesRequest{UserId: 7})
	if err != nil {
		t.Fatalf("list user roles: %v", err)
	}
	if repo.userRolesID != 7 {
		t.Fatalf("repo user id = %d, want 7", repo.userRolesID)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetValue() != "7" || !out.GetItems()[0].GetAssigned() {
		t.Fatalf("items = %+v", out.GetItems())
	}
	if out.GetUnmanagedCount() != 3 || out.GetVersion() != "v1" {
		t.Fatalf("set = (%d, %q)", out.GetUnmanagedCount(), out.GetVersion())
	}
}

// TestSetUserRolesMapsInput 校验整组替换端点把请求原样转成用例入参
// （去重排序在用例层），并把返回集合渲染为 DTO。
func TestSetUserRolesMapsInput(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	svc := newAssignmentService(repo)
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 9})

	out, err := svc.SetUserRoles(ctx, &userv1.SetUserRolesRequest{
		UserId: 7, RoleIds: []int64{3, 1, 3}, ExpectedVersion: "v1",
	})
	if err != nil {
		t.Fatalf("set user roles: %v", err)
	}
	want := biz.SetUserRolesInput{UserID: 7, RoleIDs: []int64{1, 3}, ExpectedVersion: "v1"}
	if repo.setInput.UserID != want.UserID || repo.setInput.ExpectedVersion != want.ExpectedVersion {
		t.Fatalf("input = %+v, want %+v", repo.setInput, want)
	}
	if len(repo.setInput.RoleIDs) != 2 || repo.setInput.RoleIDs[0] != 1 || repo.setInput.RoleIDs[1] != 3 {
		t.Fatalf("role ids = %v, want [1 3]", repo.setInput.RoleIDs)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetValue() != "8" {
		t.Fatalf("items = %+v", out.GetItems())
	}
	if out.GetUnmanagedCount() != 1 || out.GetVersion() != "v2" {
		t.Fatalf("set = (%d, %q)", out.GetUnmanagedCount(), out.GetVersion())
	}
}

// TestOwnerOptionsEndpoints 校验三个归属候选端点各自路由到对应来源并
// 归一化分页。
func TestOwnerOptionsEndpoints(t *testing.T) {
	repo := &fakeAssignmentRepo{}
	svc := newAssignmentService(repo)
	auth := biz.Identity{TenantID: "t-1", UserID: 9}
	ctx := biz.WithIdentity(context.Background(), auth)

	userOut, err := svc.ListOwnerUserOptions(ctx, &userv1.ListOwnerOptionsRequest{})
	if err != nil || repo.lastOwnerKind != "user" {
		t.Fatalf("owner user = (%q, %v)", repo.lastOwnerKind, err)
	}
	if userOut.GetItems()[0].GetValue() != "11" || repo.ownerQ.Page != 1 || repo.ownerQ.PageSize != biz.OptionDefaultPageSize {
		t.Fatalf("owner user option = %+v, query = %+v", userOut.GetItems(), repo.ownerQ)
	}

	if _, err := svc.ListOwnerDeptOptions(ctx, &userv1.ListOwnerOptionsRequest{}); err != nil || repo.lastOwnerKind != "dept" {
		t.Fatalf("owner dept = (%q, %v)", repo.lastOwnerKind, err)
	}
	if _, err := svc.ListOwnerGroupOptions(ctx, &userv1.ListOwnerOptionsRequest{}); err != nil || repo.lastOwnerKind != "group" {
		t.Fatalf("owner group = (%q, %v)", repo.lastOwnerKind, err)
	}
	if repo.gotAuth != auth || repo.ownerCalls != 3 {
		t.Fatalf("owner calls = %d, auth = %+v", repo.ownerCalls, repo.gotAuth)
	}
}
