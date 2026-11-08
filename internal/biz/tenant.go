package biz

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/tenant/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrTenantNotFound 表示目标租户不存在（含已软删与调用方租户树外
	// 不可见：RLS 之外等同不存在）。
	ErrTenantNotFound = kratoserrors.NotFound(v1.ErrorReason_TENANT_NOT_FOUND.String(), "tenant not found")
	// ErrTenantInvalidArgument 表示租户请求参数非法（必填缺失、列宽超限、
	// locale 形态、父级引用无效、max_user_count 取值、操作自身租户等）。
	ErrTenantInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_TENANT_INVALID_ARGUMENT.String(), "invalid tenant argument")
	// ErrTenantAlreadyExists 表示未删除租户间 code 或 name 唯一约束冲突。
	ErrTenantAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_TENANT_ALREADY_EXISTS.String(), "tenant already exists")
	// ErrTenantHasChildren 表示租户存在未删除的直接子租户，禁止删除
	//（需自叶向根逐层删）。
	ErrTenantHasChildren = kratoserrors.Conflict(v1.ErrorReason_TENANT_HAS_CHILDREN.String(), "tenant has active children")
	// ErrTenantSelfStatusChange 表示更新路径试图改变调用方自身租户的
	// status：自身停用/恢复须走 Disable/Restore，不能借通用更新绕过。
	// 与 ErrTenantInvalidArgument 同 code/reason，errors.Is 可互判。
	ErrTenantSelfStatusChange = kratoserrors.BadRequest(v1.ErrorReason_TENANT_INVALID_ARGUMENT.String(),
		"cannot change own tenant status; use disable or restore")
	// ErrTenantCallerMissing 表示调用方租户行不存在或已软删：创建子租户
	// 前同事务锁定调用方行，未命中按入参错误拒绝且不写入；与
	// ErrTenantInvalidArgument 同 code/reason。
	ErrTenantCallerMissing = kratoserrors.BadRequest(v1.ErrorReason_TENANT_INVALID_ARGUMENT.String(),
		"caller tenant not found or deleted")
)

// 租户数据状态：TenantStatusUnspecified 只是创建入参的「未指定」哨兵
// （optional int32 的缺失必须与显式 0=禁用可区分），不落库；用例把未指定
// 落为缺省启用，显式 0 原样落库为禁用。停用/恢复是目标状态写（0/1）。
const (
	TenantStatusDisabled    int32 = 0
	TenantStatusEnabled     int32 = 1
	TenantStatusUnspecified int32 = -1
)

// DefaultTenantLocale 是租户默认语言缺省值：创建时 default_locale 为空
// （TrimSpace 后）落此值，与 schema 列默认值一致。
const DefaultTenantLocale = "zh-CN"

// Tenant 是租户实体领域模型。租户树模型：每行归属其父租户（TenantID
// 列），RLS 允许命中 tenant_id = app.tenant_id 或 id = app.tenant_id 的
// 行——每个租户可读写自己名下的直接子租户与自身，树外行天然不可见。
// 结构父级与行归属必须一致：创建时 parent_id 省略（nil）或显式等于调用
// 方租户，指向其它租户一律拒绝。TenantID 由服务端按认证身份落值、只读；
// ParentID 是结构列、Code 是身份列（同 dict code / feature flag_key
// 口径），两者创建后不可变；Name 在未删除行间唯一（部分唯一索引），
// 更新可改。
type Tenant struct {
	ID            string
	Status        int32 // 0 禁用、1 启用
	Code          string
	Name          string
	ParentID      *string // nil 表示创建时缺省（用例展开为调用方租户）
	ContactName   *string // 可空
	ContactPhone  *string // 可空
	ExpiredAt     *time.Time
	MaxUserCount  *int32 // 可空；提供时必须 >0
	DefaultLocale string
	TenantID      string // 只读：服务端落值（行归属父租户）

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// TenantList 是租户仓储 List 返回的分页信封。
type TenantList struct {
	Items      []*Tenant
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// TenantRepo 是租户仓储接口。租户边界由存储层 RLS 承担：sys_tenant 的
// 策略允许命中 tenant_id = app.tenant_id 或 id = app.tenant_id 的行，每个
// 租户可读写自己名下的直接子租户与自身，树外行等同不存在。
// 写/状态方法名带 Super 表达「管理一整个租户实体」，不表示绕过 RLS；
// 唯一例外是删除前置的只读计数函数（见 README「迁移暂存」）。
type TenantRepo interface {
	// List 按动态查询协议返回分页集合（调用方租户树可见部分）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*TenantList, error)
	// Get 按 id 查询单个租户；不存在、已软删或树外不可见返回
	// ErrTenantNotFound。
	Get(ctx context.Context, auth Identity, id string) (*Tenant, error)
	// CreateSuper 创建租户：id 由仓储发号，tenant_id 取会话
	// app.tenant_id；parent_id 必须省略或等于调用方租户，且调用方租户行
	// 须存在、未软删并在插入前同事务锁定（否则 ErrTenantInvalidArgument /
	// ErrTenantCallerMissing）；未删除行间 code/name 冲突映射
	// ErrTenantAlreadyExists。
	CreateSuper(ctx context.Context, auth Identity, t *Tenant) (*Tenant, error)
	// UpdateSuper 覆盖可变字段（status、name、contact_name、
	// contact_phone、expired_at、max_user_count、default_locale）；code 是
	// 身份列（创建后不可变，更新输入忽略），parent_id/tenant_id 是
	// 结构/归属列，均不写。目标不可见返回 ErrTenantNotFound；调用方自身
	// 租户的 status 变更返回 ErrTenantSelfStatusChange（同值更新放行）。
	UpdateSuper(ctx context.Context, auth Identity, t *Tenant) (*Tenant, error)
	// DeleteSuper 软删除租户；存在未删除直接子租户返回
	// ErrTenantHasChildren，目标不可见返回 ErrTenantNotFound。
	DeleteSuper(ctx context.Context, auth Identity, id string) error
	// DisableSuper 置 status=0（幂等目标状态写）；目标不可见返回
	// ErrTenantNotFound。
	DisableSuper(ctx context.Context, auth Identity, id string) (*Tenant, error)
	// RestoreSuper 置 status=1（幂等目标状态写）；目标不可见返回
	// ErrTenantNotFound。
	RestoreSuper(ctx context.Context, auth Identity, id string) (*Tenant, error)
}

// TenantUsecase 是平台级租户管理用例。API 方法名统一带 Super（对象是
// 租户实体）；Super 表达平台级租户实体管理，不表示绕过 RLS。
type TenantUsecase struct {
	repo TenantRepo
}

// NewTenantUsecase 构造租户用例。
func NewTenantUsecase(repo TenantRepo) *TenantUsecase {
	return &TenantUsecase{repo: repo}
}

// GetTenantSuper 按 id 查询租户。
func (uc *TenantUsecase) GetTenantSuper(ctx context.Context, auth Identity, id string) (*Tenant, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrTenantInvalidArgument
	}
	return uc.repo.Get(ctx, auth, id)
}

// ListTenantsSuper 经动态查询协议列出租户（调用方租户树可见部分）。
func (uc *TenantUsecase) ListTenantsSuper(ctx context.Context, auth Identity, q *dynq.Query) (*TenantList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateTenantSuper 创建租户实体：status 未指定缺省启用、显式 0 禁用；
// parent_id 省略缺省为调用方租户，提供时必须等于调用方租户（结构父级与
// 行归属必须一致）。code/name 去首尾空白，default_locale 空则 zh-CN。
func (uc *TenantUsecase) CreateTenantSuper(ctx context.Context, auth Identity, t *Tenant) (*Tenant, error) {
	if err := validateTenant(t, false); err != nil {
		return nil, err
	}
	t.Code = strings.TrimSpace(t.Code)
	t.Name = strings.TrimSpace(t.Name)
	t.DefaultLocale = strings.TrimSpace(t.DefaultLocale)
	if t.DefaultLocale == "" {
		t.DefaultLocale = DefaultTenantLocale
	}
	if t.ParentID == nil {
		parent := auth.TenantID
		t.ParentID = &parent
	}
	// 结构父级必须等于调用方租户：RLS 的 WITH CHECK 要求新行
	// tenant_id = app.tenant_id，若允许挂到其它租户名下会形成结构父级
	// 与行归属分裂的树。每个租户只能创建自己的直接子租户。
	if *t.ParentID != auth.TenantID {
		return nil, ErrTenantInvalidArgument
	}
	if t.Status == TenantStatusUnspecified {
		t.Status = TenantStatusEnabled
	}
	return uc.repo.CreateSuper(ctx, auth, t)
}

// UpdateTenantSuper 覆盖可变字段（status、name、contact_name、
// contact_phone、expired_at、max_user_count、default_locale）。code/
// parent_id/tenant_id 创建后不可变并在输入中被清除；name 去首尾空白、
// default_locale 空则 zh-CN。自身租户的 status 变更（同值除外）拒绝。
func (uc *TenantUsecase) UpdateTenantSuper(ctx context.Context, auth Identity, t *Tenant) (*Tenant, error) {
	if t == nil || strings.TrimSpace(t.ID) == "" {
		return nil, ErrTenantInvalidArgument
	}
	if err := validateTenant(t, true); err != nil {
		return nil, err
	}
	t.Name = strings.TrimSpace(t.Name)
	t.DefaultLocale = strings.TrimSpace(t.DefaultLocale)
	if t.DefaultLocale == "" {
		t.DefaultLocale = DefaultTenantLocale
	}
	t.Code = ""
	t.ParentID = nil
	t.TenantID = ""
	if t.ID == auth.TenantID {
		current, err := uc.repo.Get(ctx, auth, t.ID)
		if err != nil {
			return nil, err
		}
		if int32(current.Status) != t.Status {
			return nil, ErrTenantSelfStatusChange
		}
	}
	return uc.repo.UpdateSuper(ctx, auth, t)
}

// DeleteTenantSuper 软删除租户；存在未删除直接子租户时仓储返回
// ErrTenantHasChildren。禁止删除调用方自身租户。
func (uc *TenantUsecase) DeleteTenantSuper(ctx context.Context, auth Identity, id string) error {
	if err := uc.validateTarget(auth, id); err != nil {
		return err
	}
	return uc.repo.DeleteSuper(ctx, auth, id)
}

// DisableTenantSuper 停用租户（status=0，幂等目标状态写）。禁止停用调用方
// 自身租户。
func (uc *TenantUsecase) DisableTenantSuper(ctx context.Context, auth Identity, id string) (*Tenant, error) {
	if err := uc.validateTarget(auth, id); err != nil {
		return nil, err
	}
	return uc.repo.DisableSuper(ctx, auth, id)
}

// RestoreTenantSuper 恢复租户（status=1，幂等目标状态写）。禁止恢复调用方
// 自身租户。
func (uc *TenantUsecase) RestoreTenantSuper(ctx context.Context, auth Identity, id string) (*Tenant, error) {
	if err := uc.validateTarget(auth, id); err != nil {
		return nil, err
	}
	return uc.repo.RestoreSuper(ctx, auth, id)
}

// validateTarget 校验目标 id 非空且不是调用方自身租户：停用/恢复/删除
// 自身租户会切断当前管理会话的租户身份，按入参错误拒绝（文案英文，与
// ErrTenantInvalidArgument 同 code/reason，errors.Is 可判定）。
func (uc *TenantUsecase) validateTarget(auth Identity, id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ErrTenantInvalidArgument
	}
	if trimmed == auth.TenantID {
		return ErrTenantInvalidArgument
	}
	return nil
}

// 租户列宽约束，与 sys_tenant 的 code/name/contact_name/contact_phone/
// default_locale 列宽对齐。
const (
	tenantCodeMaxRunes          = 32  // varchar(32)
	tenantNameMaxRunes          = 256 // varchar(256)
	tenantContactNameMaxRunes   = 64  // varchar(64)
	tenantContactPhoneMaxRunes  = 32  // varchar(32)
	tenantDefaultLocaleMaxRunes = 10  // varchar(10)
)

// tenantLocalePattern 是 default_locale 的 BCP-47 基本形态校验（语言
// 2-3 个字母，后接可选的大小写无关子标签，每段 1-8 位字母数字），如
// zh-CN、en-US、ja-JP。总长由列宽单独限制（≤10）。
var tenantLocalePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// validateTenant 校验租户入参：name 必填且不超列宽、联系人列不超列宽、
// default_locale 非空时匹配 BCP-47 且不超列宽、max_user_count 提供时 >0、
// status 合法（创建接受未指定哨兵，更新只认 0/1）。code 仅创建路径必填。
func validateTenant(t *Tenant, update bool) error {
	if t == nil ||
		strings.TrimSpace(t.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(t.Name)) > tenantNameMaxRunes ||
		(t.ContactName != nil && utf8.RuneCountInString(*t.ContactName) > tenantContactNameMaxRunes) ||
		(t.ContactPhone != nil && utf8.RuneCountInString(*t.ContactPhone) > tenantContactPhoneMaxRunes) ||
		(t.MaxUserCount != nil && *t.MaxUserCount <= 0) {
		return ErrTenantInvalidArgument
	}
	if !update && (strings.TrimSpace(t.Code) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(t.Code)) > tenantCodeMaxRunes) {
		return ErrTenantInvalidArgument
	}
	locale := strings.TrimSpace(t.DefaultLocale)
	if locale != "" &&
		(utf8.RuneCountInString(locale) > tenantDefaultLocaleMaxRunes || !tenantLocalePattern.MatchString(locale)) {
		return ErrTenantInvalidArgument
	}
	return validateTenantStatus(t.Status, update)
}

// validateTenantStatus 校验 status 取值：更新路径必填（只认 0/1），创建
// 路径额外接受未指定哨兵（缺省由用例落启用）。
func validateTenantStatus(status int32, update bool) error {
	switch {
	case update:
		if status != TenantStatusDisabled && status != TenantStatusEnabled {
			return ErrTenantInvalidArgument
		}
	case status != TenantStatusUnspecified &&
		status != TenantStatusDisabled && status != TenantStatusEnabled:
		return ErrTenantInvalidArgument
	}
	return nil
}
