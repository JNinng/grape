package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc/gen"
)

// sys_tenant_config 的读路径与默认嵌入空间解析（biz.EmbedderResolver 的
// PostgreSQL 实现）：读取 vector.embedder 键，经 IdentityTx 执行，租户边界
// 由 RLS 承担。解析顺序「请求显式 > 租户配置 > 全局 conf 兜底」中本仓储
// 承担后两级；租户值指向未声明空间按 ErrEmbedderUnknown 严格拒绝，不静默
// 回落；无缓存（唯一索引点读，每次摄取/检索现场解析）。
const (
	// embedderConfigKey 是租户级默认嵌入空间的配置键（sys_tenant_config.config_key）。
	embedderConfigKey = "vector.embedder"
	// tenantConfigEnabledStatus 是启用的配置行状态（sys_tenant_config.status）。
	tenantConfigEnabledStatus = int64(1)
)

// embedderResolver 是默认嵌入空间解析器。
type embedderResolver struct {
	data   *Data
	spaces map[string]int32 // 空间键 → 声明维度（conf.VectorSpaces 单一解析源）
	def    string           // 全局默认键（装配期已校验引用已声明键）
}

var _ biz.EmbedderResolver = (*embedderResolver)(nil)

// NewEmbedderResolver 构造默认嵌入空间解析器。conf.vector.enabled=false
// 时返回禁用占位实现（调用按设施缺失拒绝）；启用时经 conf.VectorSpaces
// 校验空间清单与全局默认键，fail-fast。
func NewEmbedderResolver(c *conf.Vector, d *Data) (biz.EmbedderResolver, func(), error) {
	if !c.GetEnabled() {
		return disabledEmbedderResolver{}, func() {}, nil
	}
	spaces, def, err := conf.VectorSpaces(c)
	if err != nil {
		return nil, nil, err
	}
	return &embedderResolver{data: d, spaces: spaces, def: def}, func() {}, nil
}

// ResolveDefaultEmbedder 解析当前身份的默认嵌入空间：租户配置
// （sys_tenant_config 的 vector.embedder 行，启用状态）优先，未配置回落
// 全局默认。返回值身份与维度成对；身份缺失 fail-closed（与 VectorStore
// 同纪律：身份只来自认证中间件）。
func (r *embedderResolver) ResolveDefaultEmbedder(ctx context.Context) (string, int, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return "", 0, errors.New("pg_sqlc: embedder resolver requires an authenticated identity")
	}
	auth := pgAuth(id)
	if err := auth.validate(); err != nil {
		return "", 0, err
	}
	configKey := embedderConfigKey
	status := tenantConfigEnabledStatus
	var chosen string
	err := r.data.IdentityTx(ctx, auth, func(s *IdentityTx) error {
		res, err := gen.ListSysTenantConfig(ctx, s.Tx(), gen.ListSysTenantConfigParams{
			ConfigKey: &configKey,
			Status:    &status,
			Limit:     1,
		})
		if err != nil {
			return err
		}
		// (tenant_id, config_key) 唯一索引保证至多一行；空白值视为未配置。
		if len(res.Items) > 0 && res.Items[0].Value != nil {
			chosen = strings.TrimSpace(*res.Items[0].Value)
		}
		return nil
	})
	if err != nil {
		return "", 0, fmt.Errorf("pg_sqlc: resolve default embedder: %w", err)
	}
	if chosen == "" {
		chosen = r.def
	}
	dim, ok := r.spaces[chosen]
	if !ok {
		return "", 0, fmt.Errorf("%w: tenant config %s = %q is not declared in conf.vector.embedders", biz.ErrEmbedderUnknown, embedderConfigKey, chosen)
	}
	return chosen, int(dim), nil
}

// disabledEmbedderResolver 是设施未启用时的占位实现：按设施缺失拒绝。
type disabledEmbedderResolver struct{}

var _ biz.EmbedderResolver = disabledEmbedderResolver{}

func (disabledEmbedderResolver) ResolveDefaultEmbedder(context.Context) (string, int, error) {
	return "", 0, biz.ErrEmbedderDisabled
}
