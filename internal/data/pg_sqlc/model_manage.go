package pg_sqlc

import (
	"context"
	"errors"
	"strings"

	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// modelRepo 是 biz.ModelRepo 的 PostgreSQL 实现。租户行走普通 DML（RLS
// 只放行本租户行）；平台行（tenant_id=''）经 98_model_write.sql 的
// SECURITY DEFINER 函数写入。
type modelRepo struct {
	data *Data
}

// NewModelRepo 构造模型目录仓储。
func NewModelRepo(data *Data) biz.ModelRepo {
	return &modelRepo{data: data}
}

func toBizModel(po *gen.SysModel) *biz.ModelCfg {
	if po == nil {
		return nil
	}
	hasCipher := po.ApiKeyCipher != nil && strings.TrimSpace(*po.ApiKeyCipher) != ""
	hasEnv := po.ApiKeyEnv != nil && strings.TrimSpace(*po.ApiKeyEnv) != ""
	return &biz.ModelCfg{
		ID:              po.ID,
		Platform:        po.TenantID == "",
		Status:          int32(po.Status),
		Kind:            po.Kind,
		Name:            po.Name,
		ModelKey:        po.ModelKey,
		Provider:        po.Provider,
		BaseURL:         po.BaseUrl,
		Model:           po.Model,
		Dimension:       po.Dimension,
		TimeoutMs:       po.TimeoutMs,
		ContextWindow:   po.ContextWindow,
		MaxOutputTokens: po.MaxOutputTokens,
		HasAPIKey:       hasCipher || hasEnv,
		APIKeyEnv:       po.ApiKeyEnv,
		Remark:          po.Remark,
		CreatedBy:       po.CreatedBy,
		UpdatedBy:       po.UpdatedBy,
		CreatedAt:       tsToTime(po.CreatedAt),
		UpdatedAt:       tsToTimePtr(po.UpdatedAt),
		DeletedAt:       tsToTimePtr(po.DeletedAt),
	}
}

func cipherPtr(cipher string) *string {
	if strings.TrimSpace(cipher) == "" {
		return nil
	}
	return &cipher
}

// GetModel 按 id 读取本租户行或平台行。
func (r *modelRepo) GetModel(ctx context.Context, auth biz.Identity, id int64) (*biz.ModelCfg, error) {
	var out *biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().GetSysModelByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizModel(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListModels 读取可见模型（本租户行 + 平台行）。
func (r *modelRepo) ListModels(ctx context.Context, auth biz.Identity, kind string) ([]*biz.ModelCfg, error) {
	var out []*biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		var kindArg *string
		if kind != "" {
			kindArg = &kind
		}
		rows, err := s.Q().ListSysModelAll(ctx, kindArg)
		if err != nil {
			return err
		}
		out = make([]*biz.ModelCfg, 0, len(rows))
		for i := range rows {
			out = append(out, toBizModel(&rows[i]))
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// ListModelOptions 读取可见且启用的模型选项；kind 为空时合并全部类别。
func (r *modelRepo) ListModelOptions(ctx context.Context, auth biz.Identity, kind string) ([]*biz.ModelCfg, error) {
	kinds := []string{kind}
	if kind == "" {
		kinds = []string{biz.ModelKindText, biz.ModelKindEmbedding, biz.ModelKindRerank}
	}
	var out []*biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		for _, k := range kinds {
			rows, err := s.Q().ListSysModelOptions(ctx, k)
			if err != nil {
				return err
			}
			for i := range rows {
				out = append(out, toBizModel(&rows[i]))
			}
		}
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// LockModel 锁定目标行并返回当前密钥密文：先按本租户行加锁；未命中时
// 读取平台行（平台行写入由受控函数在 UPDATE 内完成锁定）。
func (r *modelRepo) LockModel(ctx context.Context, auth biz.Identity, id int64) (*biz.ModelCfg, string, error) {
	var out *biz.ModelCfg
	var cipher string
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().LockSysModelTenantByID(ctx, id)
		if err == nil {
			out = toBizModel(&row)
			cipher = stringOrEmpty(row.ApiKeyCipher)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		platform, err := s.Q().GetSysModelByID(ctx, id)
		if err != nil {
			return err
		}
		out = toBizModel(&platform)
		cipher = stringOrEmpty(platform.ApiKeyCipher)
		return nil
	})
	if err != nil {
		return nil, "", r.mapErr(err)
	}
	return out, cipher, nil
}

// CreateModel 创建租户模型行。
func (r *modelRepo) CreateModel(ctx context.Context, auth biz.Identity, m *biz.ModelCfg, apiKeyCipher string) (*biz.ModelCfg, error) {
	var out *biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysModelTenant(ctx, gen.InsertSysModelTenantParams{
			ID:              newID(),
			Status:          int16(m.Status),
			Kind:            m.Kind,
			Name:            m.Name,
			ModelKey:        m.ModelKey,
			Provider:        m.Provider,
			BaseUrl:         m.BaseURL,
			Model:           m.Model,
			Dimension:       m.Dimension,
			TimeoutMs:       m.TimeoutMs,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			ApiKeyCipher:    cipherPtr(apiKeyCipher),
			ApiKeyEnv:       m.APIKeyEnv,
			Remark:          m.Remark,
			CreatedBy:       auth.UserID,
			UpdatedBy:       auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizModel(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateModel 覆盖租户模型行可变字段：先锁目标行。
func (r *modelRepo) UpdateModel(ctx context.Context, auth biz.Identity, m *biz.ModelCfg, apiKeyCipher string) (*biz.ModelCfg, error) {
	var out *biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysModelTenantByID(ctx, m.ID); err != nil {
			return err
		}
		row, err := s.Q().UpdateSysModelTenant(ctx, gen.UpdateSysModelTenantParams{
			Status:          int16(m.Status),
			Name:            m.Name,
			Provider:        m.Provider,
			BaseUrl:         m.BaseURL,
			Model:           m.Model,
			Dimension:       m.Dimension,
			TimeoutMs:       m.TimeoutMs,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			ApiKeyCipher:    cipherPtr(apiKeyCipher),
			ApiKeyEnv:       m.APIKeyEnv,
			Remark:          m.Remark,
			UpdatedBy:       auth.UserID,
			ID:              m.ID,
		})
		if err != nil {
			return err
		}
		out = toBizModel(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// DeleteModel 软删除租户模型行。
func (r *modelRepo) DeleteModel(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		if _, err := s.Q().LockSysModelTenantByID(ctx, id); err != nil {
			return err
		}
		rows, err := s.Q().SoftDeleteSysModelTenant(ctx, gen.SoftDeleteSysModelTenantParams{UpdatedBy: auth.UserID, ID: id})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// CreateModelPlatform 创建平台模型行（受控函数）。
func (r *modelRepo) CreateModelPlatform(ctx context.Context, auth biz.Identity, m *biz.ModelCfg, apiKeyCipher string) (*biz.ModelCfg, error) {
	var out *biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().InsertSysModelPlatform(ctx, gen.InsertSysModelPlatformParams{
			ID:              newID(),
			Status:          int16(m.Status),
			Kind:            m.Kind,
			Name:            m.Name,
			ModelKey:        m.ModelKey,
			Provider:        m.Provider,
			BaseUrl:         m.BaseURL,
			Model:           m.Model,
			Dimension:       m.Dimension,
			TimeoutMs:       m.TimeoutMs,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			ApiKeyCipher:    cipherPtr(apiKeyCipher),
			ApiKeyEnv:       m.APIKeyEnv,
			Remark:          m.Remark,
			Operator:        auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizModel(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// UpdateModelPlatform 覆盖平台模型行可变字段（受控函数，未命中 NotFound）。
func (r *modelRepo) UpdateModelPlatform(ctx context.Context, auth biz.Identity, m *biz.ModelCfg, apiKeyCipher string) (*biz.ModelCfg, error) {
	var out *biz.ModelCfg
	err := r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		row, err := s.Q().UpdateSysModelPlatform(ctx, gen.UpdateSysModelPlatformParams{
			ID:              m.ID,
			Status:          int16(m.Status),
			Name:            m.Name,
			Provider:        m.Provider,
			BaseUrl:         m.BaseURL,
			Model:           m.Model,
			Dimension:       m.Dimension,
			TimeoutMs:       m.TimeoutMs,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
			ApiKeyCipher:    cipherPtr(apiKeyCipher),
			ApiKeyEnv:       m.APIKeyEnv,
			Remark:          m.Remark,
			Operator:        auth.UserID,
		})
		if err != nil {
			return err
		}
		out = toBizModel(&row)
		return nil
	})
	if err != nil {
		return nil, r.mapErr(err)
	}
	return out, nil
}

// DeleteModelPlatform 软删除平台模型行（受控函数）。
func (r *modelRepo) DeleteModelPlatform(ctx context.Context, auth biz.Identity, id int64) error {
	return r.mapErr(r.data.IdentityTx(ctx, pgAuth(auth), func(s *IdentityTx) error {
		rows, err := s.Q().SoftDeleteSysModelPlatform(ctx, gen.SoftDeleteSysModelPlatformParams{ID: id, Operator: auth.UserID})
		if err != nil {
			return err
		}
		if rows == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}))
}

// mapErr 把驱动错误映射为模型目录领域错误。
func (r *modelRepo) mapErr(err error) error {
	return mapStorageError(err, biz.ErrModelNotFound, biz.ErrModelInvalidArgument, biz.ErrModelAlreadyExists)
}
