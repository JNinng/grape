package biz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// mockDeptRepo 捕获仓储调用并按用例配置返回值：biz 只验证用例编排与领域
// 校验，不触真实存储。
type mockDeptRepo struct {
	DeptRepo

	getByIDErr  error
	getByIDDept *Dept
	all         []*Dept
	allErr      error

	getByIDCalls int
	allCalls     int
	created      *Dept
	updated      *Dept
	movedID      int64
	movedTo      *int64
	movedCalls   int
	deleted      []int64
	listed       bool
}

func (m *mockDeptRepo) GetByID(_ context.Context, _ Identity, id int64) (*Dept, error) {
	m.getByIDCalls++
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	if m.getByIDDept != nil {
		return m.getByIDDept, nil
	}
	return &Dept{ID: id, Status: DeptStatusEnabled, OwnerUserID: 1}, nil
}

func (m *mockDeptRepo) List(context.Context, Identity, *dynq.Query) (*DeptList, error) {
	m.listed = true
	return &DeptList{}, nil
}

func (m *mockDeptRepo) ListAll(_ context.Context, _ Identity) ([]*Dept, error) {
	m.allCalls++
	if m.allErr != nil {
		return nil, m.allErr
	}
	return m.all, nil
}

func (m *mockDeptRepo) Create(_ context.Context, _ Identity, d *Dept) (*Dept, error) {
	m.created = d
	out := *d
	out.ID = 100
	return &out, nil
}

func (m *mockDeptRepo) Update(_ context.Context, _ Identity, d *Dept) (*Dept, error) {
	m.updated = d
	return d, nil
}

func (m *mockDeptRepo) Move(_ context.Context, _ Identity, id int64, parentID *int64) (*Dept, error) {
	m.movedCalls++
	m.movedID = id
	m.movedTo = parentID
	return &Dept{ID: id, ParentID: parentID, Status: DeptStatusEnabled, OwnerUserID: 1}, nil
}

func (m *mockDeptRepo) SoftDelete(_ context.Context, _ Identity, id int64) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func TestDeptUsecaseCreateDeptDefaultsUnspecifiedStatusToEnabled(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	out, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", OwnerUserID: 5, Status: DeptStatusUnspecified})
	if err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if out.Status != DeptStatusEnabled {
		t.Fatalf("status = %d, want enabled", out.Status)
	}
	if repo.created == nil || repo.created.OwnerUserID != 5 {
		t.Fatalf("repo created = %+v, want owner user 5", repo.created)
	}
}

func TestDeptUsecaseCreateDeptWithExplicitDisabledStatusKeepsDisabled(t *testing.T) {
	// 显式 status=0 是合法的禁用取值，不得并入「未指定缺省启用」分支。
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	out, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "paused", OwnerUserID: 5, Status: DeptStatusDisabled})
	if err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if repo.created == nil || repo.created.Status != DeptStatusDisabled {
		t.Fatalf("repo created status = %+v, want disabled", repo.created)
	}
	if out.Status != DeptStatusDisabled {
		t.Fatalf("out status = %d, want disabled", out.Status)
	}
}

func TestDeptUsecaseCreateDeptRejectsBlankName(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "   ", OwnerUserID: 5})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestDeptUsecaseCreateDeptRejectsOverlongName(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: strings.Repeat("部", 257), OwnerUserID: 5})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
}

func TestDeptUsecaseCreateDeptRejectsZeroOwnerUser(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng"})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestDeptUsecaseCreateDeptRejectsInvalidStatus(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", Status: 2, OwnerUserID: 5})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
}

func TestDeptUsecaseCreateDeptRejectsUnknownParent(t *testing.T) {
	repo := &mockDeptRepo{getByIDErr: ErrDeptNotFound}
	uc := NewDeptUsecase(repo)
	parent := int64(42)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", OwnerUserID: 5, ParentID: &parent})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestDeptUsecaseCreateDeptPropagatesParentLookupFailure(t *testing.T) {
	// 父级点查的存储错误原样透传（不吞成参数错误），Create 不被调用。
	repo := &mockDeptRepo{getByIDErr: errors.New("storage down")}
	uc := NewDeptUsecase(repo)
	parent := int64(42)

	_, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", OwnerUserID: 5, ParentID: &parent})
	if err == nil || errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want original storage error", err)
	}
	if repo.created != nil {
		t.Fatalf("repo created = %+v, want nil", repo.created)
	}
}

func TestDeptUsecaseCreateDeptAcceptsKnownParent(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)
	parent := int64(42)

	out, err := uc.CreateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", OwnerUserID: 5, ParentID: &parent})
	if err != nil {
		t.Fatalf("create dept: %v", err)
	}
	if out.ID != 100 {
		t.Fatalf("created id = %d, want 100", out.ID)
	}
}

func TestDeptUsecaseUpdateDeptRejectsMissingId(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.UpdateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{Name: "eng", Status: DeptStatusEnabled})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.updated != nil {
		t.Fatalf("repo updated = %+v, want nil", repo.updated)
	}
}

func TestDeptUsecaseUpdateDeptRejectsInvalidStatus(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.UpdateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{ID: 5, Name: "eng", Status: 9})
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
}

func TestDeptUsecaseUpdateDeptForwardsMutableFieldsOnly(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	out, err := uc.UpdateDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, &Dept{ID: 5, Name: "eng-2", Status: DeptStatusDisabled})
	if err != nil {
		t.Fatalf("update dept: %v", err)
	}
	if repo.updated == nil || repo.updated.ID != 5 || repo.updated.Name != "eng-2" || repo.updated.Status != DeptStatusDisabled {
		t.Fatalf("repo updated = %+v, want id 5 name eng-2 disabled", repo.updated)
	}
	if out.ID != 5 {
		t.Fatalf("out id = %d, want 5", out.ID)
	}
}

// moveTestTree 构建两棵树：1→2→3 与 4（根），供环检测与跨枝移动用例。
func moveTestTree() []*Dept {
	p2 := int64(1)
	p3 := int64(2)
	return []*Dept{
		{ID: 1, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 2, ParentID: &p2, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 3, ParentID: &p3, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 4, Status: DeptStatusEnabled, OwnerUserID: 1},
	}
}

func TestDeptUsecaseMoveDeptRejectsMissingId(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0, nil)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.movedCalls != 0 {
		t.Fatalf("move calls = %d, want 0", repo.movedCalls)
	}
}

func TestDeptUsecaseMoveDeptRejectsSelfParent(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)
	self := int64(2)

	_, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, &self)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.allCalls != 0 || repo.movedCalls != 0 {
		t.Fatalf("calls = (all %d, move %d), want (0, 0)", repo.allCalls, repo.movedCalls)
	}
}

func TestDeptUsecaseMoveDeptRejectsUnknownParent(t *testing.T) {
	repo := &mockDeptRepo{all: moveTestTree()}
	uc := NewDeptUsecase(repo)
	ghost := int64(99)

	_, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, &ghost)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.movedCalls != 0 {
		t.Fatalf("move calls = %d, want 0", repo.movedCalls)
	}
}

func TestDeptUsecaseMoveDeptRejectsMoveIntoOwnSubtree(t *testing.T) {
	repo := &mockDeptRepo{all: moveTestTree()}
	uc := NewDeptUsecase(repo)
	// 根 1 移到其孙节点 3 之下：新父级祖先链含 1，成环。
	descendant := int64(3)

	_, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 1, &descendant)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.movedCalls != 0 {
		t.Fatalf("move calls = %d, want 0", repo.movedCalls)
	}
}

func TestDeptUsecaseMoveDeptAllowsMoveAcrossBranches(t *testing.T) {
	repo := &mockDeptRepo{all: moveTestTree()}
	uc := NewDeptUsecase(repo)
	// 2 移到另一棵树的根 4 之下：合法，交仓储执行。
	target := int64(4)

	out, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, &target)
	if err != nil {
		t.Fatalf("move dept: %v", err)
	}
	if repo.movedCalls != 1 || repo.movedID != 2 || repo.movedTo == nil || *repo.movedTo != 4 {
		t.Fatalf("move = (calls %d, id %d, parent %+v), want (1, 2, 4)", repo.movedCalls, repo.movedID, repo.movedTo)
	}
	if out.ParentID == nil || *out.ParentID != 4 {
		t.Fatalf("out parent = %+v, want 4", out.ParentID)
	}
}

func TestDeptUsecaseMoveDeptToRootSkipsTreeLoad(t *testing.T) {
	repo := &mockDeptRepo{all: moveTestTree()}
	uc := NewDeptUsecase(repo)

	_, err := uc.MoveDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 2, nil)
	if err != nil {
		t.Fatalf("move dept to root: %v", err)
	}
	if repo.allCalls != 0 {
		t.Fatalf("list all calls = %d, want 0 (nothing to validate for root)", repo.allCalls)
	}
	if repo.movedTo != nil {
		t.Fatalf("moved parent = %+v, want nil", repo.movedTo)
	}
}

func TestDeptUsecaseDeleteDeptRejectsMissingId(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	err := uc.DeleteDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("deleted = %+v, want empty", repo.deleted)
	}
}

func TestDeptUsecaseDeleteDeptForwardsId(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	if err := uc.DeleteDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 7); err != nil {
		t.Fatalf("delete dept: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != 7 {
		t.Fatalf("deleted = %+v, want [7]", repo.deleted)
	}
}

func TestDeptUsecaseGetDeptRejectsMissingId(t *testing.T) {
	repo := &mockDeptRepo{}
	uc := NewDeptUsecase(repo)

	_, err := uc.GetDept(context.Background(), Identity{TenantID: "t-1", UserID: 1}, 0)
	if !errors.Is(err, ErrDeptInvalidArgument) {
		t.Fatalf("err = %v, want ErrDeptInvalidArgument", err)
	}
	if repo.getByIDCalls != 0 {
		t.Fatalf("get calls = %d, want 0", repo.getByIDCalls)
	}
}

func TestDeptUsecaseGetDeptTreeAssemblesNestedOrder(t *testing.T) {
	p := int64(1)
	gp := int64(2)
	orphan := int64(8)
	// 树：1 → 2 → 3，另有孤儿 9（父引用缺失，防御性按根挂载）。
	repo := &mockDeptRepo{all: []*Dept{
		{ID: 1, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 2, ParentID: &p, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 3, ParentID: &gp, Status: DeptStatusEnabled, OwnerUserID: 1},
		{ID: 9, ParentID: &orphan, Status: DeptStatusEnabled, OwnerUserID: 1},
	}}
	uc := NewDeptUsecase(repo)

	roots, total, err := uc.GetDeptTree(context.Background(), Identity{TenantID: "t-1", UserID: 1})
	if err != nil {
		t.Fatalf("get dept tree: %v", err)
	}
	if total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
	if len(roots) != 2 || roots[0].Dept.ID != 1 || roots[1].Dept.ID != 9 {
		t.Fatalf("roots = %+v, want [1, 9]", rootIDs(roots))
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Dept.ID != 2 {
		t.Fatalf("root children = %+v, want [2]", rootIDs(roots[0].Children))
	}
	if len(roots[0].Children[0].Children) != 1 || roots[0].Children[0].Children[0].Dept.ID != 3 {
		t.Fatalf("grand children = %+v, want [3]", rootIDs(roots[0].Children[0].Children))
	}
}

func TestDeptUsecaseGetDeptTreePropagatesRepoError(t *testing.T) {
	repo := &mockDeptRepo{allErr: ErrDeptNotFound}
	uc := NewDeptUsecase(repo)

	if _, _, err := uc.GetDeptTree(context.Background(), Identity{TenantID: "t-1", UserID: 1}); !errors.Is(err, ErrDeptNotFound) {
		t.Fatalf("err = %v, want ErrDeptNotFound", err)
	}
}

// rootIDs 提取节点集合的部门 ID，供树形断言输出。
func rootIDs(nodes []*DeptNode) []int64 {
	out := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Dept.ID)
	}
	return out
}
