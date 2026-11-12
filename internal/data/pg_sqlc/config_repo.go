package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// 模型目录解析（biz.TextModelCatalog、biz.EmbedderResolver 与
// biz.RerankModelCatalog 的 PostgreSQL 实现）：读取 sys_config /
// sys_tenant_config 的模型键选键，再按「sys_model 租户行 > 平台行 > conf
// 对应条目」找端点；租户边界由 RLS 承担（sys_model 读策略放行本租户行与
// 平台行）。停用、软删与未知键一律 fail-closed，不静默回落。无缓存：配置
// 点读（唯一索引）成本低，切换即下一次调用生效。

// defaultEmbeddingTimeout 是模型行与 conf 条目都未声明超时时的单次调用
// 上限（与 openaiembedder 的缺省一致）。
const defaultEmbeddingTimeout = 30 * time.Second

// modelResolver 同时实现文本模型目录与嵌入空间解析：两者共享选键、行
// 解析与密钥解密逻辑。
type modelResolver struct {
	data          *Data
	llm           *conf.Agent_LLM
	spaces        map[string]int32 // conf.vector.embedders 键 → 维度
	confEntries   map[string]*conf.Vector_Embedding
	defaultSpace  string
	vectorEnabled bool
	cipher        biz.SecretCipher
}

var (
	_ biz.TextModelCatalog   = (*modelResolver)(nil)
	_ biz.EmbedderResolver   = (*modelResolver)(nil)
	_ biz.RerankModelCatalog = (*modelResolver)(nil)
)

// NewModelCatalog 构造模型目录解析器，同时提供 biz.TextModelCatalog、
// biz.EmbedderResolver 与 biz.RerankModelCatalog（Wire 以接口绑定同一
// 实例）。conf.vector 未启用时嵌入侧按设施缺失拒绝，文本与重排侧照常
// 工作。
func NewModelCatalog(c *conf.Agent, v *conf.Vector, d *Data, cipher biz.SecretCipher) (*modelResolver, func(), error) {
	if cipher == nil {
		return nil, nil, errors.New("pg_sqlc: model catalog requires a secret cipher")
	}
	r := &modelResolver{data: d, llm: c.GetLlm(), cipher: cipher, spaces: map[string]int32{}, confEntries: map[string]*conf.Vector_Embedding{}}
	if v.GetEnabled() {
		spaces, def, err := conf.VectorSpaces(v)
		if err != nil {
			return nil, nil, err
		}
		r.spaces = spaces
		r.defaultSpace = def
		r.vectorEnabled = true
		for _, entry := range v.GetEmbedders() {
			r.confEntries[conf.EmbedderSpaceKey(entry.GetModel(), entry.GetVersion())] = entry
		}
	}
	return r, func() {}, nil
}

// NewEmbedderResolver 是仅嵌入侧的兼容构造器（单测与旧装配面）：等价于
// 用环境变量密钥构造模型目录后只返回嵌入解析接口。生产装配走
// NewModelCatalog。
func NewEmbedderResolver(c *conf.Vector, d *Data) (biz.EmbedderResolver, func(), error) {
	cipher, cleanup, err := NewSecretCipher()
	if err != nil {
		return nil, nil, err
	}
	r, rcleanup, err := NewModelCatalog(nil, c, d, cipher)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return r, func() { rcleanup(); cleanup() }, nil
}

// ResolveTextModel 解析当前身份的文本模型。
func (r *modelResolver) ResolveTextModel(ctx context.Context) (biz.TextModelSpec, error) {
	auth, err := resolverIdentity(ctx)
	if err != nil {
		return biz.TextModelSpec{}, err
	}
	key, err := r.selectedKey(ctx, auth, biz.ConfigKeyTextModel, "")
	if err != nil {
		return biz.TextModelSpec{}, err
	}
	if key == "" && r.llm != nil {
		key = strings.TrimSpace(r.llm.GetModel())
	}
	if key == "" {
		return biz.TextModelSpec{}, fmt.Errorf("%w: no text model key is configured", biz.ErrTextModelUnavailable)
	}
	row, err := r.lookupModel(ctx, auth, key)
	if err != nil {
		return biz.TextModelSpec{}, err
	}
	if row != nil {
		if row.Status != 1 || row.Kind != biz.ModelKindText {
			return biz.TextModelSpec{}, fmt.Errorf("%w: text model %q is disabled or has a non-text kind", biz.ErrTextModelUnavailable, key)
		}
		apiKey, err := r.apiKeyOf(row)
		if err != nil {
			return biz.TextModelSpec{}, err
		}
		return biz.TextModelSpec{
			Key:      key,
			Provider: row.Provider,
			BaseURL:  stringOrEmpty(row.BaseUrl),
			Model:    row.Model,
			APIKey:   apiKey,
			Timeout:  r.textTimeout(row),
		}, nil
	}
	if r.llm != nil && key == strings.TrimSpace(r.llm.GetModel()) {
		return biz.TextModelSpec{
			Key:      key,
			Provider: r.llm.GetProvider(),
			BaseURL:  r.llm.GetBaseUrl(),
			Model:    r.llm.GetModel(),
			APIKey:   r.llm.GetApiKey(),
			Timeout:  r.llm.GetTimeout().AsDuration(),
		}, nil
	}
	return biz.TextModelSpec{}, fmt.Errorf("%w: %q is neither a visible sys_model row nor the conf.agent.llm model", biz.ErrTextModelUnavailable, key)
}

// ResolveRerankModel 解析当前身份的重排模型：选键（租户配置 ai.rerank.model
// > 全局同名键）后按「sys_model 租户行 > 平台行」找端点。rerank 无 conf
// 兜底条目，形态比文本侧简单。错误语义：键完全未配置报
// biz.ErrRerankUnavailable（调用方按跳过重排层处理）；键有值而行停用、
// 软删、类别不符或无可见行是配置错误，返回普通错误（消费方按调用失败
// 降级并记日志，与未配置相区分，不静默回落）。
func (r *modelResolver) ResolveRerankModel(ctx context.Context) (biz.RerankModelSpec, error) {
	auth, err := resolverIdentity(ctx)
	if err != nil {
		return biz.RerankModelSpec{}, err
	}
	key, err := r.selectedKey(ctx, auth, biz.ConfigKeyRerankModel, "")
	if err != nil {
		return biz.RerankModelSpec{}, err
	}
	if key == "" {
		return biz.RerankModelSpec{}, fmt.Errorf("%w: no rerank model key is configured", biz.ErrRerankUnavailable)
	}
	row, err := r.lookupModel(ctx, auth, key)
	if err != nil {
		return biz.RerankModelSpec{}, err
	}
	if row == nil {
		return biz.RerankModelSpec{}, fmt.Errorf("pg_sqlc: rerank model key %q has no visible sys_model row", key)
	}
	if row.Status != 1 || row.Kind != biz.ModelKindRerank {
		return biz.RerankModelSpec{}, fmt.Errorf("pg_sqlc: rerank model %q is disabled or has a non-rerank kind", key)
	}
	apiKey, err := r.apiKeyOf(row)
	if err != nil {
		return biz.RerankModelSpec{}, err
	}
	return biz.RerankModelSpec{
		Key:      key,
		Provider: row.Provider,
		BaseURL:  stringOrEmpty(row.BaseUrl),
		Model:    row.Model,
		APIKey:   apiKey,
		Timeout:  rerankTimeout(row),
	}, nil
}

// ResolveDefaultEmbedder 解析当前身份的默认嵌入空间：租户配置
// ai.embedding.model（兼容旧键 vector.embedder）> 全局配置 > conf 默认。
func (r *modelResolver) ResolveDefaultEmbedder(ctx context.Context) (string, int, error) {
	if !r.vectorEnabled {
		return "", 0, biz.ErrEmbedderDisabled
	}
	auth, err := resolverIdentity(ctx)
	if err != nil {
		return "", 0, err
	}
	key, err := r.selectedKey(ctx, auth, biz.ConfigKeyEmbeddingModel, biz.ConfigKeyEmbedderLegacy)
	if err != nil {
		return "", 0, err
	}
	if key == "" {
		key = r.defaultSpace
	}
	space, err := r.resolveSpace(ctx, auth, key)
	if err != nil {
		return "", 0, err
	}
	return space.Key, space.Dimension, nil
}

// ResolveEmbeddingSpace 按空间键解析端点与维度：sys_model 租户行/平台行
// 优先，conf.vector.embedders 兼容条目兜底；停用、软删与未知键拒绝。
func (r *modelResolver) ResolveEmbeddingSpace(ctx context.Context, key string) (biz.EmbeddingSpace, error) {
	if !r.vectorEnabled {
		return biz.EmbeddingSpace{}, biz.ErrEmbedderDisabled
	}
	auth, err := resolverIdentity(ctx)
	if err != nil {
		return biz.EmbeddingSpace{}, err
	}
	return r.resolveSpace(ctx, auth, key)
}

// resolveSpace 完成单键空间解析。
func (r *modelResolver) resolveSpace(ctx context.Context, auth Identity, key string) (biz.EmbeddingSpace, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return biz.EmbeddingSpace{}, fmt.Errorf("%w: embedding space key is empty", biz.ErrEmbedderUnknown)
	}
	row, err := r.lookupModel(ctx, auth, key)
	if err != nil {
		return biz.EmbeddingSpace{}, err
	}
	if row != nil {
		if row.Status != 1 || row.Kind != biz.ModelKindEmbedding {
			return biz.EmbeddingSpace{}, fmt.Errorf("%w: embedding model %q is disabled or has a non-embedding kind", biz.ErrEmbedderUnknown, key)
		}
		if row.Dimension == nil || *row.Dimension != biz.ModelEmbeddingDimension {
			return biz.EmbeddingSpace{}, fmt.Errorf("%w: embedding model %q must declare dimension %d", biz.ErrEmbeddingModelUnavailable, key, biz.ModelEmbeddingDimension)
		}
		apiKey, err := r.apiKeyOf(row)
		if err != nil {
			return biz.EmbeddingSpace{}, err
		}
		return biz.EmbeddingSpace{
			Key:       key,
			Provider:  row.Provider,
			BaseURL:   stringOrEmpty(row.BaseUrl),
			Model:     row.Model,
			APIKey:    apiKey,
			Dimension: int(*row.Dimension),
			Timeout:   r.embeddingTimeout(row),
		}, nil
	}
	entry, ok := r.confEntries[key]
	if !ok {
		return biz.EmbeddingSpace{}, fmt.Errorf("%w: %q is neither a visible sys_model row nor a conf.vector.embedders entry", biz.ErrEmbedderUnknown, key)
	}
	dim := int(r.spaces[key])
	if dim != biz.ModelEmbeddingDimension {
		return biz.EmbeddingSpace{}, fmt.Errorf("%w: conf space %q declares dimension %d, expected %d", biz.ErrEmbeddingModelUnavailable, key, dim, biz.ModelEmbeddingDimension)
	}
	timeout := entry.GetTimeout().AsDuration()
	if timeout <= 0 {
		timeout = defaultEmbeddingTimeout
	}
	return biz.EmbeddingSpace{
		Key:       key,
		Provider:  entry.GetProvider(),
		BaseURL:   entry.GetBaseUrl(),
		Model:     entry.GetModel(),
		APIKey:    entry.GetApiKey(),
		Dimension: dim,
		Timeout:   timeout,
	}, nil
}

// selectedKey 选键：租户配置（主键优先、兼容旧键次之）> 全局配置。空值
// 视为未配置；返回空串表示由调用方回落 conf 默认。
func (r *modelResolver) selectedKey(ctx context.Context, auth Identity, key, legacyKey string) (string, error) {
	var tenantValue, globalValue string
	err := r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		v, err := configValueOf(ctx, s, key)
		if err != nil {
			return err
		}
		if v == "" && legacyKey != "" {
			v, err = configValueOf(ctx, s, legacyKey)
			if err != nil {
				return err
			}
		}
		tenantValue = v
		g, err := s.Q().GetSysConfigValueByKey(ctx, key)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if g != nil {
			globalValue = strings.TrimSpace(*g)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("pg_sqlc: resolve model key %s: %w", key, err)
	}
	if tenantValue != "" {
		return tenantValue, nil
	}
	return globalValue, nil
}

// configValueOf 读取启用状态的租户配置值；无行或空值返回空串。
func configValueOf(ctx context.Context, s *IdentityTx, key string) (string, error) {
	v, err := s.Q().GetSysTenantConfigValueByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", nil
	}
	return strings.TrimSpace(*v), nil
}

// lookupModel 按 tenant > platform 找 sys_model 行（不含状态过滤，停用由
// 调用方判定并 fail-closed，避免停用行静默回落到下一级）。未找到返回
// (nil, nil)。
func (r *modelResolver) lookupModel(ctx context.Context, auth Identity, key string) (*gen.SysModel, error) {
	var out *gen.SysModel
	err := r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		row, err := s.Q().GetSysModelForTenantByKey(ctx, key)
		if err == nil {
			out = &row
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		platform, err := s.Q().GetSysModelPlatformByKey(ctx, key)
		if err == nil {
			out = &platform
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: resolve model %s: %w", key, err)
	}
	return out, nil
}

// apiKeyOf 解析模型密钥：密文优先（解密失败 fail-closed），其次环境变量。
func (r *modelResolver) apiKeyOf(row *gen.SysModel) (string, error) {
	if row.ApiKeyCipher != nil && strings.TrimSpace(*row.ApiKeyCipher) != "" {
		plain, err := r.cipher.Open(*row.ApiKeyCipher)
		if err != nil {
			return "", err
		}
		return plain, nil
	}
	if row.ApiKeyEnv != nil {
		return strings.TrimSpace(os.Getenv(*row.ApiKeyEnv)), nil
	}
	return "", nil
}

// textTimeout 取模型行超时，缺失回落 conf.agent.llm.timeout。
func (r *modelResolver) textTimeout(row *gen.SysModel) time.Duration {
	if row.TimeoutMs != nil && *row.TimeoutMs > 0 {
		return time.Duration(*row.TimeoutMs) * time.Millisecond
	}
	if r.llm != nil {
		return r.llm.GetTimeout().AsDuration()
	}
	return 0
}

// embeddingTimeout 取模型行超时，缺失回落 30s（conf 条目由 resolveSpace
// 单独取值）。
func (r *modelResolver) embeddingTimeout(row *gen.SysModel) time.Duration {
	if row.TimeoutMs != nil && *row.TimeoutMs > 0 {
		return time.Duration(*row.TimeoutMs) * time.Millisecond
	}
	return defaultEmbeddingTimeout
}

// rerankTimeout 取模型行超时，缺失返回 0（由调用方实现按其声明的缺省
// 上限兜底；rerank 无 conf 条目可回落）。
func rerankTimeout(row *gen.SysModel) time.Duration {
	if row.TimeoutMs != nil && *row.TimeoutMs > 0 {
		return time.Duration(*row.TimeoutMs) * time.Millisecond
	}
	return 0
}

// resolverIdentity 从认证 ctx 取身份：缺失或非法（空租户/非正用户）拒绝，
// 与向量存储同纪律。
func resolverIdentity(ctx context.Context) (Identity, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return Identity{}, errors.New("pg_sqlc: model resolver requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return Identity{}, err
	}
	return auth, nil
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
