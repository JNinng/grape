package biz

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockUserGroupRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与
// 领域校验，不触真实存储。
type mockUserGroupRepo struct {
	UserGroupRepo

	getErr  error
	listed  bool
	created *UserGroup
	updated *UserGroup
	deleted []int64

	memberGroupID  int64
	memberPage     int
	memberPageSize int
	memberList     *UserGroupMemberList
	addedGroupID   int64
	addedIDs       []int64
	addedCount     int64
	addErr         error
	removedGroupID int64
	removedIDs     []int64
	removedCount   int64
	removeErr      error
}

func (m *mockUserGroupRepo) GetByID(_ context.Context, _ Identity, id int64) (*UserGroup, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &UserGroup{ID: id, Status: UserGroupStatusEnabled, Name: "grp", Code: "grp"}, nil
}

func (m *mockUserGroupRepo) List(context.Context, Identity, *dynq.Query) (*UserGroupList, error) {
	m.listed = true
	return &UserGroupList{}, nil
}

func (m *mockUserGroupRepo) Create(_ context.Context, _ Identity, g *UserGroup) (*UserGroup, error) {
	m.created = g
	out := *g
	out.ID = 100
	return &out, nil
}

func (m *mockUserGroupRepo) Update(_ context.Context, _ Identity, g *UserGroup) (*UserGroup, error) {
	m.updated = g
	return g, nil
}

func (m *mockUserGroupRepo) SoftDelete(_ context.Context, _ Identity, id int64) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func (m *mockUserGroupRepo) ListMembers(_ context.Context, _ Identity, groupID int64, page, pageSize int) (*UserGroupMemberList, error) {
	m.memberGroupID = groupID
	m.memberPage = page
	m.memberPageSize = pageSize
	return m.memberList, nil
}

func (m *mockUserGroupRepo) AddMembers(_ context.Context, _ Identity, groupID int64, userIDs []int64) (int64, error) {
	m.addedGroupID = groupID
	m.addedIDs = userIDs
	if m.addErr != nil {
		return 0, m.addErr
	}
	return m.addedCount, nil
}

func (m *mockUserGroupRepo) RemoveMembers(_ context.Context, _ Identity, groupID int64, userIDs []int64) (int64, error) {
	m.removedGroupID = groupID
	m.removedIDs = userIDs
	if m.removeErr != nil {
		return 0, m.removeErr
	}
	return m.removedCount, nil
}

func TestUserGroupUsecaseCreateUserGroupDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	out, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "grp-1", Status: UserGroupStatusUnspecified})
	if err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if out.Status != UserGroupStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.Code != "grp-1" {
		t.Fatalf("repo created = %+v, want code grp-1", repo.created)
	}
}

func TestUserGroupUsecaseCreateUserGroupWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	out, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "paused", Code: "paused", Status: UserGroupStatusDisabled})
	if err != nil {
		t.Fatalf("create user group: %v", err)
	}
	if repo.created == nil || repo.created.Status != UserGroupStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
	if out.Status != UserGroupStatusDisabled {
		t.Fatalf("out status = %d, want disabled", out.Status)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsBlankName(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "   ", Code: "grp-1"})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsBlankCode(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "  "})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsOverlongName(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: strings.Repeat("组", 257), Code: "grp"})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsOverlongCode(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: strings.Repeat("c", 65)})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsOverlongRemark(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)
	remark := strings.Repeat("r", 513)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "grp", Remark: &remark})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsInvalidStatus(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "grp", Status: 2})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseCreateUserGroupRejectsNonPositiveOwnerUser(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)
	owner := int64(0)

	_, err := uc.CreateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "grp", OwnerUserID: &owner})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestUserGroupUsecaseUpdateUserGroupRejectsMissingId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.UpdateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{Name: "grp", Code: "grp", Status: UserGroupStatusEnabled})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo updated = %+v, want nil", repo.updated)
	}
}

func TestUserGroupUsecaseUpdateUserGroupRejectsInvalidStatus(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.UpdateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{ID: 5, Name: "grp", Code: "grp", Status: 9})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseUpdateUserGroupForwardsMutableFieldsOnly(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)
	remark := "updated"

	out, err := uc.UpdateUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &UserGroup{ID: 5, Name: "grp-2", Code: "grp", Status: UserGroupStatusDisabled, Remark: &remark})
	if err != nil {
		t.Fatalf("update user group: %v", err)
	}
	if repo.updated == nil || repo.updated.ID != 5 || repo.updated.Name != "grp-2" || repo.updated.Status != UserGroupStatusDisabled || repo.updated.Remark == nil || *repo.updated.Remark != "updated" {
		t.Fatalf("repo updated = %+v, want id 5 name grp-2 disabled remark updated", repo.updated)
	}
	if out.ID != 5 {
		t.Fatalf("out id = %d, want 5", out.ID)
	}
}

func TestUserGroupUsecaseGetUserGroupRejectsMissingId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.GetUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseDeleteUserGroupRejectsMissingId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	err := uc.DeleteUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("deleted = %+v, want empty", repo.deleted)
	}
}

func TestUserGroupUsecaseDeleteUserGroupForwardsId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	if err := uc.DeleteUserGroup(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 7); err != nil {
		t.Fatalf("delete user group: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 7 {
		t.Fatalf("deleted = %+v, want [7]", repo.deleted)
	}
}

func TestUserGroupUsecaseListUserGroupMembersRejectsMissingGroupId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.ListUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, 1, 20)
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseListUserGroupMembersNormalizesPageAndSize(t *testing.T) {
	repo := &mockUserGroupRepo{memberList: &UserGroupMemberList{}}
	uc := NewUserGroupUsecase(repo)

	// 页码小于 1 归一为 1，页大小越界归一到上下限。
	if _, err := uc.ListUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, 0, 0); err != nil {
		t.Fatalf("list members: %v", err)
	}
	if repo.memberGroupID != 5 || repo.memberPage != 1 || repo.memberPageSize != UserGroupMemberPageDefault {
		t.Fatalf("paging = (group %d, page %d, size %d), want (5, 1, %d)", repo.memberGroupID, repo.memberPage, repo.memberPageSize, UserGroupMemberPageDefault)
	}

	if _, err := uc.ListUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, -3, 500); err != nil {
		t.Fatalf("list members oversized: %v", err)
	}
	if repo.memberPage != 1 || repo.memberPageSize != UserGroupMemberPageMax {
		t.Fatalf("paging = (page %d, size %d), want (1, %d)", repo.memberPage, repo.memberPageSize, UserGroupMemberPageMax)
	}
}

func TestUserGroupUsecaseAddUserGroupMembersDedupesAndSortsIds(t *testing.T) {
	repo := &mockUserGroupRepo{addedCount: 2}
	uc := NewUserGroupUsecase(repo)

	added, err := uc.AddUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, []int64{9, 3, 3, 7})
	if err != nil {
		t.Fatalf("add members: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if repo.addedGroupID != 5 || !slices.Equal(repo.addedIDs, []int64{3, 7, 9}) {
		t.Fatalf("repo add = (group %d, ids %v), want (5, [3 7 9])", repo.addedGroupID, repo.addedIDs)
	}
}

func TestUserGroupUsecaseAddUserGroupMembersRejectsEmptyIds(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.AddUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, nil)
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.addedIDs != nil {
		t.Fatalf("repo add ids = %+v, want nil", repo.addedIDs)
	}
}

func TestUserGroupUsecaseAddUserGroupMembersRejectsMissingGroupId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.AddUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, []int64{3})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}

func TestUserGroupUsecaseAddUserGroupMembersPropagatesRepoError(t *testing.T) {
	repo := &mockUserGroupRepo{addErr: ErrUserGroupNotFound}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.AddUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, []int64{3})
	if !errors.Is(err, ErrUserGroupNotFound) {
		t.Fatalf("err = %v, want ErrUserGroupNotFound", err)
	}
}

func TestUserGroupUsecaseRemoveUserGroupMembersDedupesAndSortsIds(t *testing.T) {
	repo := &mockUserGroupRepo{removedCount: 1}
	uc := NewUserGroupUsecase(repo)

	removed, err := uc.RemoveUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, []int64{8, 3, 8})
	if err != nil {
		t.Fatalf("remove members: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if repo.removedGroupID != 5 || !slices.Equal(repo.removedIDs, []int64{3, 8}) {
		t.Fatalf("repo remove = (group %d, ids %v), want (5, [3 8])", repo.removedGroupID, repo.removedIDs)
	}
}

func TestUserGroupUsecaseRemoveUserGroupMembersRejectsEmptyIds(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.RemoveUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 5, nil)
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
	if repo.removedIDs != nil {
		t.Fatalf("repo remove ids = %+v, want nil", repo.removedIDs)
	}
}

func TestUserGroupUsecaseRemoveUserGroupMembersRejectsMissingGroupId(t *testing.T) {
	repo := &mockUserGroupRepo{}
	uc := NewUserGroupUsecase(repo)

	_, err := uc.RemoveUserGroupMembers(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, []int64{3})
	if !errors.Is(err, ErrUserGroupInvalidArgument) {
		t.Fatalf("err = %v, want ErrUserGroupInvalidArgument", err)
	}
}
