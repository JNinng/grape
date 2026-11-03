package service

import (
	"context"
	"errors"
	"testing"
	"time"

	orgv1 "grape/api/org/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// fakeUserGroupRepo 捕获用例转发的用户组 DO 与成员入参，供断言转换与身份透传。
type fakeUserGroupRepo struct {
	biz.UserGroupRepo

	gotAuth    biz.Identity
	callsCount int

	created  *biz.UserGroup
	updated  *biz.UserGroup
	deleted  []int64
	getID    int64
	addedIDs []int64
	added    int64
	rmIDs    []int64
	removed  int64

	memberGroupID  int64
	memberPage     int
	memberPageSize int
	memberList     *biz.UserGroupMemberList
}

func (f *fakeUserGroupRepo) GetByID(_ context.Context, auth biz.Identity, id int64) (*biz.UserGroup, error) {
	f.callsCount++
	f.gotAuth = auth
	f.getID = id
	return &biz.UserGroup{ID: id, Name: "grp", Code: "grp", Status: biz.UserGroupStatusEnabled}, nil
}

func (f *fakeUserGroupRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.UserGroupList, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.UserGroupList{Items: []*biz.UserGroup{{ID: 5, Name: "grp-a", Code: "grp-a", Status: biz.UserGroupStatusEnabled}}, HasMore: true, NextCursor: "cur-1"}, nil
}

func (f *fakeUserGroupRepo) Create(_ context.Context, auth biz.Identity, g *biz.UserGroup) (*biz.UserGroup, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = g
	out := *g
	out.ID = 9
	return &out, nil
}

func (f *fakeUserGroupRepo) Update(_ context.Context, auth biz.Identity, g *biz.UserGroup) (*biz.UserGroup, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = g
	return g, nil
}

func (f *fakeUserGroupRepo) SoftDelete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeUserGroupRepo) ListMembers(_ context.Context, auth biz.Identity, groupID int64, page, pageSize int) (*biz.UserGroupMemberList, error) {
	f.callsCount++
	f.gotAuth = auth
	f.memberGroupID = groupID
	f.memberPage = page
	f.memberPageSize = pageSize
	return f.memberList, nil
}

func (f *fakeUserGroupRepo) AddMembers(_ context.Context, auth biz.Identity, _ int64, userIDs []int64) (int64, error) {
	f.callsCount++
	f.gotAuth = auth
	f.addedIDs = userIDs
	return f.added, nil
}

func (f *fakeUserGroupRepo) RemoveMembers(_ context.Context, auth biz.Identity, _ int64, userIDs []int64) (int64, error) {
	f.callsCount++
	f.gotAuth = auth
	f.rmIDs = userIDs
	return f.removed, nil
}

func TestUserGroupServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetUserGroup(biz.WithIdentity(context.Background(), auth), &orgv1.GetUserGroupRequest{Id: 5})
	if err != nil {
		t.Fatalf("get user group: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("user group id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestUserGroupServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	_, err := svc.GetUserGroup(context.Background(), &orgv1.GetUserGroupRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestUserGroupServiceCreateUserGroupMapsOptionalFields(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	remark := "core team"
	owner := int64(11)

	out, err := svc.CreateUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.CreateUserGroupRequest{
		Group: &orgv1.UserGroup{Name: "  grp  ", Code: "grp-1", Remark: &remark, OwnerUserId: &owner},
	})
	if err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if out.GetId() != 9 {
		t.Fatalf("created id = %d, want 9", out.GetId())
	}
	if repo.created == nil || repo.created.OwnerUserID == nil || *repo.created.OwnerUserID != 11 {
		t.Fatalf("created owner = %+v, want 11", repo.created.OwnerUserID)
	}
	if repo.created.Remark == nil || *repo.created.Remark != "core team" {
		t.Fatalf("created remark = %+v, want core team", repo.created.Remark)
	}
}

func TestUserGroupServiceCreateUserGroupNormalizesBlankRemarkToNull(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	blank := "   "

	_, err := svc.CreateUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.CreateUserGroupRequest{
		Group: &orgv1.UserGroup{Name: "grp", Code: "grp", Remark: &blank},
	})
	if err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if repo.created.Remark != nil {
		t.Fatalf("created remark = %+v, want nil (blank normalized)", repo.created.Remark)
	}
}

func TestUserGroupServiceCreateUserGroupWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// optional status 的显式 0 有 presence，必须作为禁用落库而不是并入
	// 「未设置缺省启用」分支。
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	status := int32(biz.UserGroupStatusDisabled)

	_, err := svc.CreateUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.CreateUserGroupRequest{
		Group: &orgv1.UserGroup{Name: "paused", Code: "paused", Status: &status},
	})
	if err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.UserGroupStatusDisabled {
		t.Fatalf("created status = %+v, want disabled", repo.created)
	}
}

func TestUserGroupServiceUpdateUserGroupRequiresStatus(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	_, err := svc.UpdateUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.UpdateUserGroupRequest{
		Group: &orgv1.UserGroup{Id: 5, Name: "grp"},
	})
	if !errors.Is(err, biz.ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestUserGroupServiceUpdateUserGroupIgnoresCodeAndOwner(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	status := int32(biz.UserGroupStatusDisabled)
	owner := int64(99)

	_, err := svc.UpdateUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.UpdateUserGroupRequest{
		Group: &orgv1.UserGroup{Id: 5, Name: "grp-2", Code: "hacked", Status: &status, OwnerUserId: &owner},
	})
	if err != nil {
		t.Fatalf("update user group: %v", err)
	}
	if repo.updated.Code != "" {
		t.Fatalf("updated code = %q, want empty (code is immutable)", repo.updated.Code)
	}
	if repo.updated.OwnerUserID != nil {
		t.Fatalf("updated owner = %+v, want nil (owner changes go elsewhere)", repo.updated.OwnerUserID)
	}
	if repo.updated.Status != biz.UserGroupStatusDisabled {
		t.Fatalf("updated status = %d, want disabled", repo.updated.Status)
	}
}

func TestUserGroupServiceListUserGroupsRejectsConflictingPagination(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	page := int32(1)
	after := "cur"

	_, err := svc.ListUserGroups(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListUserGroupsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestUserGroupServiceListUserGroupsRendersPageEnvelope(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	page := int32(2)

	out, err := svc.ListUserGroups(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListUserGroupsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list user groups: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 || out.GetItems()[0].GetCode() != "grp-a" {
		t.Fatalf("items = %+v, want one group id 5", out.GetItems())
	}
	if !out.GetHasMore() || out.GetNextCursor() != "cur-1" || out.GetPage() != 2 {
		t.Fatalf("envelope = (has_more %v, cursor %q, page %d), want (true, cur-1, 2)", out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestUserGroupServiceListUserGroupMembersForwardsPaging(t *testing.T) {
	// total 61：第 3 页（41-60 行）之后还有第 61 行，has_more 为真。
	repo := &fakeUserGroupRepo{memberList: &biz.UserGroupMemberList{Total: 61}}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	page := int32(3)
	size := int32(20)

	out, err := svc.ListUserGroupMembers(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListUserGroupMembersRequest{Id: 5, Page: page, PageSize: size})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if repo.memberGroupID != 5 || repo.memberPage != 3 || repo.memberPageSize != 20 {
		t.Fatalf("paging = (group %d, page %d, size %d), want (5, 3, 20)", repo.memberGroupID, repo.memberPage, repo.memberPageSize)
	}
	if out.GetTotal() != 61 || !out.GetHasMore() {
		t.Fatalf("envelope = (total %d, has_more %v), want (61, true)", out.GetTotal(), out.GetHasMore())
	}
}

func TestUserGroupServiceListUserGroupMembersLastPageHasNoMore(t *testing.T) {
	repo := &fakeUserGroupRepo{memberList: &biz.UserGroupMemberList{Total: 41}}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))
	page := int32(3)
	size := int32(20)

	out, err := svc.ListUserGroupMembers(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListUserGroupMembersRequest{Id: 5, Page: page, PageSize: size})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if out.GetHasMore() {
		t.Fatalf("has_more = true, want false (41 rows on page 3 of 20)")
	}
}

func TestUserGroupServiceListUserGroupMembersRendersMemberFields(t *testing.T) {
	addedAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	repo := &fakeUserGroupRepo{memberList: &biz.UserGroupMemberList{
		Items: []*biz.UserGroupMember{{UserID: 2, Name: "zhangsan", Nickname: "张三", Status: 1, AddedBy: 7, AddedAt: addedAt}},
		Total: 1,
	}}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	out, err := svc.ListUserGroupMembers(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListUserGroupMembersRequest{Id: 5})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(out.GetItems()) != 1 {
		t.Fatalf("items = %+v, want one member", out.GetItems())
	}
	m := out.GetItems()[0]
	if m.GetUserId() != 2 || m.GetName() != "zhangsan" || m.GetNickname() != "张三" || m.GetStatus() != 1 || m.GetAddedBy() != 7 {
		t.Fatalf("member = %+v, want user 2 zhangsan 张三 added by 7", m)
	}
	if m.GetAddedAt().AsTime() != addedAt {
		t.Fatalf("added_at = %v, want %v", m.GetAddedAt().AsTime(), addedAt)
	}
}

func TestUserGroupServiceAddUserGroupMembersDelegatesIds(t *testing.T) {
	repo := &fakeUserGroupRepo{added: 2}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	out, err := svc.AddUserGroupMembers(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.AddUserGroupMembersRequest{Id: 5, UserIds: []int64{9, 3}})
	if err != nil {
		t.Fatalf("add members: %v", err)
	}
	if out.GetAdded() != 2 {
		t.Fatalf("added = %d, want 2", out.GetAdded())
	}
	// 用例对入参去重排序：仓储收到的是排序后的 ID。
	if !idsEqual(repo.addedIDs, []int64{3, 9}) {
		t.Fatalf("repo ids = %+v, want [3 9]", repo.addedIDs)
	}
}

func TestUserGroupServiceRemoveUserGroupMembersDelegatesIds(t *testing.T) {
	repo := &fakeUserGroupRepo{removed: 1}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	out, err := svc.RemoveUserGroupMembers(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.RemoveUserGroupMembersRequest{Id: 5, UserIds: []int64{3}})
	if err != nil {
		t.Fatalf("remove members: %v", err)
	}
	if out.GetRemoved() != 1 {
		t.Fatalf("removed = %d, want 1", out.GetRemoved())
	}
	if !idsEqual(repo.rmIDs, []int64{3}) {
		t.Fatalf("repo ids = %+v, want [3]", repo.rmIDs)
	}
}

func TestUserGroupServiceDeleteUserGroupDelegatesId(t *testing.T) {
	repo := &fakeUserGroupRepo{}
	svc := NewUserGroupService(biz.NewUserGroupUsecase(repo))

	if _, err := svc.DeleteUserGroup(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.DeleteUserGroupRequest{Id: 8}); err != nil {
		t.Fatalf("delete user group: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 8 {
		t.Fatalf("deleted = %+v, want [8]", repo.deleted)
	}
}

// idsEqual 比较两个 ID 切片是否一致（含 nil 与空集等价）。
func idsEqual(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
