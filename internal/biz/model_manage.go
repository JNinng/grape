package biz

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	modelv1 "grape/api/model/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

var (
	// ErrModelNotFound 表示目标模型不存在（含跨租户不可见与平台行对租户
	// 不可写：两者都不区分，不泄露存在性）。
	ErrModelNotFound = kratoserrors.NotFound(modelv1.ErrorReason_MODEL_NOT_FOUND.String(), "model not found")
	// ErrModelInvalidArgument 表示模型请求参数非法。
	ErrModelInvalidArgument = kratoserrors.BadRequest(modelv1.ErrorReason_MODEL_INVALID_ARGUMENT.String(), "invalid model argument")
	// ErrModelAlreadyExists 表示租户/平台内未删除行间 model_key 唯一冲突。
	ErrModelAlreadyExists = kratoserrors.Conflict(modelv1.ErrorReason_MODEL_ALREADY_EXISTS.String(), "model already exists")
)

// modelKeyPattern 是模型键白名单：与 conf.vectorSpaceKeyPattern 一致
// （字母或数字开头，仅含字母数字与 . _ : @ -，128 字符内）。冒号承载
// Ollama 标签等模型名分隔符。
var modelKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// ModelStatusUnspecified 是创建入参的「未指定」哨兵（optional int32 的
// 缺失必须与显式 0=禁用可区分），不落库；用例落为缺省启用。
const ModelStatusUnspecified int32 = -1

// ModelCfg 是模型目录行的领域模型。Platform 由 tenant_id='' 推导，是
// 响应事实而非可写字段。HasAPIKey 由密文或环境变量名是否配置推导，接口
// 不返回密文与明文。
type ModelCfg struct {
	ID         int64
	Platform   bool
	Status     int32
	Kind       string
	Name       string
	ModelKey   string
	Provider   string
	BaseURL    *string
	Model      string
	Dimension  *int32
	TimeoutMs  *int32
	HasAPIKey  bool
	APIKeyEnv  *string
	Remark     *string
	CreatedBy  int64
	UpdatedBy  int64
	CreatedAt  time.Time
	UpdatedAt  *time.Time
	DeletedAt  *time.Time
}

// ModelRepo 是模型目录仓储接口。租户行与平台行分路径：平台行写经受控
// SECURITY DEFINER 函数（属主绕过 RLS），租户行走普通 DML（RLS 强制）。
type ModelRepo interface {
	// GetModel 按 id 读取本租户行或平台行。
	GetModel(ctx context.Context, auth Identity, id int64) (*ModelCfg, error)
	// ListModels 读取可见模型（本租户行 + 平台行），kind 为空返回全部。
	ListModels(ctx context.Context, auth Identity, kind string) ([]*ModelCfg, error)
	// ListModelOptions 读取可见且启用的模型选项（同键租户行覆盖平台行）。
	ListModelOptions(ctx context.Context, auth Identity, kind string) ([]*ModelCfg, error)
	// LockModel 锁定目标行并返回当前密钥密文（可为空串），供更新时保留
	// 未提供的密钥。
	LockModel(ctx context.Context, auth Identity, id int64) (*ModelCfg, string, error)

	CreateModel(ctx context.Context, auth Identity, m *ModelCfg, apiKeyCipher string) (*ModelCfg, error)
	UpdateModel(ctx context.Context, auth Identity, m *ModelCfg, apiKeyCipher string) (*ModelCfg, error)
	DeleteModel(ctx context.Context, auth Identity, id int64) error

	CreateModelPlatform(ctx context.Context, auth Identity, m *ModelCfg, apiKeyCipher string) (*ModelCfg, error)
	UpdateModelPlatform(ctx context.Context, auth Identity, m *ModelCfg, apiKeyCipher string) (*ModelCfg, error)
	DeleteModelPlatform(ctx context.Context, auth Identity, id int64) error
}

// ModelUsecase 是模型目录用例：入参校验、密钥加解密与掩码、维度纪律。
type ModelUsecase struct {
	repo   ModelRepo
	cipher SecretCipher
}

// NewModelUsecase 构造模型目录用例。
func NewModelUsecase(repo ModelRepo, cipher SecretCipher) *ModelUsecase {
	return &ModelUsecase{repo: repo, cipher: cipher}
}

// GetModel 按 id 查询模型。
func (uc *ModelUsecase) GetModel(ctx context.Context, auth Identity, id int64) (*ModelCfg, error) {
	if id <= 0 {
		return nil, ErrModelInvalidArgument
	}
	return uc.repo.GetModel(ctx, auth, id)
}

// ListModels 返回可见模型清单。
func (uc *ModelUsecase) ListModels(ctx context.Context, auth Identity, kind string) ([]*ModelCfg, error) {
	kind = strings.TrimSpace(kind)
	if kind != "" && kind != ModelKindText && kind != ModelKindEmbedding {
		return nil, ErrModelInvalidArgument
	}
	return uc.repo.ListModels(ctx, auth, kind)
}

// ListModelOptions 返回可见且启用的模型选项（知识库模型下拉用）。
func (uc *ModelUsecase) ListModelOptions(ctx context.Context, auth Identity, kind string) ([]*ModelCfg, error) {
	kind = strings.TrimSpace(kind)
	if kind != "" && kind != ModelKindText && kind != ModelKindEmbedding {
		return nil, ErrModelInvalidArgument
	}
	return uc.repo.ListModelOptions(ctx, auth, kind)
}

// CreateModel 创建租户模型行。apiKey 明文非空时加密落库；空值不配置密钥。
func (uc *ModelUsecase) CreateModel(ctx context.Context, auth Identity, m *ModelCfg, apiKey string) (*ModelCfg, error) {
	if err := validateModelCfg(m, false); err != nil {
		return nil, err
	}
	if m.Status == ModelStatusUnspecified {
		m.Status = 1
	}
	cipher, err := uc.sealOptionalKey(apiKey)
	if err != nil {
		return nil, err
	}
	m.Platform = false
	return uc.repo.CreateModel(ctx, auth, m, cipher)
}

// UpdateModel 覆盖租户模型行可变字段。apiKey 非 nil 且非空时替换密钥；
// clearApiKey 清除密钥；否则保留当前密文。
func (uc *ModelUsecase) UpdateModel(ctx context.Context, auth Identity, m *ModelCfg, apiKey *string, clearApiKey bool) (*ModelCfg, error) {
	if m == nil || m.ID <= 0 {
		return nil, ErrModelInvalidArgument
	}
	if err := validateModelCfg(m, true); err != nil {
		return nil, err
	}
	_, currentCipher, err := uc.repo.LockModel(ctx, auth, m.ID)
	if err != nil {
		return nil, err
	}
	cipher, err := uc.resolveUpdatedKey(apiKey, clearApiKey, currentCipher)
	if err != nil {
		return nil, err
	}
	return uc.repo.UpdateModel(ctx, auth, m, cipher)
}

// DeleteModel 软删除租户模型行。
func (uc *ModelUsecase) DeleteModel(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrModelInvalidArgument
	}
	return uc.repo.DeleteModel(ctx, auth, id)
}

// CreateModelPlatform 创建平台模型行（super 写面）。
func (uc *ModelUsecase) CreateModelPlatform(ctx context.Context, auth Identity, m *ModelCfg, apiKey string) (*ModelCfg, error) {
	if err := validateModelCfg(m, false); err != nil {
		return nil, err
	}
	if m.Status == ModelStatusUnspecified {
		m.Status = 1
	}
	cipher, err := uc.sealOptionalKey(apiKey)
	if err != nil {
		return nil, err
	}
	m.Platform = true
	return uc.repo.CreateModelPlatform(ctx, auth, m, cipher)
}

// UpdateModelPlatform 覆盖平台模型行可变字段（super 写面）。平台行不命
// 中返回 NotFound（租户行不能经本路径改写）。
func (uc *ModelUsecase) UpdateModelPlatform(ctx context.Context, auth Identity, m *ModelCfg, apiKey *string, clearApiKey bool) (*ModelCfg, error) {
	if m == nil || m.ID <= 0 {
		return nil, ErrModelInvalidArgument
	}
	if err := validateModelCfg(m, true); err != nil {
		return nil, err
	}
	existing, currentCipher, err := uc.repo.LockModel(ctx, auth, m.ID)
	if err != nil {
		return nil, err
	}
	if !existing.Platform {
		return nil, ErrModelNotFound
	}
	cipher, err := uc.resolveUpdatedKey(apiKey, clearApiKey, currentCipher)
	if err != nil {
		return nil, err
	}
	return uc.repo.UpdateModelPlatform(ctx, auth, m, cipher)
}

// DeleteModelPlatform 软删除平台模型行（super 写面）。
func (uc *ModelUsecase) DeleteModelPlatform(ctx context.Context, auth Identity, id int64) error {
	if id <= 0 {
		return ErrModelInvalidArgument
	}
	return uc.repo.DeleteModelPlatform(ctx, auth, id)
}

// ResolveModelKey 校验 kind 下是否存在可见且启用的 model_key（知识库
// embedding_model_key 保存时校验）。
func (uc *ModelUsecase) ResolveModelKey(ctx context.Context, auth Identity, kind, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	options, err := uc.repo.ListModelOptions(ctx, auth, kind)
	if err != nil {
		return err
	}
	for _, option := range options {
		if option.ModelKey == key {
			return nil
		}
	}
	return ErrModelInvalidArgument
}

// sealOptionalKey 加密非空白明文；空白不配置。
func (uc *ModelUsecase) sealOptionalKey(apiKey string) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", nil
	}
	return uc.cipher.Seal(apiKey)
}

// resolveUpdatedKey 计算更新后的密钥密文：clear 清空；提供非空白明文则
// 替换；否则保留当前密文。
func (uc *ModelUsecase) resolveUpdatedKey(apiKey *string, clear bool, current string) (string, error) {
	if clear {
		return "", nil
	}
	if apiKey != nil && strings.TrimSpace(*apiKey) != "" {
		return uc.cipher.Seal(*apiKey)
	}
	return current, nil
}

// 模型列宽约束，与 sys_model 列宽对齐。
const (
	modelNameMaxRunes    = 128
	modelKeyMaxRunes     = 128
	modelProviderMaxRunes = 32
	modelBaseURLMaxRunes = 512
	modelNameColMaxRunes = 128
	modelEnvMaxRunes     = 128
	modelRemarkMaxRunes  = 512
)

// validateModelCfg 校验模型可变字段：类别在枚举内、名称/模型名必填、
// model_key 必填且命中白名单（仅创建路径）、嵌入维度固定 1024、超时为正。
func validateModelCfg(m *ModelCfg, update bool) error {
	if m == nil {
		return ErrModelInvalidArgument
	}
	if m.Kind != ModelKindText && m.Kind != ModelKindEmbedding {
		return ErrModelInvalidArgument
	}
	if strings.TrimSpace(m.Name) == "" || utf8.RuneCountInString(strings.TrimSpace(m.Name)) > modelNameMaxRunes {
		return ErrModelInvalidArgument
	}
	if strings.TrimSpace(m.Model) == "" || utf8.RuneCountInString(strings.TrimSpace(m.Model)) > modelNameColMaxRunes {
		return ErrModelInvalidArgument
	}
	if !update {
		key := strings.TrimSpace(m.ModelKey)
		if key == "" || utf8.RuneCountInString(key) > modelKeyMaxRunes || !modelKeyPattern.MatchString(key) {
			return ErrModelInvalidArgument
		}
	}
	if provider := strings.TrimSpace(m.Provider); utf8.RuneCountInString(provider) > modelProviderMaxRunes {
		return ErrModelInvalidArgument
	}
	if m.BaseURL != nil && utf8.RuneCountInString(*m.BaseURL) > modelBaseURLMaxRunes {
		return ErrModelInvalidArgument
	}
	if m.APIKeyEnv != nil && utf8.RuneCountInString(*m.APIKeyEnv) > modelEnvMaxRunes {
		return ErrModelInvalidArgument
	}
	if m.Remark != nil && utf8.RuneCountInString(*m.Remark) > modelRemarkMaxRunes {
		return ErrModelInvalidArgument
	}
	if m.Kind == ModelKindEmbedding {
		if m.Dimension == nil || *m.Dimension != ModelEmbeddingDimension {
			return ErrModelInvalidArgument
		}
	} else if m.Dimension != nil {
		return ErrModelInvalidArgument
	}
	if m.TimeoutMs != nil && *m.TimeoutMs <= 0 {
		return ErrModelInvalidArgument
	}
	switch {
	case update:
		if m.Status != 0 && m.Status != 1 {
			return ErrModelInvalidArgument
		}
	case m.Status != ModelStatusUnspecified && m.Status != 0 && m.Status != 1:
		return ErrModelInvalidArgument
	}
	return nil
}
