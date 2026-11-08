package biz

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/perm/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrRoleNotFound 表示目标角色不存在（含跨租户不可见与软删后不可见：
	// RLS 之外等同不存在）。
	ErrRoleNotFound = kratoserrors.NotFound(v1.ErrorReason_ROLE_NOT_FOUND.String(), "role not found")
	// ErrRoleInvalidArgument 表示角色请求参数非法（数据范围取值越界、明细
	// 引用未知用户/部门/用户组/菜单等）。
	ErrRoleInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_ROLE_INVALID_ARGUMENT.String(), "invalid role argument")
	// ErrRoleAlreadyExists 表示租户内角色编码/角色名唯一约束冲突（未删除角色间）。
	ErrRoleAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_ROLE_ALREADY_EXISTS.String(), "role already exists")
	// ErrRolePresetUndeletable 表示系统预置角色（role_type=1）不可删除。
	ErrRolePresetUndeletable = kratoserrors.Forbidden(v1.ErrorReason_ROLE_PRESET_UNDELETABLE.String(), "preset role cannot be deleted")
)

// 角色数据状态：-1 只是创建入参的「未指定」哨兵（optional int32 的缺失
// 必须与显式 0=禁用可区分），不落库；用例把未指定落为缺省启用，显式 0
// 原样落库为禁用。停用的角色失去分配资格与数据范围推导效力（判定函数
// 按 status=1 过滤），已分配用户的映射残留但不生效。
const (
	RoleStatusDisabled    int32 = 0
	RoleStatusEnabled     int32 = 1
	RoleStatusUnspecified int32 = -1
)

// 角色类型：系统预置角色不可删除（删除面拒绝），租户自定义可自由管理；
// API 创建的角色一律为租户自定义。
const (
	RoleTypePreset int32 = 1
	RoleTypeCustom int32 = 2
)

// 角色数据范围规则（sys_role.data_scope 1-7），与存储层判定函数的消费
// 口径对齐：4/5/6 分别只消费 sys_role_id_scope 中 type=1/2/3 的明细行，
// 其余取值下明细行为惰性配置；allowed_dept 只参与分配判定。
const (
	RoleDataScopeSelf        int32 = 1 // 仅本人
	RoleDataScopeDept        int32 = 2 // 本部门
	RoleDataScopeDeptSubtree int32 = 3 // 本部门及子部门
	RoleDataScopeUsers       int32 = 4 // 指定人（消费 type=1 明细）
	RoleDataScopeDepts       int32 = 5 // 指定部门（消费 type=2 明细）
	RoleDataScopeGroups      int32 = 6 // 用户组（消费 type=3 明细）
	RoleDataScopeAll         int32 = 7 // 全租户
)

// 数据范围明细类型（sys_role_id_scope.type）：指向用户/部门/用户组。
const (
	RoleIDScopeTypeUser  int32 = 1
	RoleIDScopeTypeDept  int32 = 2
	RoleIDScopeTypeGroup int32 = 3
)

// Role 是角色领域模型。角色是租户内资源：租户边界由存储层 RLS 承担，
// 行级可见性由表级 @scope（动态 List/Count）与 fn_can_see_role（详情）
// 承担。code 与 role_type 是身份列（创建后不变）；data_scope 与
// scope_dept_id 归数据范围配置面维护，不经 Update 用例修改。
type Role struct {
	ID          int64
	Status      int32 // 0 禁用、1 启用
	Name        string
	Code        string
	RoleType    int32   // 1 系统预置、2 租户自定义
	DataScope   int32   // 1-7
	ScopeDeptID *int64  // 可空：NULL=全租户可分配
	Remark      *string // 可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// RoleList 是角色仓储 List 返回的分页信封。
type RoleList struct {
	Items      []*Role
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// RoleIDScopeEntry 是数据范围明细条目：Type 决定 DataID 的指向
// （1 用户、2 部门、3 用户组）。
type RoleIDScopeEntry struct {
	Type   int32
	DataID int64
}

// RoleDataScope 是角色的数据范围配置视图：角色行上的 data_scope 与
// scope_dept_id，加上两张明细表（sys_role_id_scope、sys_role_allowed_dept）
// 的活跃行集合。数据范围推导（fn_accessible_*）按 data_scope 消费
// IDScopes；分配判定（fn_can_assign_role）消费 AllowedDeptIDs 与
// scope_dept_id。
type RoleDataScope struct {
	RoleID         int64
	DataScope      int32
	ScopeDeptID    *int64
	IDScopes       []RoleIDScopeEntry
	AllowedDeptIDs []int64
}

// RoleRepo 是角色仓储接口。全部实现必须经存储层会话门面在租户事务内
// 执行（RLS）；写路径先锁角色行再操作明细/映射表。List 消费 dynq 动态
// 查询协议。数据范围明细与角色-菜单映射不单独立资源，随本仓储维护。
type RoleRepo interface {
	// GetByID 按 id 查询单个角色；不存在、跨租户或对操作人不可见
	//（fn_can_see_role）返回 ErrRoleNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*Role, error)
	// List 按动态查询协议返回分页集合（结果按角色可见性 @scope 守卫过滤）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*RoleList, error)
	// Create 创建角色（服务端定 role_type=2、data_scope=1、scope_dept_id
	// 为空）；编码/角色名冲突映射 ErrRoleAlreadyExists。
	Create(ctx context.Context, auth Identity, r *Role) (*Role, error)
	// Update 覆盖可变字段（name、status、remark；不含 code/role_type 与
	// 数据范围配置）。
	Update(ctx context.Context, auth Identity, r *Role) (*Role, error)
	// SoftDelete 软删除角色（deleted_at 置位）。系统预置角色返回
	// ErrRolePresetUndeletable。明细/映射行物理残留：数据范围推导、分配
	// 判定与功能权限判定都以角色表启用且未删除过滤，残留行不产生可见副作用。
	SoftDelete(ctx context.Context, auth Identity, id int64) error
	// GetDataScope 读取角色的数据范围配置（角色行标量 + 两张明细表活跃行）；
	// 角色不存在、跨租户或对操作人不可见返回 ErrRoleNotFound。
	GetDataScope(ctx context.Context, auth Identity, id int64) (*RoleDataScope, error)
	// SetDataScope 整组替换角色的数据范围配置：角色行标量随入参更新，
	// 两张明细表按入参集合差量同步（移除物理删除、补建缺失、既有命中行
	// 保留原审计）。明细与分发部门引用的对象必须存在且启用（跨租户等同
	// 不存在），任一未知返回 ErrRoleInvalidArgument 且整组不写入。返回
	// 写入的配置（入参由用例归一化：去重升序，仓储原样回显）。
	SetDataScope(ctx context.Context, auth Identity, cfg *RoleDataScope) (*RoleDataScope, error)
	// ListMenus 返回角色当前的有效授权菜单 ID 集合（升序；停用/软删菜单
	// 的残留映射不出现在回显集合，与 fn_has_permission 的活跃口径一致，
	// 回显集合可原样作为 SetMenus 输入）；角色不存在、跨租户或对操作人
	// 不可见返回 ErrRoleNotFound。
	ListMenus(ctx context.Context, auth Identity, id int64) ([]int64, error)
	// SetMenus 整组替换角色的菜单授权（差量同步：以映射物理行为对账基线，
	// 不在新集合内的行物理删除——含失活菜单的残留授权，补建缺失并记录
	// 授权人，既有有效授权行保留原授权人/授权时间）。空集合合法（清空
	// 全部授权）。菜单必须存在且启用，任一未知返回 ErrRoleInvalidArgument
	// 且整组不写入。返回设置后的有效授权集合（升序）。
	SetMenus(ctx context.Context, auth Identity, id int64, menuIDs []int64) ([]int64, error)
}

// RoleUsecase 是角色用例。
type RoleUsecase struct {
	repo RoleRepo
}

// NewRoleUsecase 构造角色用例。
func NewRoleUsecase(repo RoleRepo) *RoleUsecase {
	return &RoleUsecase{repo: repo}
}

// GetRole 按 id 查询角色。
func (uc *RoleUsecase) GetRole(ctx context.Context, auth Identity, id int64) (*Role, error) {
	if id <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	return uc.repo.GetByID(ctx, auth, id)
}

// ListRoles 经动态查询协议列出角色（平铺分页视图）。
func (uc *RoleUsecase) ListRoles(ctx context.Context, auth Identity, q *dynq.Query) (*RoleList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateRole 创建角色：status 未指定缺省启用、显式 0 禁用；role_type
// 服务端定为租户自定义（2）；数据范围缺省仅本人、scope_dept_id 为空，
// 由 SetRoleDataScope 配置。
func (uc *RoleUsecase) CreateRole(ctx context.Context, auth Identity, r *Role) (*Role, error) {
	if err := validateRole(r, false); err != nil {
		return nil, err
	}
	if r.Status == RoleStatusUnspecified {
		r.Status = RoleStatusEnabled
	}
	r.RoleType = RoleTypeCustom
	r.DataScope = RoleDataScopeSelf
	r.ScopeDeptID = nil
	return uc.repo.Create(ctx, auth, r)
}

// UpdateRole 覆盖可变字段（name、status、remark）。code 与 role_type 是
// 身份列、数据范围配置归 SetRoleDataScope，均不在本路径变更。
func (uc *RoleUsecase) UpdateRole(ctx context.Context, auth Identity, r *Role) (*Role, error) {
	if r == nil || r.ID <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	if err := validateRole(r, true); err != nil {
		return nil, err
	}
	return uc.repo.Update(ctx, auth, r)
}

// DeleteRole 软删除角色；系统预置角色由仓储在锁定行后拒绝。
func (uc *RoleUsecase) DeleteRole(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrRoleInvalidArgument
	}
	return uc.repo.SoftDelete(ctx, auth, id)
}

// GetRoleDataScope 读取角色的数据范围配置。
func (uc *RoleUsecase) GetRoleDataScope(ctx context.Context, auth Identity, id int64) (*RoleDataScope, error) {
	if id <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	return uc.repo.GetDataScope(ctx, auth, id)
}

// SetRoleDataScope 设置角色的数据范围配置：入参去重归一（明细按
// (type, data_id) 升序、分发部门升序）后整组交给仓储差量同步。
func (uc *RoleUsecase) SetRoleDataScope(ctx context.Context, auth Identity, cfg *RoleDataScope) (*RoleDataScope, error) {
	if cfg == nil || cfg.RoleID <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	if cfg.DataScope < RoleDataScopeSelf || cfg.DataScope > RoleDataScopeAll {
		return nil, ErrRoleInvalidArgument
	}
	if cfg.ScopeDeptID != nil && *cfg.ScopeDeptID <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	for _, e := range cfg.IDScopes {
		if (e.Type < RoleIDScopeTypeUser || e.Type > RoleIDScopeTypeGroup) || e.DataID <= 0 {
			return nil, ErrRoleInvalidArgument
		}
	}
	for _, id := range cfg.AllowedDeptIDs {
		if id <= 0 {
			return nil, ErrRoleInvalidArgument
		}
	}
	cfg.IDScopes = dedupIDScopes(cfg.IDScopes)
	cfg.AllowedDeptIDs = dedupInt64s(cfg.AllowedDeptIDs)
	return uc.repo.SetDataScope(ctx, auth, cfg)
}

// ListRoleMenus 返回角色已授权的菜单 ID 集合。
func (uc *RoleUsecase) ListRoleMenus(ctx context.Context, auth Identity, id int64) ([]int64, error) {
	if id <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	return uc.repo.ListMenus(ctx, auth, id)
}

// SetRoleMenus 设置角色的菜单授权：入参去重排序；空集合合法（清空全部
// 授权），语义幂等由仓储差量同步保证。
func (uc *RoleUsecase) SetRoleMenus(ctx context.Context, auth Identity, id int64, menuIDs []int64) ([]int64, error) {
	if id <= 0 {
		return nil, ErrRoleInvalidArgument
	}
	for _, menuID := range menuIDs {
		if menuID <= 0 {
			return nil, ErrRoleInvalidArgument
		}
	}
	return uc.repo.SetMenus(ctx, auth, id, dedupInt64s(menuIDs))
}

// 角色列宽约束，与 sys_role 的 name/code/remark 列宽对齐。
const (
	roleNameMaxRunes   = 256 // varchar(256)
	roleCodeMaxRunes   = 64  // varchar(64)
	roleRemarkMaxRunes = 512 // varchar(512)
)

// validateRole 校验角色可变字段：name 必填且不超列宽、remark 不超列宽、
// status 合法（创建路径额外接受未指定哨兵，缺省由用例落启用；更新路径
// 必填，只认 0/1）。code 是身份列：仅创建路径必填且不超列宽（更新输入
// 不携带，仓储不写该列）。
func validateRole(r *Role, update bool) error {
	if r == nil ||
		strings.TrimSpace(r.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(r.Name)) > roleNameMaxRunes ||
		(r.Remark != nil && utf8.RuneCountInString(*r.Remark) > roleRemarkMaxRunes) {
		return ErrRoleInvalidArgument
	}
	if !update && (strings.TrimSpace(r.Code) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(r.Code)) > roleCodeMaxRunes) {
		return ErrRoleInvalidArgument
	}
	switch {
	case update:
		if r.Status != RoleStatusDisabled && r.Status != RoleStatusEnabled {
			return ErrRoleInvalidArgument
		}
	case r.Status != RoleStatusUnspecified &&
		r.Status != RoleStatusDisabled && r.Status != RoleStatusEnabled:
		return ErrRoleInvalidArgument
	}
	return nil
}

// dedupIDScopes 数据范围明细去重并按 (type, data_id) 升序，保证存在性
// 判定与差量同步的稳定。
func dedupIDScopes(entries []RoleIDScopeEntry) []RoleIDScopeEntry {
	out := slices.Clone(entries)
	slices.SortFunc(out, func(a, b RoleIDScopeEntry) int {
		if a.Type != b.Type {
			return int(a.Type - b.Type)
		}
		return int(a.DataID - b.DataID)
	})
	return slices.CompactFunc(out, func(a, b RoleIDScopeEntry) bool {
		return a.Type == b.Type && a.DataID == b.DataID
	})
}

// dedupInt64s ID 集合去重并升序。
func dedupInt64s(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}
