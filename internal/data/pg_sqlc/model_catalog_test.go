package pg_sqlc

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
)

// 模型目录集成测试（真实 PostgreSQL，未设 GRAPE_TEST_* 时跳过）：平台行
// 双租户可见、租户行覆盖平台行、停用 fail-closed、密钥加解密与唯一冲突。

func modelCatalogTestCipher(t *testing.T) biz.SecretCipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv(ModelSecretKeyEnv, base64.StdEncoding.EncodeToString(key))
	cipher, cleanup, err := NewSecretCipher()
	if err != nil {
		t.Fatalf("NewSecretCipher: %v", err)
	}
	t.Cleanup(cleanup)
	return cipher
}

func modelCatalogTestResolver(t *testing.T, d *Data, cipher biz.SecretCipher) *modelResolver {
	t.Helper()
	r, cleanup, err := NewModelCatalog(
		&conf.Agent{Llm: &conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "qwen3:8b"}},
		&conf.Vector{Enabled: false},
		d,
		cipher,
	)
	if err != nil {
		t.Fatalf("NewModelCatalog: %v", err)
	}
	t.Cleanup(cleanup)
	return r
}

func TestModelCatalog_platform_visible_and_tenant_override(t *testing.T) {
	d := setupTestDB(t)
	cipher := modelCatalogTestCipher(t)
	resolver := modelCatalogTestResolver(t, d, cipher)

	authA := biz.Identity{TenantID: "t-test-01", UserID: 1}
	authB := biz.Identity{TenantID: "t-test-02", UserID: 1}
	ctxA := biz.WithIdentity(context.Background(), authA)
	ctxB := biz.WithIdentity(context.Background(), authB)

	// 平台文本模型（种子 qwen3:8b）对两个租户都可见。
	specB, err := resolver.ResolveTextModel(ctxB)
	if err != nil {
		t.Fatalf("resolve text model for tenant B: %v", err)
	}
	if specB.Model != "qwen3:8b" || specB.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("tenant B spec = %+v, want the seeded platform row", specB)
	}

	// 租户 A 建同名键的租户行并加密密钥：A 解析到租户行，B 仍解析平台行。
	encrypted, err := cipher.Seal("tenant-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	repo := NewModelRepo(d)
	created, err := repo.CreateModel(context.Background(), authA, &biz.ModelCfg{
		Status:   1,
		Kind:     biz.ModelKindText,
		Name:     "Tenant Qwen",
		ModelKey: "qwen3:8b",
		Provider: "openai",
		BaseURL:  ptrString("http://tenant"),
		Model:    "tenant-qwen",
	}, encrypted)
	if err != nil {
		t.Fatalf("create tenant model: %v", err)
	}
	if created.Platform {
		t.Fatal("tenant model reported as platform")
	}
	if !created.HasAPIKey {
		t.Fatal("tenant model should report a configured API key")
	}

	specA, err := resolver.ResolveTextModel(ctxA)
	if err != nil {
		t.Fatalf("resolve text model for tenant A: %v", err)
	}
	if specA.Model != "tenant-qwen" || specA.APIKey != "tenant-secret" || specA.BaseURL != "http://tenant" {
		t.Fatalf("tenant A spec = %+v, want tenant override with decrypted key", specA)
	}

	specB, err = resolver.ResolveTextModel(ctxB)
	if err != nil {
		t.Fatalf("resolve text model for tenant B after override: %v", err)
	}
	if specB.Model != "qwen3:8b" || specB.APIKey != "" {
		t.Fatalf("tenant B spec = %+v, want platform row untouched", specB)
	}
}

func TestModelCatalog_disabled_or_deleted_selection_fails_closed(t *testing.T) {
	d := setupTestDB(t)
	cipher := modelCatalogTestCipher(t)
	resolver := modelCatalogTestResolver(t, d, cipher)
	authA := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctxA := biz.WithIdentity(context.Background(), authA)
	repo := NewModelRepo(d)

	created, err := repo.CreateModel(context.Background(), authA, &biz.ModelCfg{
		Status:   1,
		Kind:     biz.ModelKindText,
		Name:     "Tenant Qwen",
		ModelKey: "qwen3:8b",
		Provider: "openai",
		BaseURL:  ptrString("http://tenant"),
		Model:    "tenant-qwen",
	}, "")
	if err != nil {
		t.Fatalf("create tenant model: %v", err)
	}

	// 停用租户行：同名平台行仍然启用，但租户覆盖停用必须 fail-closed，
	// 不得静默回落到平台行。
	created.Status = 0
	if _, err := repo.UpdateModel(context.Background(), authA, created, ""); err != nil {
		t.Fatalf("disable tenant model: %v", err)
	}
	if _, err := resolver.ResolveTextModel(ctxA); !errors.Is(err, biz.ErrTextModelUnavailable) {
		t.Fatalf("resolve disabled tenant override = %v, want ErrTextModelUnavailable", err)
	}

	// 软删租户行后回落平台行。
	if err := repo.DeleteModel(context.Background(), authA, created.ID); err != nil {
		t.Fatalf("delete tenant model: %v", err)
	}
	spec, err := resolver.ResolveTextModel(ctxA)
	if err != nil || spec.Model != "qwen3:8b" {
		t.Fatalf("resolve after delete = (%+v, %v), want platform row", spec, err)
	}
}

func TestModelCatalog_embedding_dimension_and_override(t *testing.T) {
	d := setupTestDB(t)
	cipher := modelCatalogTestCipher(t)
	r, cleanup, err := NewModelCatalog(
		&conf.Agent{Llm: &conf.Agent_LLM{Model: "qwen3:8b"}},
		&conf.Vector{Enabled: true, DefaultEmbedder: "qwen3-embedding:0.6b", Embedders: []*conf.Vector_Embedding{
			{Model: "qwen3-embedding:0.6b", BaseUrl: "http://conf", Dimension: 1024},
		}},
		d,
		cipher,
	)
	if err != nil {
		t.Fatalf("NewModelCatalog: %v", err)
	}
	defer cleanup()
	authA := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctxA := biz.WithIdentity(context.Background(), authA)

	space, err := r.ResolveEmbeddingSpace(ctxA, "qwen3-embedding:0.6b")
	if err != nil {
		t.Fatalf("resolve embedding space: %v", err)
	}
	if space.Dimension != biz.ModelEmbeddingDimension || space.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Fatalf("space = %+v, want seeded platform endpoint at dimension 1024", space)
	}

	// 未知键拒绝。
	if _, err := r.ResolveEmbeddingSpace(ctxA, "ghost-embedding"); !errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("resolve unknown space = %v, want ErrEmbedderUnknown", err)
	}
}

func TestModelRepo_tenant_key_unique_conflict(t *testing.T) {
	d := setupTestDB(t)
	repo := NewModelRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	model := &biz.ModelCfg{
		Status:   1,
		Kind:     biz.ModelKindText,
		Name:     "Dup",
		ModelKey: "dup-key",
		Provider: "openai",
		Model:    "dup",
	}
	if _, err := repo.CreateModel(context.Background(), auth, model, ""); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := repo.CreateModel(context.Background(), auth, model, ""); !errors.Is(err, biz.ErrModelAlreadyExists) {
		t.Fatalf("duplicate create = %v, want ErrModelAlreadyExists", err)
	}
}

func TestModelCatalog_rerank_model_resolution(t *testing.T) {
	d := setupTestDB(t)
	cipher := modelCatalogTestCipher(t)
	resolver := modelCatalogTestResolver(t, d, cipher)
	repo := NewModelRepo(d)
	auth := biz.Identity{TenantID: "t-test-01", UserID: 1}
	ctx := biz.WithIdentity(context.Background(), auth)

	// 键完全未配置（租户与全局均无 ai.rerank.model）：跳过该层的哨兵。
	if _, err := resolver.ResolveRerankModel(ctx); !errors.Is(err, biz.ErrRerankUnavailable) {
		t.Fatalf("resolve without rerank config = %v, want ErrRerankUnavailable", err)
	}

	// 配置全局键但无 sys_model 行：配置错误返回普通错误，与未配置哨兵
	// 区分（调用方按调用失败降级，而非静默跳过）。
	configs := NewConfigRepo(d)
	value := "bge-reranker:0.6b"
	if _, err := configs.CreateSuper(ctx, auth, &biz.Config{
		Status:    1,
		Name:      "rerank model",
		ConfigKey: biz.ConfigKeyRerankModel,
		Value:     &value,
		ValueType: "string",
	}); err != nil {
		t.Fatalf("seed global rerank key: %v", err)
	}
	_, err := resolver.ResolveRerankModel(ctx)
	if err == nil || errors.Is(err, biz.ErrRerankUnavailable) {
		t.Fatalf("resolve configured-but-missing row = %v, want plain error", err)
	}

	// 平台 rerank 行就位后解析到该行：端点事实与超时按行声明。
	created, err := repo.CreateModelPlatform(ctx, auth, &biz.ModelCfg{
		Status:    1,
		Kind:      biz.ModelKindRerank,
		Name:      "BGE Reranker",
		ModelKey:  "bge-reranker:0.6b",
		Provider:  "openai",
		BaseURL:   ptrString("http://127.0.0.1:11434/v1"),
		Model:     "bge-reranker:0.6b",
		TimeoutMs: func(v int32) *int32 { return &v }(8000),
	}, "")
	if err != nil {
		t.Fatalf("create platform rerank row: %v", err)
	}
	spec, err := resolver.ResolveRerankModel(ctx)
	if err != nil {
		t.Fatalf("resolve rerank model: %v", err)
	}
	if spec.Key != "bge-reranker:0.6b" || spec.BaseURL != "http://127.0.0.1:11434/v1" || spec.Model != "bge-reranker:0.6b" {
		t.Fatalf("spec = %+v, want the platform rerank row", spec)
	}
	if spec.Timeout != 8*time.Second {
		t.Fatalf("timeout = %v, want 8s from the row declaration", spec.Timeout)
	}

	// 停用行 fail-closed：键已配置而模型停用属「配置了但坏了」，返回
	// 普通错误而非跳过哨兵。
	created.Status = 0
	if _, err := repo.UpdateModelPlatform(ctx, auth, created, ""); err != nil {
		t.Fatalf("disable platform rerank row: %v", err)
	}
	_, err = resolver.ResolveRerankModel(ctx)
	if err == nil || errors.Is(err, biz.ErrRerankUnavailable) {
		t.Fatalf("resolve disabled rerank row = %v, want plain error", err)
	}
}
