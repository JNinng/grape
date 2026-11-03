package service

import (
	"context"
	"errors"
	"testing"

	orgv1 "grape/api/org/v1"
	queryv1 "grape/api/query/v1"
	"grape/internal/biz"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeDeptRepo 捕获用例转发的部门 DO 与查询入参，供断言转换与身份透传。
type fakeDeptRepo struct {
	biz.DeptRepo

	gotAuth    biz.Identity
	callsCount int

	created  *biz.Dept
	updated  *biz.Dept
	movedID  int64
	movedTo  *int64
	deleted  []int64
	getErr   error
	parentID int64 // GetByID 返回的部门 id
}

func (f *fakeDeptRepo) GetByID(_ context.Context, auth biz.Identity, id int64) (*biz.Dept, error) {
	f.callsCount++
	f.gotAuth = auth
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &biz.Dept{ID: id, ParentID: &f.parentID, Name: "parent", Status: biz.DeptStatusEnabled, OwnerUserID: 1}, nil
}

func (f *fakeDeptRepo) List(_ context.Context, auth biz.Identity, _ *dynq.Query) (*biz.DeptList, error) {
	f.callsCount++
	f.gotAuth = auth
	return &biz.DeptList{Items: []*biz.Dept{{ID: 5, Name: "dept-a", Status: biz.DeptStatusEnabled, OwnerUserID: 1}}, HasMore: true, NextCursor: "cur-1"}, nil
}

func (f *fakeDeptRepo) ListAll(_ context.Context, auth biz.Identity) ([]*biz.Dept, error) {
	f.callsCount++
	f.gotAuth = auth
	p := int64(1)
	return []*biz.Dept{
		{ID: 1, Name: "root", Status: biz.DeptStatusEnabled, OwnerUserID: 1},
		{ID: 2, Name: "child", ParentID: &p, Status: biz.DeptStatusEnabled, OwnerUserID: 1},
		{ID: 3, Name: "other-root", Status: biz.DeptStatusEnabled, OwnerUserID: 1},
	}, nil
}

func (f *fakeDeptRepo) Create(_ context.Context, auth biz.Identity, d *biz.Dept) (*biz.Dept, error) {
	f.callsCount++
	f.gotAuth = auth
	f.created = d
	out := *d
	out.ID = 9
	return &out, nil
}

func (f *fakeDeptRepo) Update(_ context.Context, auth biz.Identity, d *biz.Dept) (*biz.Dept, error) {
	f.callsCount++
	f.gotAuth = auth
	f.updated = d
	return d, nil
}

func (f *fakeDeptRepo) Move(_ context.Context, auth biz.Identity, id int64, parentID *int64) (*biz.Dept, error) {
	f.callsCount++
	f.gotAuth = auth
	f.movedID = id
	f.movedTo = parentID
	return &biz.Dept{ID: id, ParentID: parentID, Name: "moved", Status: biz.DeptStatusEnabled, OwnerUserID: 1}, nil
}

func (f *fakeDeptRepo) SoftDelete(_ context.Context, auth biz.Identity, id int64) error {
	f.callsCount++
	f.gotAuth = auth
	f.deleted = append(f.deleted, id)
	return nil
}

func TestDeptServiceReadsIdentityFromContext(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	auth := biz.Identity{TenantID: "t-1", UserID: 7}

	out, err := svc.GetDept(biz.WithIdentity(context.Background(), auth), &orgv1.GetDeptRequest{Id: 5})
	if err != nil {
		t.Fatalf("get dept: %v", err)
	}
	if out.GetId() != 5 {
		t.Fatalf("dept id = %d, want 5", out.GetId())
	}
	if repo.callsCount != 1 || repo.gotAuth != auth {
		t.Fatalf("usecase auth = %+v (calls %d), want %+v once", repo.gotAuth, repo.callsCount, auth)
	}
}

func TestDeptServiceFailsClosedWithoutIdentity(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))

	_, err := svc.GetDept(context.Background(), &orgv1.GetDeptRequest{Id: 5})
	if !errors.Is(err, biz.ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestDeptServiceCreateDeptMapsOptionalFields(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	parent := int64(3)
	group := int64(4)

	out, err := svc.CreateDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.CreateDeptRequest{
		Dept: &orgv1.Dept{Name: "  eng  ", ParentId: &parent, OwnerUserId: 11, OwnerUserGroupId: &group},
	})
	if err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if out.GetId() != 9 {
		t.Fatalf("created id = %d, want 9", out.GetId())
	}
	if repo.created == nil || repo.created.ParentID == nil || *repo.created.ParentID != 3 {
		t.Fatalf("created parent = %+v, want 3", repo.created.ParentID)
	}
	if repo.created.OwnerUserGroupID == nil || *repo.created.OwnerUserGroupID != 4 {
		t.Fatalf("created owner group = %+v, want 4", repo.created.OwnerUserGroupID)
	}
}

func TestDeptServiceCreateDeptWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// optional status 的显式 0 有 presence，必须作为禁用落库而不是并入
	// 「未设置缺省启用」分支。
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	status := int32(biz.DeptStatusDisabled)

	_, err := svc.CreateDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.CreateDeptRequest{
		Dept: &orgv1.Dept{Name: "paused", OwnerUserId: 11, Status: &status},
	})
	if err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if repo.created == nil || repo.created.Status != biz.DeptStatusDisabled {
		t.Fatalf("created status = %+v, want disabled", repo.created)
	}
}

func TestDeptServiceUpdateDeptRequiresStatus(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))

	_, err := svc.UpdateDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.UpdateDeptRequest{
		Dept: &orgv1.Dept{Id: 5, Name: "eng"},
	})
	if !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestDeptServiceUpdateDeptIgnoresParentAndOwner(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	parent := int64(3)
	status := int32(biz.DeptStatusDisabled)

	_, err := svc.UpdateDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.UpdateDeptRequest{
		Dept: &orgv1.Dept{Id: 5, Name: "eng", Status: &status, ParentId: &parent, OwnerUserId: 99},
	})
	if err != nil {
		t.Fatalf("update dept: %v", err)
	}
	if repo.updated.ParentID != nil {
		t.Fatalf("updated parent = %+v, want nil (move goes through MoveDept)", repo.updated.ParentID)
	}
	if repo.updated.OwnerUserID != 0 {
		t.Fatalf("updated owner user = %d, want 0", repo.updated.OwnerUserID)
	}
	if repo.updated.Status != biz.DeptStatusDisabled {
		t.Fatalf("updated status = %d, want disabled", repo.updated.Status)
	}
}

func TestDeptServiceMoveDeptUnsetParentMeansRoot(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))

	out, err := svc.MoveDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.MoveDeptRequest{Id: 5})
	if err != nil {
		t.Fatalf("move dept: %v", err)
	}
	if repo.movedID != 5 || repo.movedTo != nil {
		t.Fatalf("move = (%d, %+v), want (5, nil)", repo.movedID, repo.movedTo)
	}
	if out.ParentId != nil {
		t.Fatalf("moved parent = %+v, want nil", out.ParentId)
	}
}

func TestDeptServiceMoveDeptMapsParentPointer(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	parent := int64(3)

	_, err := svc.MoveDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.MoveDeptRequest{Id: 5, ParentId: &parent})
	if err != nil {
		t.Fatalf("move dept: %v", err)
	}
	if repo.movedTo == nil || *repo.movedTo != 3 {
		t.Fatalf("moved parent = %+v, want 3", repo.movedTo)
	}
}

func TestDeptServiceListDeptsRejectsConflictingPagination(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	page := int32(1)
	after := "cur"

	_, err := svc.ListDepts(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListDeptsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page, After: &after}},
	})
	if !errors.Is(err, biz.ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.callsCount != 0 {
		t.Fatalf("usecase calls = %d, want 0", repo.callsCount)
	}
}

func TestDeptServiceListDeptsRendersPageEnvelope(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	page := int32(2)

	out, err := svc.ListDepts(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListDeptsRequest{
		Query: &queryv1.Query{Pagination: &queryv1.Pagination{Page: &page}},
	})
	if err != nil {
		t.Fatalf("list depts: %v", err)
	}
	if len(out.GetItems()) != 1 || out.GetItems()[0].GetId() != 5 || out.GetItems()[0].GetName() != "dept-a" {
		t.Fatalf("items = %+v, want one dept id 5", out.GetItems())
	}
	if !out.GetHasMore() || out.GetNextCursor() != "cur-1" || out.GetPage() != 2 {
		t.Fatalf("envelope = (has_more %v, cursor %q, page %d), want (true, cur-1, 2)", out.GetHasMore(), out.GetNextCursor(), out.GetPage())
	}
}

func TestDeptServiceListDeptsNormalizesEnumStyleConditions(t *testing.T) {
	// 部门无枚举字段别名，条件值原样透传：int64 过滤值按 JSON 数字到达。
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))
	v := structpb.NewNumberValue(2)

	if _, err := svc.ListDepts(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.ListDeptsRequest{
		Query: &queryv1.Query{Where: []*queryv1.Condition{{Field: "parentId", Op: "eq", Value: v}}},
	}); err != nil {
		t.Fatalf("list depts with parentId filter: %v", err)
	}
	if repo.callsCount != 1 {
		t.Fatalf("usecase calls = %d, want 1", repo.callsCount)
	}
}

func TestDeptServiceGetDeptTreeRendersNestedNodes(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))

	out, err := svc.GetDeptTree(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.GetDeptTreeRequest{})
	if err != nil {
		t.Fatalf("get dept tree: %v", err)
	}
	if out.GetTotal() != 3 || len(out.GetRoots()) != 2 {
		t.Fatalf("tree = (total %d, roots %d), want (3, 2)", out.GetTotal(), len(out.GetRoots()))
	}
	root := out.GetRoots()[0]
	if root.GetDept().GetId() != 1 || len(root.GetChildren()) != 1 || root.GetChildren()[0].GetDept().GetId() != 2 {
		t.Fatalf("tree shape = %+v, want root 1 with child 2", root)
	}
}

func TestDeptServiceDeleteDeptDelegatesId(t *testing.T) {
	repo := &fakeDeptRepo{}
	svc := NewDeptService(biz.NewDeptUsecase(repo))

	if _, err := svc.DeleteDept(biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-1", UserID: 7}), &orgv1.DeleteDeptRequest{Id: 8}); err != nil {
		t.Fatalf("delete dept: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 8 {
		t.Fatalf("deleted = %+v, want [8]", repo.deleted)
	}
}
