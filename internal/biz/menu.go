package biz

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/perm/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

var (
	// ErrMenuNotFound 表示目标菜单不存在（含跨租户不可见与软删后不可见）。
	ErrMenuNotFound = kratoserrors.NotFound(v1.ErrorReason_MENU_NOT_FOUND.String(), "menu not found")
	// ErrMenuInvalidArgument 表示菜单请求参数非法（名称/权限码缺失或超长、
	// 菜单类型越界、父级引用无效、移入自身子树等）。
	ErrMenuInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_MENU_INVALID_ARGUMENT.String(), "invalid menu argument")
	// ErrMenuAlreadyExists 表示租户内权限编码唯一约束冲突（未删除菜单间）。
	ErrMenuAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_MENU_ALREADY_EXISTS.String(), "menu already exists")
	// ErrMenuHasChildren 表示菜单存在未删除的直接子菜单，禁止删除
	//（需自叶向根逐层删）。
	ErrMenuHasChildren = kratoserrors.Conflict(v1.ErrorReason_MENU_HAS_CHILDREN.String(), "menu has active children")
)

// 菜单数据状态：-1 只是创建入参的「未指定」哨兵（optional int32 的缺失
// 必须与显式 0=禁用可区分），不落库；用例把未指定落为缺省启用，显式 0
// 原样落库为禁用。停用的菜单不再产生功能权限（判定函数按 status=1
// 过滤），角色授权映射残留但不生效。
const (
	MenuStatusDisabled    int32 = 0
	MenuStatusEnabled     int32 = 1
	MenuStatusUnspecified int32 = -1
)

// 菜单类型（sys_menu.menu_type）：目录是分组节点，菜单是可导航页面，
// 按钮是操作权限点（功能权限码的主要载体）。
const (
	MenuTypeDirectory int32 = 1
	MenuTypePage      int32 = 2
	MenuTypeButton    int32 = 3
)

// Menu 是菜单/权限点领域模型。功能权限码链的宿主树是租户内资源：读面
// 租户边界由存储层 RLS 承担，表无 @scope（租户内全可见），故读方法不
// 携带 Scope；写面经部署面 SECURITY DEFINER 固定口径写函数写入，运行
// 角色无直写权限，写方法带 Super。permission_code 与 menu_type 是身份/
// 类型列（创建后不变），parent_id 是结构列（变更走 Move 专项口径）。
type Menu struct {
	ID             int64
	Status         int32 // 0 禁用、1 启用
	Name           string
	PermissionCode string
	ParentID       *int64 // nil 表示根菜单
	MenuType       int32  // 1 目录、2 菜单、3 按钮

	Icon           *string
	RoutePath      *string
	Component      *string
	PermissionExpr *string
	SortOrder      int32
	Remark         *string

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// MenuNode 是菜单树节点：菜单与其直接子节点。Children 的顺序与租户内
// 平铺列表一致（sort_order 升序，同序按 id 升序）。
type MenuNode struct {
	Menu     *Menu
	Children []*MenuNode
}

// MenuRepo 是菜单仓储接口。读路径经存储层会话门面在租户事务内执行
// （RLS）；写路径是受控写面：经部署面 SECURITY DEFINER 固定口径写函数
// 写入（函数内以 app.tenant_id 复位租户边界，父级校验/防环/软删口径
// 收在函数内，事务纪律与普通写一致——每事务仍设 app.tenant_id）。
type MenuRepo interface {
	// GetByID 按 id 查询单个菜单；不存在或跨租户返回 ErrMenuNotFound。
	GetByID(ctx context.Context, auth Identity, id int64) (*Menu, error)
	// ListAll 返回租户内全部未删除菜单（树装配与环检测的全量输入；
	// 菜单树在租户规模内有限，不做分页）。
	ListAll(ctx context.Context, auth Identity) ([]*Menu, error)
	// CreateSuper 创建菜单（受控写函数固定口径：父节点校验在函数内
	// 兜底）；权限编码冲突映射 ErrMenuAlreadyExists。
	CreateSuper(ctx context.Context, auth Identity, m *Menu) (*Menu, error)
	// UpdateSuper 覆盖可变字段（name、status、icon、route_path、
	// component、permission_expr、sort_order、remark；不含
	// permission_code/menu_type/parent_id 身份/类型/结构列）。
	UpdateSuper(ctx context.Context, auth Identity, m *Menu) (*Menu, error)
	// MoveSuper 移动菜单到新父级（parentID 为 nil 表示移为根）；父级
	// 非法/成环由受控函数兜底拒绝。
	MoveSuper(ctx context.Context, auth Identity, id int64, parentID *int64) (*Menu, error)
	// SoftDeleteSuper 软删除菜单（deleted_at 置位）；存在未删除直接
	// 子菜单返回 ErrMenuHasChildren（受控函数兜底同口径）。
	SoftDeleteSuper(ctx context.Context, auth Identity, id int64) error
}

// MenuUsecase 是菜单用例。
type MenuUsecase struct {
	repo MenuRepo
}

// NewMenuUsecase 构造菜单用例。
func NewMenuUsecase(repo MenuRepo) *MenuUsecase {
	return &MenuUsecase{repo: repo}
}

// GetMenu 按 id 查询菜单。
func (uc *MenuUsecase) GetMenu(ctx context.Context, auth Identity, id int64) (*Menu, error) {
	if id <= 0 {
		return nil, ErrMenuInvalidArgument
	}
	return uc.repo.GetByID(ctx, auth, id)
}

// GetMenuTree 返回租户内全部菜单的树形结构：parent_id 为空的菜单是根，
// 子节点顺序与 ListAll 的平铺顺序一致。父引用缺失的行按根处理（受控
// 写函数纪律下不会出现，防御性兜底而非新语义）。
func (uc *MenuUsecase) GetMenuTree(ctx context.Context, auth Identity) ([]*MenuNode, int, error) {
	all, err := uc.repo.ListAll(ctx, auth)
	if err != nil {
		return nil, 0, err
	}
	nodes := make(map[int64]*MenuNode, len(all))
	for _, m := range all {
		nodes[m.ID] = &MenuNode{Menu: m}
	}
	roots := make([]*MenuNode, 0)
	for _, m := range all {
		node := nodes[m.ID]
		if m.ParentID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*m.ParentID]
		if !ok {
			// 父节点不在租户未删除集合内（防御性兜底）：按根挂载。
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}
	return roots, len(all), nil
}

// CreateMenuSuper 创建菜单（super 面）：status 未指定（哨兵）缺省启用、
// 显式 0 原样落库为禁用，父菜单存在性先经仓储判定（引用无效返回
// ErrMenuInvalidArgument）；受控写函数内父级合法性兜底校验。
func (uc *MenuUsecase) CreateMenuSuper(ctx context.Context, auth Identity, m *Menu) (*Menu, error) {
	if err := validateMenu(m, false); err != nil {
		return nil, err
	}
	if m.ParentID != nil {
		if _, err := uc.repo.GetByID(ctx, auth, *m.ParentID); err != nil {
			if kratoserrors.IsNotFound(err) {
				return nil, ErrMenuInvalidArgument
			}
			return nil, err
		}
	}
	if m.Status == MenuStatusUnspecified {
		m.Status = MenuStatusEnabled
	}
	return uc.repo.CreateSuper(ctx, auth, m)
}

// UpdateMenuSuper 覆盖可变字段（name、status、icon、route_path、
// component、permission_expr、sort_order、remark）。permission_code 与
// menu_type 是身份/类型列、parent_id 是结构列，均不在本路径变更。
func (uc *MenuUsecase) UpdateMenuSuper(ctx context.Context, auth Identity, m *Menu) (*Menu, error) {
	if m == nil || m.ID <= 0 {
		return nil, ErrMenuInvalidArgument
	}
	if err := validateMenu(m, true); err != nil {
		return nil, err
	}
	return uc.repo.UpdateSuper(ctx, auth, m)
}

// MoveMenuSuper 移动菜单：校验目标父级存在且不在被移动菜单的祖先链上
// （防环）。先取租户全量菜单构建父链做领域校验，再交仓储执行——受控
// 写函数内的防环校验是并发窗口的兜底防线。
func (uc *MenuUsecase) MoveMenuSuper(ctx context.Context, auth Identity, id int64, parentID *int64) (*Menu, error) {
	if id <= 0 {
		return nil, ErrMenuInvalidArgument
	}
	if parentID != nil {
		if *parentID == id {
			return nil, ErrMenuInvalidArgument
		}
		all, err := uc.repo.ListAll(ctx, auth)
		if err != nil {
			return nil, err
		}
		parents := make(map[int64]*int64, len(all))
		for _, m := range all {
			parents[m.ID] = m.ParentID
		}
		if _, ok := parents[*parentID]; !ok {
			// 父菜单不存在、已删除或跨租户：引用无效。
			return nil, ErrMenuInvalidArgument
		}
		// 沿新父级的祖先链上溯：命中被移动菜单即成环（移入自身子树）。
		cur := *parentID
		for i := 0; cur != 0 && i <= len(all); i++ {
			if cur == id {
				return nil, ErrMenuInvalidArgument
			}
			next := parents[cur]
			if next == nil {
				break
			}
			cur = *next
		}
	}
	return uc.repo.MoveSuper(ctx, auth, id, parentID)
}

// DeleteMenuSuper 软删除菜单；存在未删除直接子菜单时仓储返回
// ErrMenuHasChildren。
func (uc *MenuUsecase) DeleteMenuSuper(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrMenuInvalidArgument
	}
	return uc.repo.SoftDeleteSuper(ctx, auth, id)
}

// 菜单列宽约束，与 sys_menu 的列定义对齐。
const (
	menuNameMaxRunes           = 64  // varchar(64)
	menuPermissionCodeMaxRunes = 128 // varchar(128)
	menuIconMaxRunes           = 128 // varchar(128)
	menuRoutePathMaxRunes      = 256 // varchar(256)
	menuComponentMaxRunes      = 256 // varchar(256)
	menuPermissionExprMaxRunes = 256 // varchar(256)
	menuRemarkMaxRunes         = 512 // varchar(512)
)

// optionalTextInRange 可空文本列宽校验：nil 合法（不更新语义），非 nil
// 时不得超列宽。
func optionalTextInRange(s *string, maxRunes int) bool {
	return s == nil || utf8.RuneCountInString(*s) <= maxRunes
}

// validateMenu 校验菜单字段：name 必填且不超列宽、permission_code 必填
// 且不超列宽与 menu_type 取值 1-3（均仅创建路径——身份/类型列，更新输入
// 不携带语义变更）、可空文本列不超各自列宽、status 合法（创建路径额外
// 接受未指定哨兵，缺省由用例落启用；更新路径必填，只认 0/1）。
func validateMenu(m *Menu, update bool) error {
	if m == nil ||
		strings.TrimSpace(m.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(m.Name)) > menuNameMaxRunes ||
		!optionalTextInRange(m.Icon, menuIconMaxRunes) ||
		!optionalTextInRange(m.RoutePath, menuRoutePathMaxRunes) ||
		!optionalTextInRange(m.Component, menuComponentMaxRunes) ||
		!optionalTextInRange(m.PermissionExpr, menuPermissionExprMaxRunes) ||
		!optionalTextInRange(m.Remark, menuRemarkMaxRunes) {
		return ErrMenuInvalidArgument
	}
	if !update && (strings.TrimSpace(m.PermissionCode) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(m.PermissionCode)) > menuPermissionCodeMaxRunes ||
		m.MenuType < MenuTypeDirectory || m.MenuType > MenuTypeButton) {
		return ErrMenuInvalidArgument
	}
	switch {
	case update:
		if m.Status != MenuStatusDisabled && m.Status != MenuStatusEnabled {
			return ErrMenuInvalidArgument
		}
	case m.Status != MenuStatusUnspecified &&
		m.Status != MenuStatusDisabled && m.Status != MenuStatusEnabled:
		return ErrMenuInvalidArgument
	}
	return nil
}
