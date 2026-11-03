package pg_sqlc

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
)

// 默认嵌入空间解析（biz.EmbedderResolver 的 PostgreSQL 实现）集成测试：
// 以真实 PostgreSQL 验证「租户配置 > 全局 conf 兜底」的解析顺序与严格性
// 口径（未知键拒绝、未配置回落、身份缺失 fail-closed）。复用根包套件的
// setupTestDB（deploy 全清单装载，含 sys_tenant_config）。

// resolverTestConf 构造启用态解析器配置：双空间（同维度亦可，注册表只
// 依赖键唯一），全局默认 bge-m3@v1。
func resolverTestConf() *conf.Vector {
	return &conf.Vector{
		Enabled:         true,
		DefaultEmbedder: "bge-m3@v1",
		Embedders: []*conf.Vector_Embedding{
			{Model: "bge-m3", Version: "v1", Dimension: 1024},
			{Model: "bge-m3", Version: "v2", Dimension: 1024},
		},
	}
}

// seedTenantEmbedder 写入（或清除）租户的 vector.embedder 配置行：经
// IdentityTx 落库（RLS FORCE 下与生产写入同路径）。
func seedTenantEmbedder(t *testing.T, d *Data, tenantID, value string) {
	t.Helper()
	auth, err := NewIdentity(biz.Identity{TenantID: tenantID, UserID: 1})
	if err != nil {
		t.Fatalf("map identity: %v", err)
	}
	err = d.IdentityTx(context.Background(), auth, func(s *IdentityTx) error {
		if value == "" {
			_, err := s.Tx().Exec(context.Background(),
				`DELETE FROM public.sys_tenant_config WHERE config_key = 'vector.embedder'`)
			return err
		}
		_, err := s.Tx().Exec(context.Background(),
			`INSERT INTO public.sys_tenant_config (id, status, name, config_key, value, value_type, tenant_id, created_by, updated_by)
VALUES (990001, 1, 'default embedder', 'vector.embedder', $1, 'string', current_setting('app.tenant_id'), 1, 1)
ON CONFLICT (tenant_id, config_key) WHERE deleted_at IS NULL
DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP`, value)
		return err
	})
	if err != nil {
		t.Fatalf("seed tenant config: %v", err)
	}
}

func TestEmbedderResolver_tenant_config_overrides_global_default(t *testing.T) {
	d := setupTestDB(t)
	resolver, cleanup, err := NewEmbedderResolver(resolverTestConf(), d)
	if err != nil {
		t.Fatalf("NewEmbedderResolver: %v", err)
	}
	defer cleanup()
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-reslv-a", UserID: 1})

	// 未配置：回落全局默认。
	space, dim, err := resolver.ResolveDefaultEmbedder(ctx)
	if err != nil || space != "bge-m3@v1" || dim != 1024 {
		t.Fatalf("resolve without tenant config = (%s, %d, %v), want (bge-m3@v1, 1024, nil)", space, dim, err)
	}

	// 租户配置声明 v2：覆盖全局默认，身份与维度成对返回。
	seedTenantEmbedder(t, d, "t-reslv-a", "bge-m3@v2")
	space, dim, err = resolver.ResolveDefaultEmbedder(ctx)
	if err != nil || space != "bge-m3@v2" || dim != 1024 {
		t.Fatalf("resolve with tenant config = (%s, %d, %v), want (bge-m3@v2, 1024, nil)", space, dim, err)
	}

	// 其他租户不受该租户配置影响（RLS 与显式租户参数双重隔离）。
	other := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-reslv-b", UserID: 2})
	space, _, err = resolver.ResolveDefaultEmbedder(other)
	if err != nil || space != "bge-m3@v1" {
		t.Fatalf("resolve for other tenant = (%s, %v), want global default", space, err)
	}
}

func TestEmbedderResolver_unknown_tenant_value_rejected_without_fallback(t *testing.T) {
	d := setupTestDB(t)
	resolver, cleanup, err := NewEmbedderResolver(resolverTestConf(), d)
	if err != nil {
		t.Fatalf("NewEmbedderResolver: %v", err)
	}
	defer cleanup()
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-reslv-c", UserID: 3})

	// 租户配置指向未声明空间：严格拒绝（不静默回落全局——静默换空间会
	// 让摄取/检索悄悄失配）。
	seedTenantEmbedder(t, d, "t-reslv-c", "ghost-model@v9")
	if _, _, err := resolver.ResolveDefaultEmbedder(ctx); !errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("resolve with unknown tenant value = %v, want ErrEmbedderUnknown", err)
	}

	// 清除配置后恢复全局默认（空白值与缺失行同口径：未配置）。
	seedTenantEmbedder(t, d, "t-reslv-c", "")
	space, _, err := resolver.ResolveDefaultEmbedder(ctx)
	if err != nil || space != "bge-m3@v1" {
		t.Fatalf("resolve after clearing config = (%s, %v), want global default", space, err)
	}
}

func TestEmbedderResolver_unauthenticated_and_disabled_fail(t *testing.T) {
	d := setupTestDB(t)
	resolver, cleanup, err := NewEmbedderResolver(resolverTestConf(), d)
	if err != nil {
		t.Fatalf("NewEmbedderResolver: %v", err)
	}
	defer cleanup()
	// 身份缺失 fail-closed：解析器绝不以默认身份触库。
	if _, _, err := resolver.ResolveDefaultEmbedder(context.Background()); err == nil {
		t.Fatal("resolve without identity = nil error, want rejection")
	}

	// 设施未启用：占位实现按能力缺失拒绝。
	disabled, cleanup2, err := NewEmbedderResolver(&conf.Vector{Enabled: false}, d)
	if err != nil {
		t.Fatalf("NewEmbedderResolver(disabled) = %v, want placeholder", err)
	}
	defer cleanup2()
	if _, _, err := disabled.ResolveDefaultEmbedder(context.Background()); !errors.Is(err, biz.ErrEmbedderDisabled) {
		t.Fatalf("disabled resolve = %v, want ErrEmbedderDisabled", err)
	}
}

func TestNewEmbedderResolver_invalid_spaces_fails(t *testing.T) {
	// 空间注册表非法（默认键未声明）：装配期拒绝。
	bad := resolverTestConf()
	bad.DefaultEmbedder = "ghost"
	if _, _, err := NewEmbedderResolver(bad, &Data{}); err == nil {
		t.Fatal("NewEmbedderResolver(undeclared default) = nil error, want rejection")
	}
}
