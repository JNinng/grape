package pg_sqlc

import (
	"context"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// configRepo 与 tenantConfigRepo 是 biz.ConfigRepo / biz.TenantConfigRepo 的
// PostgreSQL 实现。sys_config 是平台级全局表（无 tenant_id、无 RLS），写经
// 受控写函数（98_config_write.sql）；sys_tenant_config 是租户表，读写走
// 普通 DML/动态 List，租户边界由 RLS 承担，tenant_id 一律取会话。

// configRepo 是 biz.ConfigRepo 的 PostgreSQL 实现。
type configRepo struct {
	data *Data
}

// NewConfigRepo 构造全局配置仓储。
func NewConfigRepo(data *Data) biz.ConfigRepo {
	return &configRepo{data: data}
}

// tenantConfigRepo 是 biz.TenantConfigRepo 的 PostgreSQL 实现。
type tenantConfigRepo struct {
	data *Data
}

// NewTenantConfigRepo 构造租户配置仓储。
func NewTenantConfigRepo(data *Data) biz.TenantConfigRepo {
	return &tenantConfigRepo{data: data}
}

// toBizConfig 将持久化全局配置转换为领域模型。
func toBizConfig(po *gen.SysConfig) *biz.Config {
	if po == nil {
		return nil
	}
	return &biz.Config{
		ID:        po.ID,
		Status:    int32(po.Status),
		Name:      po.Name,
		ConfigKey: po.ConfigKey,
		Value:     po.Value,
		ValueType: po.ValueType,
		Remark:    po.Remark,
		CreatedBy: po.CreatedBy,
		UpdatedBy: po.UpdatedBy,
		CreatedAt: tsToTime(po.CreatedAt),
		UpdatedAt: tsToTimePtr(po.UpdatedAt),
		DeletedAt: tsToTimePtr(po.DeletedAt),
	}
}

// toBizTenantConfig 将持久化租户配置转换为领域模型。
func toBizTenantConfig(po *gen.SysTenantConfig) *biz.TenantConfig {
	if po == nil {
		return nil
	}
	return &biz.TenantConfig{
		ID:        po.ID,
		Status:    int32(po.Status),
		Name:      po.Name,
		ConfigKey: po.ConfigKey,
		Value:     po.Value,
		ValueType: po.ValueType,
		Remark:    po.Remark,
		TenantID:  po.TenantID,
		CreatedBy: po.CreatedBy,
		UpdatedBy: po.UpdatedBy,
		CreatedAt: tsToTime(po.CreatedAt),
		UpdatedAt: tsToTimePtr(po.UpdatedAt),
		DeletedAt: tsToTimePtr(po.DeletedAt),
	}
}

// Get 按主键读取全局配置（sys_config 无 RLS，已软删等同不存在）。
func (r *configRepo) Get(ctx context.Context, auth biz.Identity, id int64) (*biz.Config, error) {
	var out *biz.Config
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysConfigByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；表无 @scope 与 RLS，软删过滤由
// 表级 @filter 声明在解析期作为服务端固定不变量注入，字段、操作符、
// 投影与游标合法性由生成代码校验，协议错误映射为
// ErrConfigInvalidArgument。
func (r *configRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.ConfigList, error) {
	var out *biz.ConfigList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysConfigQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysConfig(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.ConfigList{
			Items:      make([]*biz.Config, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			list.Items = append(list.Items, toBizConfig(&res.Items[i]))
		}
		// total 与翻页位置无关，计数前剥离游标：游标会把行集收窄到
		// 某一页位置之后，导致各页 total 递减。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysConfig(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// CreateSuper 创建全局配置（受控写面）：主键由应用层发号（newID），审计
// 列记操作者；未删除行间 config_key 唯一冲突映射为
// ErrConfigAlreadyExists。
func (r *configRepo) CreateSuper(ctx context.Context, auth biz.Identity, c *biz.Config) (*biz.Config, error) {
	var out *biz.Config
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysConfig(ctx, gen.InsertSysConfigParams{
			ID:        newID(),
			Status:    int16(c.Status),
			Name:      c.Name,
			ConfigKey: c.ConfigKey,
			Value:     c.Value,
			ValueType: c.ValueType,
			Remark:    c.Remark,
			Operator:  auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateSuper 覆盖可变字段（name、status、value、remark；不含
// config_key/value_type 身份/值语义列）。受控函数对未命中目标返回
// NULL——生成代码按无行扫描失败，映射为 NotFound。
func (r *configRepo) UpdateSuper(ctx context.Context, auth biz.Identity, c *biz.Config) (*biz.Config, error) {
	var out *biz.Config
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysConfig(ctx, gen.UpdateSysConfigParams{
			ID:       c.ID,
			Status:   int16(c.Status),
			Name:     c.Name,
			Value:    c.Value,
			Remark:   c.Remark,
			Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// DeleteSuper 软删除全局配置。受控函数返回删除行数，0 即目标未命中
// （映射 NotFound）。
func (r *configRepo) DeleteSuper(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().SoftDeleteSysConfig(ctx, gen.SoftDeleteSysConfigParams{
			ID:       id,
			Operator: auth.UserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 将驱动与协议错误映射为全局配置的领域错误：无行（含已软删
// 等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束
// 冲突 → AlreadyExists；其余原样透传。
func (r *configRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrConfigNotFound, biz.ErrConfigInvalidArgument, biz.ErrConfigAlreadyExists)
}

// Get 按主键读取租户配置。租户边界由 RLS 承担，跨租户等同不存在。
func (r *tenantConfigRepo) Get(ctx context.Context, auth biz.Identity, id int64) (*biz.TenantConfig, error) {
	var out *biz.TenantConfig
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysTenantConfigByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizTenantConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封，口径同全局配置 List；租户边界
// 由 RLS 承担（表无 @scope，租户内全可见），协议错误映射为
// ErrTenantConfigInvalidArgument。
func (r *tenantConfigRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.TenantConfigList, error) {
	var out *biz.TenantConfigList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysTenantConfigQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysTenantConfig(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.TenantConfigList{
			Items:      make([]*biz.TenantConfig, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			// tenant_id 是 @internal 列，动态投影不返回；按认证身份回填，
			// 与 Get/Create/Update 的响应契约保持一致。
			item := toBizTenantConfig(&res.Items[i])
			item.TenantID = auth.TenantID
			list.Items = append(list.Items, item)
		}
		// total 与翻页位置无关，计数前剥离游标（口径同全局配置）。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysTenantConfig(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Create 创建租户配置：主键由应用层发号（newID），租户由 SQL 取会话
// app.tenant_id 落值（不取入参），审计列记操作者；租户内未删除配置间
// config_key 唯一冲突映射为 ErrTenantConfigAlreadyExists。
func (r *tenantConfigRepo) Create(ctx context.Context, auth biz.Identity, c *biz.TenantConfig) (*biz.TenantConfig, error) {
	var out *biz.TenantConfig
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysTenantConfig(ctx, gen.InsertSysTenantConfigParams{
			ID:        newID(),
			Status:    int16(c.Status),
			Name:      c.Name,
			ConfigKey: c.ConfigKey,
			Value:     c.Value,
			ValueType: c.ValueType,
			Remark:    c.Remark,
			CreatedBy: auth.UserID,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizTenantConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（name、status、value、remark）；目标不存在、跨
// 租户或已软删时无行扫描失败（映射 NotFound）——单条 UPDATE 同时完成
// 行锁、租户边界（RLS）与目标命中判定，不另做取行判定。
func (r *tenantConfigRepo) Update(ctx context.Context, auth biz.Identity, c *biz.TenantConfig) (*biz.TenantConfig, error) {
	var out *biz.TenantConfig
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysTenantConfig(ctx, gen.UpdateSysTenantConfigParams{
			ID:        c.ID,
			Status:    int16(c.Status),
			Name:      c.Name,
			Value:     c.Value,
			Remark:    c.Remark,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizTenantConfig(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Delete 软删除租户配置；更新 0 行即目标不存在、跨租户或已软删（映射
// NotFound）。
func (r *tenantConfigRepo) Delete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().SoftDeleteSysTenantConfig(ctx, gen.SoftDeleteSysTenantConfigParams{
			ID:        id,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 将驱动与协议错误映射为租户配置的领域错误，口径同全局配置。
func (r *tenantConfigRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrTenantConfigNotFound, biz.ErrTenantConfigInvalidArgument, biz.ErrTenantConfigAlreadyExists)
}

// featureFlagRepo 与 translationRepo 是 biz.FeatureFlagRepo /
// biz.TranslationRepo 的 PostgreSQL 实现。两表均为租户表：读写都走普通
// DML / 动态 List，租户边界由 RLS 承担，tenant_id 一律由会话
// app.tenant_id 落值（不取请求值）；唯一约束限定未删除行间，软删后身份
// 列可复用。

// featureFlagRepo 是 biz.FeatureFlagRepo 的 PostgreSQL 实现。
type featureFlagRepo struct {
	data *Data
}

// NewFeatureFlagRepo 构造功能开关仓储。
func NewFeatureFlagRepo(data *Data) biz.FeatureFlagRepo {
	return &featureFlagRepo{data: data}
}

// translationRepo 是 biz.TranslationRepo 的 PostgreSQL 实现。
type translationRepo struct {
	data *Data
}

// NewTranslationRepo 构造翻译仓储。
func NewTranslationRepo(data *Data) biz.TranslationRepo {
	return &translationRepo{data: data}
}

// toBizFeatureFlag 将持久化功能开关转换为领域模型。
func toBizFeatureFlag(po *gen.SysFeatureFlag) *biz.FeatureFlag {
	if po == nil {
		return nil
	}
	return &biz.FeatureFlag{
		ID:          po.ID,
		Status:      int32(po.Status),
		Name:        po.Name,
		FlagKey:     po.FlagKey,
		Description: po.Description,
		IsEnabled:   po.IsEnabled,
		Remark:      po.Remark,
		TenantID:    po.TenantID,
		CreatedBy:   po.CreatedBy,
		UpdatedBy:   po.UpdatedBy,
		CreatedAt:   tsToTime(po.CreatedAt),
		UpdatedAt:   tsToTimePtr(po.UpdatedAt),
		DeletedAt:   tsToTimePtr(po.DeletedAt),
	}
}

// toBizTranslation 将持久化翻译转换为领域模型。
func toBizTranslation(po *gen.SysTranslation) *biz.Translation {
	if po == nil {
		return nil
	}
	return &biz.Translation{
		ID:         po.ID,
		Status:     int32(po.Status),
		TableName:  po.TableName,
		RowID:      po.RowID,
		ColumnName: po.ColumnName,
		Locale:     po.Locale,
		Value:      po.Value,
		Remark:     po.Remark,
		TenantID:   po.TenantID,
		CreatedBy:  po.CreatedBy,
		UpdatedBy:  po.UpdatedBy,
		CreatedAt:  tsToTime(po.CreatedAt),
		UpdatedAt:  tsToTimePtr(po.UpdatedAt),
		DeletedAt:  tsToTimePtr(po.DeletedAt),
	}
}

// Get 按主键读取功能开关。租户边界由 RLS 承担，跨租户等同不存在。
func (r *featureFlagRepo) Get(ctx context.Context, auth biz.Identity, id int64) (*biz.FeatureFlag, error) {
	var out *biz.FeatureFlag
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysFeatureFlagByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizFeatureFlag(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；租户边界由 RLS 承担（表无
// @scope，租户内全可见），协议错误映射为 ErrFeatureFlagInvalidArgument。
func (r *featureFlagRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.FeatureFlagList, error) {
	var out *biz.FeatureFlagList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysFeatureFlagQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysFeatureFlag(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.FeatureFlagList{
			Items:      make([]*biz.FeatureFlag, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			// tenant_id 是 @internal 列，动态投影不返回；按认证身份回填。
			item := toBizFeatureFlag(&res.Items[i])
			item.TenantID = auth.TenantID
			list.Items = append(list.Items, item)
		}
		// total 与翻页位置无关，计数前剥离游标（口径同配置族）。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysFeatureFlag(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Create 创建功能开关：主键由应用层发号（newID），租户由 SQL 取会话
// app.tenant_id 落值（不取入参），审计列记操作者；租户内未删除开关间
// flag_key 唯一冲突映射为 ErrFeatureFlagAlreadyExists。
func (r *featureFlagRepo) Create(ctx context.Context, auth biz.Identity, f *biz.FeatureFlag) (*biz.FeatureFlag, error) {
	var out *biz.FeatureFlag
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysFeatureFlag(ctx, gen.InsertSysFeatureFlagParams{
			ID:          newID(),
			Status:      int16(f.Status),
			Name:        f.Name,
			FlagKey:     f.FlagKey,
			Description: f.Description,
			IsEnabled:   f.IsEnabled,
			Remark:      f.Remark,
			CreatedBy:   auth.UserID,
			UpdatedBy:   auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizFeatureFlag(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（name、description、is_enabled、remark、status）；
// 目标不存在、跨租户或已软删时无行扫描失败（映射 NotFound）——单条
// UPDATE 同时完成行锁、租户边界（RLS）与目标命中判定。
func (r *featureFlagRepo) Update(ctx context.Context, auth biz.Identity, f *biz.FeatureFlag) (*biz.FeatureFlag, error) {
	var out *biz.FeatureFlag
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysFeatureFlag(ctx, gen.UpdateSysFeatureFlagParams{
			ID:          f.ID,
			Status:      int16(f.Status),
			Name:        f.Name,
			Description: f.Description,
			IsEnabled:   f.IsEnabled,
			Remark:      f.Remark,
			UpdatedBy:   auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizFeatureFlag(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Delete 软删除功能开关；更新 0 行即目标不存在、跨租户或已软删（映射
// NotFound）。
func (r *featureFlagRepo) Delete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().SoftDeleteSysFeatureFlag(ctx, gen.SoftDeleteSysFeatureFlagParams{
			ID:        id,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 将驱动与协议错误映射为功能开关的领域错误：无行（含已软删
// 等同不存在）→ NotFound；dynq 协议错误 → InvalidArgument；唯一约束
// 冲突 → AlreadyExists；其余原样透传。
func (r *featureFlagRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrFeatureFlagNotFound, biz.ErrFeatureFlagInvalidArgument, biz.ErrFeatureFlagAlreadyExists)
}

// Get 按主键读取翻译。租户边界由 RLS 承担，跨租户等同不存在。
func (r *translationRepo) Get(ctx context.Context, auth biz.Identity, id int64) (*biz.Translation, error) {
	var out *biz.Translation
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysTranslationByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizTranslation(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// List 解析动态查询协议并返回分页信封；租户边界由 RLS 承担（表无
// @scope，租户内全可见），协议错误映射为
// ErrTranslationInvalidArgument。
func (r *translationRepo) List(ctx context.Context, auth biz.Identity, q *dynq.Query) (*biz.TranslationList, error) {
	var out *biz.TranslationList
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		params, err := gen.ParseSysTranslationQuery(q)
		if err != nil {
			return err
		}
		res, err := gen.ListSysTranslation(ctx, s.Tx(), params)
		if err != nil {
			return err
		}
		list := &biz.TranslationList{
			Items:      make([]*biz.Translation, 0, len(res.Items)),
			HasMore:    res.HasMore,
			NextCursor: res.NextCursor,
		}
		for i := range res.Items {
			// tenant_id 是 @internal 列，动态投影不返回；按认证身份回填。
			item := toBizTranslation(&res.Items[i])
			item.TenantID = auth.TenantID
			list.Items = append(list.Items, item)
		}
		// total 与翻页位置无关，计数前剥离游标（口径同配置族）。
		if dynq.IncludeTotalOf(q.Pagination) {
			countParams := params
			countParams.After = nil
			total, err := gen.CountSysTranslation(ctx, s.Tx(), countParams)
			if err != nil {
				return err
			}
			list.Total = &total
		}
		out = list
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Create 创建翻译：主键由应用层发号（newID），租户由 SQL 取会话
// app.tenant_id 落值（不取入参），审计列记操作者；租户内未删除翻译间
// 身份四元组唯一冲突映射为 ErrTranslationAlreadyExists。
func (r *translationRepo) Create(ctx context.Context, auth biz.Identity, t *biz.Translation) (*biz.Translation, error) {
	var out *biz.Translation
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysTranslation(ctx, gen.InsertSysTranslationParams{
			ID:         newID(),
			Status:     int16(t.Status),
			TableName:  t.TableName,
			RowID:      t.RowID,
			ColumnName: t.ColumnName,
			Locale:     t.Locale,
			Value:      t.Value,
			Remark:     t.Remark,
			CreatedBy:  auth.UserID,
			UpdatedBy:  auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizTranslation(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Update 覆盖可变字段（status、value、remark）；目标不存在、跨租户或已
// 软删时无行扫描失败（映射 NotFound）——单条 UPDATE 同时完成行锁、租户
// 边界（RLS）与目标命中判定。
func (r *translationRepo) Update(ctx context.Context, auth biz.Identity, t *biz.Translation) (*biz.Translation, error) {
	var out *biz.Translation
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysTranslation(ctx, gen.UpdateSysTranslationParams{
			ID:        t.ID,
			Status:    int16(t.Status),
			Value:     t.Value,
			Remark:    t.Remark,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizTranslation(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// Delete 软删除翻译；更新 0 行即目标不存在、跨租户或已软删（映射
// NotFound）。
func (r *translationRepo) Delete(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().SoftDeleteSysTranslation(ctx, gen.SoftDeleteSysTranslationParams{
			ID:        id,
			UpdatedBy: auth.UserID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 将驱动与协议错误映射为翻译的领域错误，口径同功能开关。
func (r *translationRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrTranslationNotFound, biz.ErrTranslationInvalidArgument, biz.ErrTranslationAlreadyExists)
}
