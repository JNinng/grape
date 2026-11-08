package service

import (
	"context"
	"errors"
	"testing"

	dictv1 "grape/api/dict/v1"
	orgv1 "grape/api/org/v1"
	permv1 "grape/api/perm/v1"
	userv1 "grape/api/user/v1"
	"grape/internal/biz"
)

// 嵌套消息缺失（请求体为 {} 或 {"user":null}）时须返回参数错误，
// 不能因直接访问嵌套字段而 panic。
func TestServiceNestedBody_when_nested_message_missing_returns_invalid_argument(t *testing.T) {
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7})
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{
			name: "user create",
			call: func() error {
				_, err := NewUserService(biz.NewUserUsecase(&fakeUserRepo{}), biz.NewAssignmentUsecase(nil)).
					CreateUser(ctx, &userv1.CreateUserRequest{})
				return err
			},
			want: biz.ErrUserInvalidArgument,
		},
		{
			name: "user update",
			call: func() error {
				_, err := NewUserService(biz.NewUserUsecase(&fakeUserRepo{}), biz.NewAssignmentUsecase(nil)).
					UpdateUser(ctx, &userv1.UpdateUserRequest{})
				return err
			},
			want: biz.ErrUserInvalidArgument,
		},
		{
			name: "dept create",
			call: func() error {
				_, err := NewDeptService(biz.NewDeptUsecase(&fakeDeptRepo{})).
					CreateDept(ctx, &orgv1.CreateDeptRequest{})
				return err
			},
			want: biz.ErrDeptInvalidArgument,
		},
		{
			name: "dept update",
			call: func() error {
				_, err := NewDeptService(biz.NewDeptUsecase(&fakeDeptRepo{})).
					UpdateDept(ctx, &orgv1.UpdateDeptRequest{})
				return err
			},
			want: biz.ErrDeptInvalidArgument,
		},
		{
			name: "user group create",
			call: func() error {
				_, err := NewUserGroupService(biz.NewUserGroupUsecase(&fakeUserGroupRepo{})).
					CreateUserGroup(ctx, &orgv1.CreateUserGroupRequest{})
				return err
			},
			want: biz.ErrUserGroupInvalidArgument,
		},
		{
			name: "user group update",
			call: func() error {
				_, err := NewUserGroupService(biz.NewUserGroupUsecase(&fakeUserGroupRepo{})).
					UpdateUserGroup(ctx, &orgv1.UpdateUserGroupRequest{})
				return err
			},
			want: biz.ErrUserGroupInvalidArgument,
		},
		{
			name: "role create",
			call: func() error {
				_, err := NewRoleService(biz.NewRoleUsecase(&fakeRoleRepo{})).
					CreateRole(ctx, &permv1.CreateRoleRequest{})
				return err
			},
			want: biz.ErrRoleInvalidArgument,
		},
		{
			name: "role update",
			call: func() error {
				_, err := NewRoleService(biz.NewRoleUsecase(&fakeRoleRepo{})).
					UpdateRole(ctx, &permv1.UpdateRoleRequest{})
				return err
			},
			want: biz.ErrRoleInvalidArgument,
		},
		{
			name: "menu create",
			call: func() error {
				_, err := NewMenuService(biz.NewMenuUsecase(&fakeMenuRepo{})).
					CreateMenuSuper(ctx, &permv1.CreateMenuSuperRequest{})
				return err
			},
			want: biz.ErrMenuInvalidArgument,
		},
		{
			name: "menu update",
			call: func() error {
				_, err := NewMenuService(biz.NewMenuUsecase(&fakeMenuRepo{})).
					UpdateMenuSuper(ctx, &permv1.UpdateMenuSuperRequest{})
				return err
			},
			want: biz.ErrMenuInvalidArgument,
		},
		{
			name: "dict type create",
			call: func() error {
				_, err := NewDictService(biz.NewDictUsecase(&fakeDictRepo{})).
					CreateDictType(ctx, &dictv1.CreateDictTypeRequest{})
				return err
			},
			want: biz.ErrDictTypeInvalidArgument,
		},
		{
			name: "dict type update",
			call: func() error {
				_, err := NewDictService(biz.NewDictUsecase(&fakeDictRepo{})).
					UpdateDictType(ctx, &dictv1.UpdateDictTypeRequest{})
				return err
			},
			want: biz.ErrDictTypeInvalidArgument,
		},
		{
			name: "dict data create",
			call: func() error {
				_, err := NewDictService(biz.NewDictUsecase(&fakeDictRepo{})).
					CreateDictData(ctx, &dictv1.CreateDictDataRequest{})
				return err
			},
			want: biz.ErrDictDataInvalidArgument,
		},
		{
			name: "dict data update",
			call: func() error {
				_, err := NewDictService(biz.NewDictUsecase(&fakeDictRepo{})).
					UpdateDictData(ctx, &dictv1.UpdateDictDataRequest{})
				return err
			},
			want: biz.ErrDictDataInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
