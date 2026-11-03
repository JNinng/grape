package biz

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/org/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrDeptNotFound 表示目标部门不存在（含跨租户不可见：RLS 之外等同不存在）。
	ErrDeptNotFound = kratoserrors.NotFound(v1.ErrorReason_DEPT_NOT_FOUND.String(), "dept not found")
	// ErrDeptInvalidArgument 表示部门请求参数非法（父子引用无效、移入自身子树等）。
	ErrDeptInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_DEPT_INVALID_ARGUMENT.String(), "invalid dept argument")
	// ErrDeptAlreadyExists 表示租户内部门名唯一约束冲突（未删除部门间）。
	ErrDeptAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_DEPT_ALREADY_EXISTS.String(), "dept already exists")
	// ErrDeptHasChildren 表示部门存在未删除的直接子部门，禁止删除（需自叶向根逐层删）。
	ErrDeptHasChildren = kratoserrors.Conflict(v1.ErrorReason_DEPT_HAS_CHILDREN.String(), "dept has active children")
)

// Dept 是部门领域模型。组织树是租户内资源：租户边界由存储层 RLS 承担，
// 表无 @scope（租户内全可见），故仓储方法不携带 Scope；闭包表
// sys_dept_closure 是派生数据，由数据库触发器随增删改自动维护，不进领域
// 模型。parent_id 与 owner_* 属结构/归属列，不经 Update 用例修改。
type Dept struct {
	ID       int64
	Status   int32 // 0 禁用、1 启用
	Name     string
	ParentID *int64 // nil 表示根部门

	OwnerUserID      int64  // 部门管理员用户，非空列
	OwnerUserGroupID *int64 // 可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// DeptList 是部门仓储 List 返回的分页信封。
type DeptList struct {
	Items      []*Dept
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// DeptNode 是组织树节点：部门与其直接子节点。Children 的顺序与租户内
// 平铺列表一致（创建时间升序，同刻按 id 升序）。
type DeptNode struct {
	Dept     *Dept
	Children []*DeptNode
}

// DeptRepo 是部门仓储接口。全部实现必须经存储层会话门面在租户事务内
// 执行（RLS）；闭包路径由触发器在写事务内自动维护。List 消费 dynq 动态
// 查询协议。
type DeptRepo interface {
	// GetByID 按 id 查询单个部门；不存在或跨租户返回 ErrDeptNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*Dept, error)
	// List 按动态查询协议返回分页集合（租户内平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*DeptList, error)
	// ListAll 返回租户内全部未删除部门（树装配与环检测的全量输入；
	// 组织树在租户规模内有限，不做分页）。
	ListAll(ctx context.Context, auth Identity) ([]*Dept, error)
	// Create 创建部门（闭包路径由触发器建立）；父部门不存在/已删除/
	// 跨租户返回 ErrDeptInvalidArgument，部门名冲突映射 ErrDeptAlreadyExists。
	Create(ctx context.Context, auth Identity, d *Dept) (*Dept, error)
	// Update 覆盖可变字段（name、status；不含 parent_id 与 owner_*）。
	Update(ctx context.Context, auth Identity, d *Dept) (*Dept, error)
	// Move 移动部门到新父级（parentID 为 nil 表示移为根部门）；闭包路径
	// 由触发器在同一事务内重写，父级非法/成环由触发器兜底拒绝。
	Move(ctx context.Context, auth Identity, id int64, parentID *int64) (*Dept, error)
	// SoftDelete 软删除部门（deleted_at 置位）；存在未删除直接子部门返回
	// ErrDeptHasChildren（触发器兜底同口径）。
	SoftDelete(ctx context.Context, auth Identity, id int64) error
}

// DeptUsecase 是部门用例。
type DeptUsecase struct {
	repo DeptRepo
}

// NewDeptUsecase 构造部门用例。
func NewDeptUsecase(repo DeptRepo) *DeptUsecase {
	return &DeptUsecase{repo: repo}
}

// GetDept 按 id 查询部门。
func (uc *DeptUsecase) GetDept(ctx context.Context, auth Identity, id int64) (*Dept, error) {
	if id <= 0 {
		return nil, ErrDeptInvalidArgument
	}
	return uc.repo.GetByID(ctx, auth, id)
}

// ListDepts 经动态查询协议列出部门（平铺分页视图）。
func (uc *DeptUsecase) ListDepts(ctx context.Context, auth Identity, q *dynq.Query) (*DeptList, error) {
	return uc.repo.List(ctx, auth, q)
}

// GetDeptTree 返回租户内全部部门的树形结构：parent_id 为空的部门是根，
// 子节点顺序与 ListAll 的平铺顺序一致。父引用缺失的行按根处理（触发器
// 纪律下不会出现，防御性兜底而非新语义）。
func (uc *DeptUsecase) GetDeptTree(ctx context.Context, auth Identity) ([]*DeptNode, int, error) {
	all, err := uc.repo.ListAll(ctx, auth)
	if err != nil {
		return nil, 0, err
	}
	nodes := make(map[int64]*DeptNode, len(all))
	for _, d := range all {
		nodes[d.ID] = &DeptNode{Dept: d}
	}
	roots := make([]*DeptNode, 0)
	for _, d := range all {
		node := nodes[d.ID]
		if d.ParentID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*d.ParentID]
		if !ok {
			// 父节点不在租户未删除集合内（防御性兜底）：按根挂载。
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, len(all), nil
}

// CreateDept 创建部门：status 未指定（哨兵）缺省启用、显式 0 原样落库为
// 禁用，父部门存在性先经仓储判定（引用无效返回 ErrDeptInvalidArgument）；
// 闭包路径由触发器建立。
func (uc *DeptUsecase) CreateDept(ctx context.Context, auth Identity, d *Dept) (*Dept, error) {
	if err := validateDept(d, false); err != nil {
		return nil, err
	}
	if d.ParentID != nil {
		if _, err := uc.repo.GetByID(ctx, auth, *d.ParentID); err != nil {
			if kratoserrors.IsNotFound(err) {
				return nil, ErrDeptInvalidArgument
			}
			return nil, err
		}
	}
	if d.Status == DeptStatusUnspecified {
		d.Status = DeptStatusEnabled
	}
	return uc.repo.Create(ctx, auth, d)
}

// UpdateDept 覆盖可变字段（name、status）。parent_id 与 owner_* 由服务端
// 管理：移动走 MoveDept。
func (uc *DeptUsecase) UpdateDept(ctx context.Context, auth Identity, d *Dept) (*Dept, error) {
	if d == nil || d.ID <= 0 {
		return nil, ErrDeptInvalidArgument
	}
	if err := validateDept(d, true); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, auth, d)
}

// MoveDept 移动部门：校验目标父级存在且不在被移动部门的祖先链上（防环）。
// 先取租户全量部门构建父链做领域校验，再交仓储执行——写事务内触发器是
// 并发窗口的兜底防线（校验与重写同一事务）。
func (uc *DeptUsecase) MoveDept(ctx context.Context, auth Identity, id int64, parentID *int64) (*Dept, error) {
	if id <= 0 {
		return nil, ErrDeptInvalidArgument
	}
	if parentID != nil {
		if *parentID == id {
			return nil, ErrDeptInvalidArgument
		}
		all, err := uc.repo.ListAll(ctx, auth)
		if err != nil {
			return nil, err
		}
		parents := make(map[int64]*int64, len(all))
		for _, d := range all {
			parents[d.ID] = d.ParentID
		}
		if _, ok := parents[*parentID]; !ok {
			// 父部门不存在、已删除或跨租户：引用无效。
			return nil, ErrDeptInvalidArgument
		}
		// 沿新父级的祖先链上溯：命中被移动部门即成环（移入自身子树）。
		cur := *parentID
		for i := 0; cur != 0 && i <= len(all); i++ {
			if cur == id {
				return nil, ErrDeptInvalidArgument
			}
			next := parents[cur]
			if next == nil {
				break
			}
			cur = *next
		}
	}
	return uc.repo.Move(ctx, auth, id, parentID)
}

// DeleteDept 软删除部门；存在未删除直接子部门时仓储返回 ErrDeptHasChildren。
func (uc *DeptUsecase) DeleteDept(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrDeptInvalidArgument
	}
	return uc.repo.SoftDelete(ctx, auth, id)
}

// DeptStatusEnabled / DeptStatusDisabled / DeptStatusUnspecified 与存储层
// sys_dept.status 取值对齐：-1 只是创建入参的「未指定」哨兵（optional
// int32 的缺失必须与显式 0=禁用可区分），不落库；用例把未指定落为缺省
// 启用，显式 0 原样落库为禁用。
const (
	DeptStatusDisabled    int32 = 0
	DeptStatusEnabled     int32 = 1
	DeptStatusUnspecified int32 = -1
)

// deptNameMaxRunes 与 sys_dept.name 列宽（varchar(256)）对齐。
const deptNameMaxRunes = 256

// validateDept 校验部门可变字段：name 必填且不超列宽、owner_user_id 必填
// （创建路径）、status 合法（创建路径额外接受未指定哨兵，缺省由用例落
// 启用；更新路径必填，只认 0/1）。
func validateDept(d *Dept, update bool) error {
	if d == nil || strings.TrimSpace(d.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(d.Name)) > deptNameMaxRunes {
		return ErrDeptInvalidArgument
	}
	switch {
	case update:
		if d.Status != DeptStatusDisabled && d.Status != DeptStatusEnabled {
			return ErrDeptInvalidArgument
		}
	case d.Status != DeptStatusUnspecified &&
		d.Status != DeptStatusDisabled && d.Status != DeptStatusEnabled:
		return ErrDeptInvalidArgument
	}
	if !update && d.OwnerUserID <= 0 {
		return ErrDeptInvalidArgument
	}
	return nil
}
