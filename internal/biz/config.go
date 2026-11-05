package biz

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	v1 "grape/api/config/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrConfigNotFound 表示目标全局配置不存在（含已软删）。
	ErrConfigNotFound = kratoserrors.NotFound(v1.ErrorReason_CONFIG_NOT_FOUND.String(), "config not found")
	// ErrConfigInvalidArgument 表示全局配置请求参数非法（必填缺失、列宽
	// 超限、值类型不在白名单等）。
	ErrConfigInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_CONFIG_INVALID_ARGUMENT.String(), "invalid config argument")
	// ErrConfigAlreadyExists 表示未删除全局配置间 config_key 唯一约束冲突。
	ErrConfigAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_CONFIG_ALREADY_EXISTS.String(), "config already exists")
	// ErrConfigPlatformRequired 表示全局配置写路径的调用方不是平台（根）
	// 租户。sys_config 是跨租户共享表，写权限若只依赖调用者租户内的
	// 角色 → 菜单 → 权限码链，子租户被授予菜单/角色管理后可自造
	// sys:config:global:* 写码——因此写路径在用例层强制平台信任边界，
	// 非根租户即使持有权限码也拒绝（403，存在性不敏感）。
	ErrConfigPlatformRequired = kratoserrors.Forbidden(v1.ErrorReason_CONFIG_PLATFORM_REQUIRED.String(), "platform tenant required")
	// ErrTenantConfigNotFound 表示目标租户配置不存在（含跨租户不可见与
	// 已软删）。
	ErrTenantConfigNotFound = kratoserrors.NotFound(v1.ErrorReason_TENANT_CONFIG_NOT_FOUND.String(), "tenant config not found")
	// ErrTenantConfigInvalidArgument 表示租户配置请求参数非法（必填缺失、
	// 列宽超限、值类型不在白名单等）。
	ErrTenantConfigInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_TENANT_CONFIG_INVALID_ARGUMENT.String(), "invalid tenant config argument")
	// ErrTenantConfigAlreadyExists 表示租户内未删除配置间 config_key 唯一
	// 约束冲突。
	ErrTenantConfigAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_TENANT_CONFIG_ALREADY_EXISTS.String(), "tenant config already exists")
	// ErrFeatureFlagNotFound 表示目标功能开关不存在（含跨租户不可见与已
	// 软删）。
	ErrFeatureFlagNotFound = kratoserrors.NotFound(v1.ErrorReason_FEATURE_FLAG_NOT_FOUND.String(), "feature flag not found")
	// ErrFeatureFlagInvalidArgument 表示功能开关请求参数非法（必填缺失、
	// 列宽超限、status 取值等）。
	ErrFeatureFlagInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_FEATURE_FLAG_INVALID_ARGUMENT.String(), "invalid feature flag argument")
	// ErrFeatureFlagAlreadyExists 表示租户内未删除开关间 flag_key 唯一约束
	// 冲突。
	ErrFeatureFlagAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_FEATURE_FLAG_ALREADY_EXISTS.String(), "feature flag already exists")
	// ErrTranslationNotFound 表示目标翻译不存在（含跨租户不可见与已软删）。
	ErrTranslationNotFound = kratoserrors.NotFound(v1.ErrorReason_TRANSLATION_NOT_FOUND.String(), "translation not found")
	// ErrTranslationInvalidArgument 表示翻译请求参数非法（必填缺失、列宽
	// 超限、locale 形态、row_id 取值等）。
	ErrTranslationInvalidArgument = kratoserrors.BadRequest(v1.ErrorReason_TRANSLATION_INVALID_ARGUMENT.String(), "invalid translation argument")
	// ErrTranslationAlreadyExists 表示租户内未删除翻译间身份四元组唯一
	// 约束冲突。
	ErrTranslationAlreadyExists = kratoserrors.Conflict(v1.ErrorReason_TRANSLATION_ALREADY_EXISTS.String(), "translation already exists")
)

// 配置数据状态：-1 只是创建入参的「未指定」哨兵（optional int32 的缺失
// 必须与显式 0=禁用可区分），不落库；用例把未指定落为缺省启用，显式 0
// 原样落库为禁用。全局配置与租户配置共用同一取值口径。
const (
	ConfigStatusDisabled    int32 = 0
	ConfigStatusEnabled     int32 = 1
	ConfigStatusUnspecified int32 = -1
)

// ConfigValueTypeWhiteList 是配置 value_type 的合法值清单（schema 注释
// 的约定枚举：消费方取值时按此预处理）。库层无 CHECK 约束，白名单收在
// 用例入参校验，脏值在写入口拒绝。全局配置与租户配置共用。
var ConfigValueTypeWhiteList = []string{"string", "integer", "boolean", "float", "json"}

// Config 是系统全局配置领域模型。sys_config 是平台级全局表：不绑定
// 租户、无 tenant_id、不设 RLS，故仓储方法不携带租户或 Scope 语义，
// Identity 只用于审计操作者与认证事务纪律。config_key 是唯一键
// （未删除行间），value_type 是消费方取值预处理声明：两者创建后不可变，
// 不经 Update 用例修改。
type Config struct {
	ID        int64
	Status    int32 // 0 禁用、1 启用
	Name      string
	ConfigKey string
	Value     *string // 可空
	ValueType string
	Remark    *string // 可空

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// ConfigList 是全局配置仓储 List 返回的分页信封。
type ConfigList struct {
	Items      []*Config
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// TenantConfig 是租户配置领域模型。租户边界由存储层 RLS 承担（表无
// @scope，租户内全可见），TenantID 由服务端按认证身份落值、只读。
// config_key（租户内未删除行间唯一）与 value_type 创建后不可变。
type TenantConfig struct {
	ID        int64
	Status    int32 // 0 禁用、1 启用
	Name      string
	ConfigKey string
	Value     *string // 可空
	ValueType string
	Remark    *string // 可空
	TenantID  string  // 只读：服务端落值

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// TenantConfigList 是租户配置仓储 List 返回的分页信封。
type TenantConfigList struct {
	Items      []*TenantConfig
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// ConfigRepo 是全局配置仓储接口。sys_config 无 RLS 与行级范围，读取
// 本身即全局口径（不带 super）；写入经部署面 SECURITY DEFINER 固定口径
// 写函数（role.sql 已对运行角色 REVOKE 直写），方法名按 permission.md
// 口径带 Super。List 消费 dynq 动态查询协议。
type ConfigRepo interface {
	// Get 按 id 查询单个全局配置；不存在或已软删返回 ErrConfigNotFound。
	Get(ctx context.Context, auth Identity, id int64) (*Config, error)
	// List 按动态查询协议返回分页集合（全局平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*ConfigList, error)
	// CreateSuper 经受控写函数创建全局配置；config_key 冲突映射
	// ErrConfigAlreadyExists。
	CreateSuper(ctx context.Context, auth Identity, c *Config) (*Config, error)
	// UpdateSuper 经受控写函数覆盖可变字段（name、status、value、remark；
	// 不含 config_key/value_type）。
	UpdateSuper(ctx context.Context, auth Identity, c *Config) (*Config, error)
	// DeleteSuper 经受控写函数软删除全局配置（deleted_at 置位）。
	DeleteSuper(ctx context.Context, auth Identity, id int64) error
}

// TenantConfigRepo 是租户配置仓储接口。租户边界由存储层会话门面在租户
// 事务内执行（RLS），写路径为普通 DML，故方法名一律不带 Super。
// TenantID 由仓储从会话 app.tenant_id 落值，不取请求值。
type TenantConfigRepo interface {
	// Get 按 id 查询单个租户配置；不存在、跨租户或已软删返回
	// ErrTenantConfigNotFound。
	Get(ctx context.Context, auth Identity, id int64) (*TenantConfig, error)
	// List 按动态查询协议返回分页集合（租户内平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*TenantConfigList, error)
	// Create 创建租户配置；租户内 config_key 冲突映射
	// ErrTenantConfigAlreadyExists。
	Create(ctx context.Context, auth Identity, c *TenantConfig) (*TenantConfig, error)
	// Update 覆盖可变字段（name、status、value、remark；不含 config_key/
	// value_type/tenant_id）。
	Update(ctx context.Context, auth Identity, c *TenantConfig) (*TenantConfig, error)
	// Delete 软删除租户配置（deleted_at 置位）。
	Delete(ctx context.Context, auth Identity, id int64) error
}

// PlatformTenantGuard 判定调用方租户是否为平台（根）租户，供全局配置写
// 路径的信任边界使用。sys_config 是跨租户全局表，功能权限码链（角色 →
// 菜单 → 权限码）只在调用者租户内成立，不能单独作为写全局配置的授权
// 依据；实现须读调用方自身租户行（RLS 放行 id = app.tenant_id）：租户
// 存在、未软删且 parent_id IS NULL 才是平台租户，目标不存在/已删 →
// (false, nil)。错误应原样透传（用例不吞错）。
type PlatformTenantGuard interface {
	IsPlatformTenant(ctx context.Context, auth Identity) (bool, error)
}

// ConfigUsecase 是全局配置用例。写路径（Create/Update/DeleteSuper）先经
// PlatformTenantGuard 校验平台信任边界，读路径不设此守卫（读取本身即
// 全局口径）。
type ConfigUsecase struct {
	repo  ConfigRepo
	guard PlatformTenantGuard
}

// NewConfigUsecase 构造全局配置用例。
func NewConfigUsecase(repo ConfigRepo, guard PlatformTenantGuard) *ConfigUsecase {
	return &ConfigUsecase{repo: repo, guard: guard}
}

// requirePlatformTenant 是全局配置写路径的平台信任边界：非平台（根）
// 租户返回 ErrConfigPlatformRequired（403），守卫自身错误原样透传。
// 未平台校验通过前不触达仓储。
func (uc *ConfigUsecase) requirePlatformTenant(ctx context.Context, auth Identity) error {
	platform, err := uc.guard.IsPlatformTenant(ctx, auth)
	if err != nil {
		return err
	}
	if !platform {
		return ErrConfigPlatformRequired
	}
	return nil
}

// GetConfig 按 id 查询全局配置。
func (uc *ConfigUsecase) GetConfig(ctx context.Context, auth Identity, id int64) (*Config, error) {
	if id <= 0 {
		return nil, ErrConfigInvalidArgument
	}
	return uc.repo.Get(ctx, auth, id)
}

// ListConfigs 经动态查询协议列出全局配置（平铺分页视图）。
func (uc *ConfigUsecase) ListConfigs(ctx context.Context, auth Identity, q *dynq.Query) (*ConfigList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateConfigSuper 创建全局配置：仅平台（根）租户可写（写权限不能只
// 依赖租户内功能权限码，见 PlatformTenantGuard）；status 未指定（哨兵）
// 缺省启用、显式 0 原样落库为禁用；config_key/value_type 必须合法（后者
// 限白名单）。name/config_key 落库前去除首尾空白（与列宽校验口径一致，
// 避免「校验按 trim 后长度、落库却是原值」触发库层 22001）；value 原样
// 透传。
func (uc *ConfigUsecase) CreateConfigSuper(ctx context.Context, auth Identity, c *Config) (*Config, error) {
	if err := uc.requirePlatformTenant(ctx, auth); err != nil {
		return nil, err
	}
	if err := validateConfig(c, false); err != nil {
		return nil, err
	}
	c.Name = strings.TrimSpace(c.Name)
	c.ConfigKey = strings.TrimSpace(c.ConfigKey)
	if c.Status == ConfigStatusUnspecified {
		c.Status = ConfigStatusEnabled
	}
	return uc.repo.CreateSuper(ctx, auth, c)
}

// UpdateConfigSuper 覆盖可变字段（name、status、value、remark）；仅平台
// （根）租户可写。config_key/value_type 由服务端管理：身份/值语义列创建
// 后不变。name 落库前去除首尾空白（口径同创建）；value 原样透传。
func (uc *ConfigUsecase) UpdateConfigSuper(ctx context.Context, auth Identity, c *Config) (*Config, error) {
	if err := uc.requirePlatformTenant(ctx, auth); err != nil {
		return nil, err
	}
	if c == nil || c.ID <= 0 {
		return nil, ErrConfigInvalidArgument
	}
	if err := validateConfig(c, true); err != nil {
		return nil, err
	}
	c.Name = strings.TrimSpace(c.Name)
	return uc.repo.UpdateSuper(ctx, auth, c)
}

// DeleteConfigSuper 软删除全局配置；仅平台（根）租户可写。目标不存在或
// 已软删由仓储映射 NotFound。
func (uc *ConfigUsecase) DeleteConfigSuper(ctx context.Context, auth Identity, id int64) error {
	if err := uc.requirePlatformTenant(ctx, auth); err != nil {
		return err
	}
	if id <= 0 {
		return ErrConfigInvalidArgument
	}
	return uc.repo.DeleteSuper(ctx, auth, id)
}

// TenantConfigUsecase 是租户配置用例。
type TenantConfigUsecase struct {
	repo TenantConfigRepo
}

// NewTenantConfigUsecase 构造租户配置用例。
func NewTenantConfigUsecase(repo TenantConfigRepo) *TenantConfigUsecase {
	return &TenantConfigUsecase{repo: repo}
}

// GetTenantConfig 按 id 查询租户配置。
func (uc *TenantConfigUsecase) GetTenantConfig(ctx context.Context, auth Identity, id int64) (*TenantConfig, error) {
	if id <= 0 {
		return nil, ErrTenantConfigInvalidArgument
	}
	return uc.repo.Get(ctx, auth, id)
}

// ListTenantConfigs 经动态查询协议列出租户配置（租户内平铺分页视图）。
func (uc *TenantConfigUsecase) ListTenantConfigs(ctx context.Context, auth Identity, q *dynq.Query) (*TenantConfigList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateTenantConfig 创建租户配置：status 未指定（哨兵）缺省启用、显式
// 0 原样落库为禁用；tenant_id 不取入参值，由仓储按会话落值。
// name/config_key 落库前去除首尾空白（口径同全局配置）；value 原样透传。
func (uc *TenantConfigUsecase) CreateTenantConfig(ctx context.Context, auth Identity, c *TenantConfig) (*TenantConfig, error) {
	if err := validateTenantConfig(c, false); err != nil {
		return nil, err
	}
	c.Name = strings.TrimSpace(c.Name)
	c.ConfigKey = strings.TrimSpace(c.ConfigKey)
	if c.Status == ConfigStatusUnspecified {
		c.Status = ConfigStatusEnabled
	}
	return uc.repo.Create(ctx, auth, c)
}

// UpdateTenantConfig 覆盖可变字段（name、status、value、remark）。
// config_key/value_type 身份/值语义列与 tenant_id 归属列创建后不变。
// name 落库前去除首尾空白（口径同创建）；value 原样透传。
func (uc *TenantConfigUsecase) UpdateTenantConfig(ctx context.Context, auth Identity, c *TenantConfig) (*TenantConfig, error) {
	if c == nil || c.ID <= 0 {
		return nil, ErrTenantConfigInvalidArgument
	}
	if err := validateTenantConfig(c, true); err != nil {
		return nil, err
	}
	c.Name = strings.TrimSpace(c.Name)
	return uc.repo.Update(ctx, auth, c)
}

// DeleteTenantConfig 软删除租户配置；目标不存在、跨租户或已软删由仓储
// 映射 NotFound。
func (uc *TenantConfigUsecase) DeleteTenantConfig(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrTenantConfigInvalidArgument
	}
	return uc.repo.Delete(ctx, auth, id)
}

// FeatureFlag 是功能开关领域模型。租户边界由存储层 RLS 承担（表无
// @scope，租户内全可见），TenantID 由服务端按认证身份落值、只读。
// flag_key（租户内未删除行间唯一）创建后不可变；IsEnabled 是功能开关的
// 业务取值（创建缺省 false）；Status 是行状态（0 禁用、1 启用）。
type FeatureFlag struct {
	ID          int64
	Status      int32 // 0 禁用、1 启用
	Name        string
	FlagKey     string
	Description *string // 可空
	IsEnabled   bool
	Remark      *string // 可空
	TenantID    string  // 只读：服务端落值

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// FeatureFlagList 是功能开关仓储 List 返回的分页信封。
type FeatureFlagList struct {
	Items      []*FeatureFlag
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// Translation 是通用翻译领域模型。租户边界由存储层 RLS 承担（表无
// @scope，租户内全可见），TenantID 由服务端按认证身份落值、只读。
// TableName/RowID/ColumnName/Locale 共同构成身份四元组（租户内未删除
// 行间唯一），创建后不可变；Value 是翻译文本，原样透传但落库前去空白。
type Translation struct {
	ID         int64
	Status     int32 // 0 禁用、1 启用
	TableName  string
	RowID      int64
	ColumnName string
	Locale     string
	Value      string
	Remark     *string // 可空
	TenantID   string  // 只读：服务端落值

	CreatedBy int64
	UpdatedBy int64
	CreatedAt time.Time
	UpdatedAt *time.Time
	DeletedAt *time.Time
}

// TranslationList 是翻译仓储 List 返回的分页信封。
type TranslationList struct {
	Items      []*Translation
	Total      *int64 // include_total=false 时为 nil
	HasMore    bool
	NextCursor string
}

// FeatureFlagRepo 是功能开关仓储接口。租户边界由存储层会话门面在租户
// 事务内执行（RLS），写路径为普通 DML，故方法名一律不带 Super。
// TenantID 由仓储从会话 app.tenant_id 落值，不取请求值。
type FeatureFlagRepo interface {
	// Get 按 id 查询单个功能开关；不存在、跨租户或已软删返回
	// ErrFeatureFlagNotFound。
	Get(ctx context.Context, auth Identity, id int64) (*FeatureFlag, error)
	// List 按动态查询协议返回分页集合（租户内平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*FeatureFlagList, error)
	// Create 创建功能开关；租户内 flag_key 冲突映射
	// ErrFeatureFlagAlreadyExists。
	Create(ctx context.Context, auth Identity, f *FeatureFlag) (*FeatureFlag, error)
	// Update 覆盖可变字段（name、description、is_enabled、remark、status；
	// 不含 flag_key/tenant_id）。
	Update(ctx context.Context, auth Identity, f *FeatureFlag) (*FeatureFlag, error)
	// Delete 软删除功能开关（deleted_at 置位）。
	Delete(ctx context.Context, auth Identity, id int64) error
}

// TranslationRepo 是翻译仓储接口。租户边界由存储层会话门面在租户事务内
// 执行（RLS），写路径为普通 DML，故方法名一律不带 Super。TenantID 由
// 仓储从会话 app.tenant_id 落值，不取请求值。
type TranslationRepo interface {
	// Get 按 id 查询单个翻译；不存在、跨租户或已软删返回
	// ErrTranslationNotFound。
	Get(ctx context.Context, auth Identity, id int64) (*Translation, error)
	// List 按动态查询协议返回分页集合（租户内平铺视图）。
	List(ctx context.Context, auth Identity, q *dynq.Query) (*TranslationList, error)
	// Create 创建翻译；租户内身份四元组冲突映射
	// ErrTranslationAlreadyExists。
	Create(ctx context.Context, auth Identity, t *Translation) (*Translation, error)
	// Update 覆盖可变字段（status、value、remark；不含身份四元组与
	// tenant_id）。
	Update(ctx context.Context, auth Identity, t *Translation) (*Translation, error)
	// Delete 软删除翻译（deleted_at 置位）。
	Delete(ctx context.Context, auth Identity, id int64) error
}

// FeatureFlagUsecase 是功能开关用例。
type FeatureFlagUsecase struct {
	repo FeatureFlagRepo
}

// NewFeatureFlagUsecase 构造功能开关用例。
func NewFeatureFlagUsecase(repo FeatureFlagRepo) *FeatureFlagUsecase {
	return &FeatureFlagUsecase{repo: repo}
}

// GetFeatureFlag 按 id 查询功能开关。
func (uc *FeatureFlagUsecase) GetFeatureFlag(ctx context.Context, auth Identity, id int64) (*FeatureFlag, error) {
	if id <= 0 {
		return nil, ErrFeatureFlagInvalidArgument
	}
	return uc.repo.Get(ctx, auth, id)
}

// ListFeatureFlags 经动态查询协议列出功能开关（租户内平铺分页视图）。
func (uc *FeatureFlagUsecase) ListFeatureFlags(ctx context.Context, auth Identity, q *dynq.Query) (*FeatureFlagList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateFeatureFlag 创建功能开关：status 未指定（哨兵）缺省启用、显式 0
// 原样落库为禁用；is_enabled 未设置由传输层落 false。name/flag_key 落库
// 前去除首尾空白（与列宽校验口径一致，避免「校验按 trim 后长度、落库却
// 是原值」触发库层 22001）；tenant_id 不取入参，flag_key 创建时按入参
// trim 后写入。
func (uc *FeatureFlagUsecase) CreateFeatureFlag(ctx context.Context, auth Identity, f *FeatureFlag) (*FeatureFlag, error) {
	if err := validateFeatureFlag(f, false); err != nil {
		return nil, err
	}
	f.Name = strings.TrimSpace(f.Name)
	f.FlagKey = strings.TrimSpace(f.FlagKey)
	f.TenantID = ""
	if f.Status == ConfigStatusUnspecified {
		f.Status = ConfigStatusEnabled
	}
	return uc.repo.Create(ctx, auth, f)
}

// UpdateFeatureFlag 覆盖可变字段（name、description、is_enabled、remark、
// status）。flag_key 是身份列、tenant_id 是归属列，创建后不变，在输入中
// 被清除。name 落库前去除首尾空白（口径同创建）。
func (uc *FeatureFlagUsecase) UpdateFeatureFlag(ctx context.Context, auth Identity, f *FeatureFlag) (*FeatureFlag, error) {
	if f == nil || f.ID <= 0 {
		return nil, ErrFeatureFlagInvalidArgument
	}
	if err := validateFeatureFlag(f, true); err != nil {
		return nil, err
	}
	f.Name = strings.TrimSpace(f.Name)
	f.FlagKey = ""
	f.TenantID = ""
	return uc.repo.Update(ctx, auth, f)
}

// DeleteFeatureFlag 软删除功能开关；目标不存在、跨租户或已软删由仓储
// 映射 NotFound。
func (uc *FeatureFlagUsecase) DeleteFeatureFlag(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrFeatureFlagInvalidArgument
	}
	return uc.repo.Delete(ctx, auth, id)
}

// TranslationUsecase 是翻译用例。
type TranslationUsecase struct {
	repo TranslationRepo
}

// NewTranslationUsecase 构造翻译用例。
func NewTranslationUsecase(repo TranslationRepo) *TranslationUsecase {
	return &TranslationUsecase{repo: repo}
}

// GetTranslation 按 id 查询翻译。
func (uc *TranslationUsecase) GetTranslation(ctx context.Context, auth Identity, id int64) (*Translation, error) {
	if id <= 0 {
		return nil, ErrTranslationInvalidArgument
	}
	return uc.repo.Get(ctx, auth, id)
}

// ListTranslations 经动态查询协议列出翻译（租户内平铺分页视图）。
func (uc *TranslationUsecase) ListTranslations(ctx context.Context, auth Identity, q *dynq.Query) (*TranslationList, error) {
	return uc.repo.List(ctx, auth, q)
}

// CreateTranslation 创建翻译：status 未指定（哨兵）缺省启用、显式 0 原样
// 落库为禁用。table_name/column_name/locale/value 落库前去除首尾空白（与
// 校验口径一致）；value 内部空白原样保留（不做空白折叠）。
func (uc *TranslationUsecase) CreateTranslation(ctx context.Context, auth Identity, t *Translation) (*Translation, error) {
	if err := validateTranslation(t, false); err != nil {
		return nil, err
	}
	t.TableName = strings.TrimSpace(t.TableName)
	t.ColumnName = strings.TrimSpace(t.ColumnName)
	t.Locale = strings.TrimSpace(t.Locale)
	t.Value = strings.TrimSpace(t.Value)
	t.TenantID = ""
	if t.Status == ConfigStatusUnspecified {
		t.Status = ConfigStatusEnabled
	}
	return uc.repo.Create(ctx, auth, t)
}

// UpdateTranslation 覆盖可变字段（status、value、remark）。身份四元组与
// tenant_id 创建后不变，在输入中被清除；value 落库前去除首尾空白（口径
// 同创建）。
func (uc *TranslationUsecase) UpdateTranslation(ctx context.Context, auth Identity, t *Translation) (*Translation, error) {
	if t == nil || t.ID <= 0 {
		return nil, ErrTranslationInvalidArgument
	}
	if err := validateTranslation(t, true); err != nil {
		return nil, err
	}
	t.TableName = ""
	t.RowID = 0
	t.ColumnName = ""
	t.Locale = ""
	t.Value = strings.TrimSpace(t.Value)
	t.TenantID = ""
	return uc.repo.Update(ctx, auth, t)
}

// DeleteTranslation 软删除翻译；目标不存在、跨租户或已软删由仓储映射
// NotFound。
func (uc *TranslationUsecase) DeleteTranslation(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrTranslationInvalidArgument
	}
	return uc.repo.Delete(ctx, auth, id)
}

// 配置列宽约束，与 sys_config / sys_tenant_config 的 name/config_key/
// value_type/remark 列宽对齐（value 为 text，无列宽上限）。
const (
	configNameMaxRunes      = 128 // varchar(128)
	configKeyMaxRunes       = 128 // varchar(128)
	configValueTypeMaxRunes = 32  // varchar(32)
	configRemarkMaxRunes    = 512 // varchar(512)

	// 功能开关（sys_feature_flag）与通用翻译（sys_translation）列宽，
	// 与 schema 声明对齐。
	featureFlagNameMaxRunes        = 128 // varchar(128)
	featureFlagKeyMaxRunes         = 128 // varchar(128)
	featureFlagDescriptionMaxRunes = 512 // varchar(512)
	featureFlagRemarkMaxRunes      = 512 // varchar(512)

	translationTableNameMaxRunes  = 64  // varchar(64)
	translationColumnNameMaxRunes = 64  // varchar(64)
	translationLocaleMaxRunes     = 10  // varchar(10)
	translationRemarkMaxRunes     = 512 // varchar(512)
)

// translationLocalePattern 是 locale 的 BCP-47 基本形态校验（语言 2-3 个
// 字母，后接可选的大小写无关子标签，每段 1-8 位字母数字），如 zh-CN、
// en-US、ja-JP、ko-KR。总长由列宽单独限制（≤10）。
var translationLocalePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// validateConfig 校验全局配置入参：name 必填且不超列宽、remark 不超
// 列宽、status 合法（创建路径额外接受未指定哨兵，缺省由用例落启用；
// 更新路径必填，只认 0/1）。config_key/value_type 是身份/值语义列：
// 仅创建路径必填（value_type 限白名单），更新输入不携带，仓储不写
// 这两列。
func validateConfig(c *Config, update bool) error {
	if c == nil ||
		strings.TrimSpace(c.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.Name)) > configNameMaxRunes ||
		(c.Remark != nil && utf8.RuneCountInString(*c.Remark) > configRemarkMaxRunes) {
		return ErrConfigInvalidArgument
	}
	if !update && (strings.TrimSpace(c.ConfigKey) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.ConfigKey)) > configKeyMaxRunes ||
		strings.TrimSpace(c.ValueType) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.ValueType)) > configValueTypeMaxRunes ||
		!slices.Contains(ConfigValueTypeWhiteList, c.ValueType)) {
		return ErrConfigInvalidArgument
	}
	return validateConfigStatus(c.Status, update, ErrConfigInvalidArgument)
}

// validateTenantConfig 校验租户配置入参，口径同 validateConfig；tenant_id
// 是只读归属列，不参与校验（仓储按会话落值）。
func validateTenantConfig(c *TenantConfig, update bool) error {
	if c == nil ||
		strings.TrimSpace(c.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.Name)) > configNameMaxRunes ||
		(c.Remark != nil && utf8.RuneCountInString(*c.Remark) > configRemarkMaxRunes) {
		return ErrTenantConfigInvalidArgument
	}
	if !update && (strings.TrimSpace(c.ConfigKey) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.ConfigKey)) > configKeyMaxRunes ||
		strings.TrimSpace(c.ValueType) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(c.ValueType)) > configValueTypeMaxRunes ||
		!slices.Contains(ConfigValueTypeWhiteList, c.ValueType)) {
		return ErrTenantConfigInvalidArgument
	}
	return validateConfigStatus(c.Status, update, ErrTenantConfigInvalidArgument)
}

// validateConfigStatus 校验配置族共用的 status 取值：更新路径必填（只认
// 0/1），创建路径额外接受未指定哨兵（缺省由用例落启用）。
func validateConfigStatus(status int32, update bool, invalid error) error {
	switch {
	case update:
		if status != ConfigStatusDisabled && status != ConfigStatusEnabled {
			return invalid
		}
	case status != ConfigStatusUnspecified &&
		status != ConfigStatusDisabled && status != ConfigStatusEnabled:
		return invalid
	}
	return nil
}

// validateFeatureFlag 校验功能开关入参：name 必填且不超列宽、description
// 与 remark 不超列宽、status 取值合法（创建可省略、更新必填）。flag_key
// 是身份列：仅创建路径必填，更新输入不携带，仓储不写该列。
func validateFeatureFlag(f *FeatureFlag, update bool) error {
	if f == nil ||
		strings.TrimSpace(f.Name) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(f.Name)) > featureFlagNameMaxRunes ||
		(f.Description != nil && utf8.RuneCountInString(*f.Description) > featureFlagDescriptionMaxRunes) ||
		(f.Remark != nil && utf8.RuneCountInString(*f.Remark) > featureFlagRemarkMaxRunes) {
		return ErrFeatureFlagInvalidArgument
	}
	if !update && (strings.TrimSpace(f.FlagKey) == "" ||
		utf8.RuneCountInString(strings.TrimSpace(f.FlagKey)) > featureFlagKeyMaxRunes) {
		return ErrFeatureFlagInvalidArgument
	}
	return validateConfigStatus(f.Status, update, ErrFeatureFlagInvalidArgument)
}

// validateTranslation 校验翻译入参：value 必填（trim 后非空）、remark 不
// 超列宽、status 取值合法（创建可省略、更新必填）；创建路径额外校验身份
// 四元组——table_name/column_name trim 后非空且不超列宽、row_id 为正、
// locale 匹配 BCP-47 基本形态且总长不超列宽。更新路径不校验身份列（服务
// 端不写）。
func validateTranslation(t *Translation, update bool) error {
	if t == nil {
		return ErrTranslationInvalidArgument
	}
	if !update {
		tableName := strings.TrimSpace(t.TableName)
		columnName := strings.TrimSpace(t.ColumnName)
		locale := strings.TrimSpace(t.Locale)
		if tableName == "" ||
			utf8.RuneCountInString(tableName) > translationTableNameMaxRunes ||
			columnName == "" ||
			utf8.RuneCountInString(columnName) > translationColumnNameMaxRunes ||
			t.RowID <= 0 ||
			locale == "" ||
			utf8.RuneCountInString(locale) > translationLocaleMaxRunes ||
			!translationLocalePattern.MatchString(locale) {
			return ErrTranslationInvalidArgument
		}
	}
	if strings.TrimSpace(t.Value) == "" {
		return ErrTranslationInvalidArgument
	}
	if t.Remark != nil && utf8.RuneCountInString(*t.Remark) > translationRemarkMaxRunes {
		return ErrTranslationInvalidArgument
	}
	return validateConfigStatus(t.Status, update, ErrTranslationInvalidArgument)
}
