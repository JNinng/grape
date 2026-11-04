package biz

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/dict/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrDictTypeNotFound 表示目标字典类型不存在（含跨租户不可见：RLS 之外等同不存在）。
	ErrDictTypeNotFound = kratoserrors.NotFound(v1.ErrorReason_DICT_TYPE_NOT_FOUND.String(), "dict type not found")
	// ErrDictTypeInvalidArgument 表示字典类型请求参数非法（必填缺失、列宽超限等）。
	ErrDictTypeInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_DICT_TYPE_INVALID_ARGUMENT.String(), "invalid dict type argument")
	// ErrDictTypeAlreadyExists 表示租户内未删除字典类型间编码唯一约束冲突。
	ErrDictTypeAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_DICT_TYPE_ALREADY_EXISTS.String(), "dict type already exists")
	// ErrDictTypeHasData 表示字典类型存在未删除的字典数据，禁止删除（先清空该类型下的数据）。
	ErrDictTypeHasData = kratoserrors.Conflict(v1.ErrorReason_DICT_TYPE_HAS_DATA.String(), "dict type has active data")
	// ErrDictDataNotFound 表示目标字典数据不存在（含跨租户不可见）。
	ErrDictDataNotFound = kratoserrors.NotFound(v1.ErrorReason_DICT_DATA_NOT_FOUND.String(), "dict data not found")
	// ErrDictDataInvalidArgument 表示字典数据请求参数非法（宿主类型未知、值类型不在白名单等）。
	ErrDictDataInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_DICT_DATA_INVALID_ARGUMENT.String(), "invalid dict data argument")
	// ErrDictDataAlreadyExists 表示租户内同类型下未删除字典数据间字典值唯一约束冲突。
	ErrDictDataAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_DICT_DATA_ALREADY_EXISTS.String(), "dict data already exists")
)

// DictStatusEnabled / DictStatusDisabled / DictStatusUnspecified 与存储层
// sys_dict_type / sys_dict_data 的 status 取值对齐：-1 只是创建入参的
// 「未指定」哨兵（optional int32 的缺失必须与显式 0=禁用可区分），不落库；
// 用例把未指定落为缺省启用，显式 0 原样落库为禁用。
const (
	DictStatusDisabled    int32 = 0
	DictStatusEnabled     int32 = 1
	DictStatusUnspecified int32 = -1
)

// DictValueTypeWhiteList 是字典数据 value_type 的合法值清单（schema 注释
// 的约定枚举：系统获取值时按此预处理）。库层无 CHECK 约束，白名单收在
// 用例入参校验，脏值在写入口拒绝。
var DictValueTypeWhiteList = []string{"string", "integer", "boolean", "float", "json"}

// DictType 是字典类型领域模型。字典是租户内公共数据：租户边界由存储层
// RLS 承担，表无 @scope（租户内全可见），故仓储方法不携带 Scope。code 是
// 身份列（创建后不变），不经 Update 用例修改。字典数据不单独立仓储，
// 随本资源的两级维护收敛在同一仓储接口。
type DictType struct {
	ID     int64
	Status int32 // 0 禁用、1 启用
	Name   string
	Code   string
	Remark *string // 可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// DictTypeList 是字典类型仓储 List 返回的分页信封。
type DictTypeList struct {
	Items      []*DictType
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// DictData 是字典数据领域模型。dict_type_id 是宿主归属列（创建后不变），
// value/value_type 是值语义列（创建后不变）：前者参与同类型下唯一约束，
// 后者决定消费方的取值预处理。
type DictData struct {
	ID         int64
	Status     int32 // 0 禁用、1 启用
	DictTypeID int64
	Label      string
	Value      string
	ValueType  string
	SortOrder  int32
	Remark     *string // 可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// DictDataList 是字典数据仓储 List 返回的分页信封。
type DictDataList struct {
	Items      []*DictData
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// DictRepo 是字典仓储接口：类型与数据两级共用。全部实现必须经存储层
// 会话门面在租户事务内执行（RLS）。数据写路径先锁宿主类型行（创建）或
// 数据行（更新/删除）再写入；类型删除路径在同一事务内校验活跃数据。
// List 消费 dynq 动态查询协议。
type DictRepo interface {
	// GetTypeByID 按 id 查询单个字典类型；不存在或跨租户返回 ErrDictTypeNotFound。
	GetTypeByID(ctx context.Context, auth Identity, id int64) (*DictType, error)
	// ListTypes 按动态查询协议返回分页集合（租户内平铺视图）。
	ListTypes(ctx context.Context, auth Identity, q *dynq.Query) (*DictTypeList, error)
	// CreateType 创建字典类型；编码冲突映射 ErrDictTypeAlreadyExists。
	CreateType(ctx context.Context, auth Identity, t *DictType) (*DictType, error)
	// UpdateType 覆盖可变字段（name、status、remark；不含 code）。
	UpdateType(ctx context.Context, auth Identity, t *DictType) (*DictType, error)
	// SoftDeleteType 软删除字典类型（deleted_at 置位）。存在未删除字典数据
	// 返回 ErrDictTypeHasData 且不写入。
	SoftDeleteType(ctx context.Context, auth Identity, id int64) error

	// GetDataByID 按 id 查询单个字典数据；不存在或跨租户返回 ErrDictDataNotFound。
	GetDataByID(ctx context.Context, auth Identity, id int64) (*DictData, error)
	// ListDatas 按动态查询协议返回分页集合（租户内平铺视图）。
	ListDatas(ctx context.Context, auth Identity, q *dynq.Query) (*DictDataList, error)
	// CreateData 创建字典数据：宿主类型必须存在且未删除，未知返回
	// ErrDictDataInvalidArgument 且不写入；同类型下字典值冲突映射
	// ErrDictDataAlreadyExists。
	CreateData(ctx context.Context, auth Identity, d *DictData) (*DictData, error)
	// UpdateData 覆盖可变字段（label、status、sort_order、remark；不含
	// value/value_type 与 dict_type_id）。
	UpdateData(ctx context.Context, auth Identity, d *DictData) (*DictData, error)
	// SoftDeleteData 软删除字典数据（deleted_at 置位）。宿主类型不受影响。
	SoftDeleteData(ctx context.Context, auth Identity, id int64) error
}

// DictUsecase 是字典用例。
type DictUsecase struct {
	repo DictRepo
}

// NewDictUsecase 构造字典用例。
func NewDictUsecase(repo DictRepo) *DictUsecase {
	return &DictUsecase{repo: repo}
}

// GetDictType 按 id 查询字典类型。
func (uc *DictUsecase) GetDictType(ctx context.Context, auth Identity, id int64) (*DictType, error) {
	if id <= 0 {
		return nil, ErrDictTypeInvalidArgument
	}
	return uc.repo.GetTypeByID(ctx, auth, id)
}

// ListDictTypes 经动态查询协议列出字典类型（平铺分页视图）。
func (uc *DictUsecase) ListDictTypes(ctx context.Context, auth Identity, q *dynq.Query) (*DictTypeList, error) {
	return uc.repo.ListTypes(ctx, auth, q)
}

// CreateDictType 创建字典类型：status 未指定（哨兵）缺省启用、显式 0 原样
// 落库为禁用。
func (uc *DictUsecase) CreateDictType(ctx context.Context, auth Identity, t *DictType) (*DictType, error) {
	if err := validateDictType(t, false); err != nil {
		return nil, err
	}
	if t.Status == DictStatusUnspecified {
		t.Status = DictStatusEnabled
	}
	return uc.repo.CreateType(ctx, auth, t)
}

// UpdateDictType 覆盖可变字段（name、status、remark）。code 由服务端管理：
// 类型编码创建后不变。
func (uc *DictUsecase) UpdateDictType(ctx context.Context, auth Identity, t *DictType) (*DictType, error) {
	if t == nil || t.ID <= 0 {
		return nil, ErrDictTypeInvalidArgument
	}
	if err := validateDictType(t, true); err != nil {
		return nil, err
	}
	return uc.repo.UpdateType(ctx, auth, t)
}

// DeleteDictType 软删除字典类型；存在未删除字典数据时仓储返回
// ErrDictTypeHasData。
func (uc *DictUsecase) DeleteDictType(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrDictTypeInvalidArgument
	}
	return uc.repo.SoftDeleteType(ctx, auth, id)
}

// GetDictData 按 id 查询字典数据。
func (uc *DictUsecase) GetDictData(ctx context.Context, auth Identity, id int64) (*DictData, error) {
	if id <= 0 {
		return nil, ErrDictDataInvalidArgument
	}
	return uc.repo.GetDataByID(ctx, auth, id)
}

// ListDictDatas 经动态查询协议列出字典数据（平铺分页视图）。
func (uc *DictUsecase) ListDictDatas(ctx context.Context, auth Identity, q *dynq.Query) (*DictDataList, error) {
	return uc.repo.ListDatas(ctx, auth, q)
}

// CreateDictData 创建字典数据：status 未指定（哨兵）缺省启用、显式 0 原样
// 落库为禁用；宿主类型存在性与同类型下字典值唯一由仓储在同一事务内判定。
func (uc *DictUsecase) CreateDictData(ctx context.Context, auth Identity, d *DictData) (*DictData, error) {
	if err := validateDictData(d, false); err != nil {
		return nil, err
	}
	if d.Status == DictStatusUnspecified {
		d.Status = DictStatusEnabled
	}
	return uc.repo.CreateData(ctx, auth, d)
}

// UpdateDictData 覆盖可变字段（label、status、sort_order、remark）。
// value/value_type 与宿主归属 dict_type_id 由服务端管理：值语义列与归属
// 列创建后不变。
func (uc *DictUsecase) UpdateDictData(ctx context.Context, auth Identity, d *DictData) (*DictData, error) {
	if d == nil || d.ID <= 0 {
		return nil, ErrDictDataInvalidArgument
	}
	if err := validateDictData(d, true); err != nil {
		return nil, err
	}
	return uc.repo.UpdateData(ctx, auth, d)
}

// DeleteDictData 软删除字典数据；宿主类型不受影响。
func (uc *DictUsecase) DeleteDictData(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrDictDataInvalidArgument
	}
	return uc.repo.SoftDeleteData(ctx, auth, id)
}

// 字典列宽约束，与 sys_dict_type / sys_dict_data 的 name/code/label/value/
// value_type/remark 列宽对齐。
const (
	dictTypeNameMaxRunes      = 256 // varchar(256)
	dictTypeCodeMaxRunes      = 64  // varchar(64)
	dictTypeRemarkMaxRunes    = 512 // varchar(512)
	dictDataLabelMaxRunes     = 256 // varchar(256)
	dictDataValueMaxRunes     = 256 // varchar(256)
	dictDataValueTypeMaxRunes = 32  // varchar(32)
	dictDataRemarkMaxRunes    = 512 // varchar(512)
)

// validateDictType 校验字典类型可变字段：name 必填且不超列宽、remark 不超
// 列宽、status 合法（创建路径额外接受未指定哨兵，缺省由用例落启用；更新
// 路径必填，只认 0/1）。code 是身份列：仅创建路径必填且不超列宽（更新
// 输入不携带，仓储不写该列）。
func validateDictType(t *DictType, update bool) error {
	if t == nil ||
		strings.TrimSpace(t.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(t.Name)) > dictTypeNameMaxRunes ||
		(t.Remark != nil && utf8.RuneCountInString(*t.Remark) > dictTypeRemarkMaxRunes) {
		return ErrDictTypeInvalidArgument
	}
	if !update && (strings.TrimSpace(t.Code) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(t.Code)) > dictTypeCodeMaxRunes) {
		return ErrDictTypeInvalidArgument
	}
	return validateDictStatus(t.Status, update, ErrDictTypeInvalidArgument)
}

// validateDictData 校验字典数据字段：label 必填且不超列宽、remark 不超列宽、
// sort_order 非负、status 合法（口径同类型）。value/value_type 与宿主
// dict_type_id 是身份/值语义/归属列，仅创建路径必填（value_type 限白名单
// 且不超列宽，宿主存在性由仓储在事务内判定）；更新输入不携带，仓储不写
// 这些列。
func validateDictData(d *DictData, update bool) error {
	if d == nil ||
		strings.TrimSpace(d.Label) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(d.Label)) > dictDataLabelMaxRunes ||
		(d.Remark != nil && utf8.RuneCountInString(*d.Remark) > dictDataRemarkMaxRunes) ||
		d.SortOrder < 0 {
		return ErrDictDataInvalidArgument
	}
	if !update {
		if strings.TrimSpace(d.Value) == "" ||
			utf8.RuneCountInString(strings.TrimSpace(d.Value)) > dictDataValueMaxRunes ||
			strings.TrimSpace(d.ValueType) == "" ||
			utf8.RuneCountInString(strings.TrimSpace(d.ValueType)) > dictDataValueTypeMaxRunes ||
			!slices.Contains(DictValueTypeWhiteList, d.ValueType) ||
			d.DictTypeID <= 0 {
			return ErrDictDataInvalidArgument
		}
	}
	return validateDictStatus(d.Status, update, ErrDictDataInvalidArgument)
}

// validateDictStatus 校验字典两级共用的 status 取值：更新路径必填（只认
// 0/1），创建路径额外接受未指定哨兵。
func validateDictStatus(status int32, update bool, invalid error) error {
	switch {
	case update:
		if status != DictStatusDisabled && status != DictStatusEnabled {
			return invalid
		}
	case status != DictStatusUnspecified &&
		status != DictStatusDisabled && status != DictStatusEnabled:
		return invalid
	}
	return nil
}
