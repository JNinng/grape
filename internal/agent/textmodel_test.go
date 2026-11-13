package agent

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"

	trpcagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// textModelCatalogStub 按租户返回不同 spec，用于验证运行期模型解析接线。
type textModelCatalogStub struct {
	byTenant map[string]biz.TextModelSpec
	err      error
}

func (s *textModelCatalogStub) ResolveTextModel(ctx context.Context) (biz.TextModelSpec, error) {
	if s.err != nil {
		return biz.TextModelSpec{}, s.err
	}
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return biz.TextModelSpec{}, errors.New("missing identity")
	}
	spec, ok := s.byTenant[id.TenantID]
	if !ok {
		return biz.TextModelSpec{}, errors.New("unknown tenant")
	}
	return spec, nil
}

func TestTextModelCache_different_specs_produce_distinct_models(t *testing.T) {
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	a, err := cache.get(biz.TextModelSpec{Key: "a", Provider: "openai", Model: "model-a", BaseURL: "http://a"})
	if err != nil {
		t.Fatalf("build model a: %v", err)
	}
	b, err := cache.get(biz.TextModelSpec{Key: "b", Provider: "openai", Model: "model-b", BaseURL: "http://b"})
	if err != nil {
		t.Fatalf("build model b: %v", err)
	}
	if a == b {
		t.Fatal("different specs resolved to the same cached model")
	}
	again, err := cache.get(biz.TextModelSpec{Key: "a", Provider: "openai", Model: "model-a", BaseURL: "http://a"})
	if err != nil {
		t.Fatalf("rebuild model a: %v", err)
	}
	if again != a {
		t.Fatal("same spec was not served from cache")
	}
}

func TestRunModelOptionResolver_requires_identity_and_appends_model(t *testing.T) {
	catalog := &textModelCatalogStub{byTenant: map[string]biz.TextModelSpec{
		"t-a": {Key: "a", Provider: "openai", Model: "model-a", BaseURL: "http://a"},
		"t-b": {Key: "b", Provider: "openai", Model: "model-b", BaseURL: "http://b"},
	}}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) {
		return []trpcagent.RunOption{trpcagent.WithModelName("base-model")}, nil
	}
	resolver := runModelOptionResolver(catalog, cache, nil, base)

	// 身份缺失：拒绝本次运行，不回落默认模型。
	if _, err := resolver(context.Background(), &adapter.RunAgentInput{}); err == nil {
		t.Fatal("resolver without identity = nil error, want rejection")
	}

	// 两个租户分别解析：保留基础许可选项，并各追加一个模型选项。
	for _, tenant := range []string{"t-a", "t-b"} {
		ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: tenant, UserID: 1})
		opts, err := resolver(ctx, &adapter.RunAgentInput{})
		if err != nil {
			t.Fatalf("resolve for %s: %v", tenant, err)
		}
		if len(opts) != 2 {
			t.Fatalf("options for %s = %d, want base + model", tenant, len(opts))
		}
	}

	// 目录解析失败（停用/未知/密钥不可用）原样拒绝。
	broken := runModelOptionResolver(&textModelCatalogStub{err: biz.ErrModelSecretUnavailable}, cache, nil, base)
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-a", UserID: 1})
	if _, err := broken(ctx, &adapter.RunAgentInput{}); !errors.Is(err, biz.ErrModelSecretUnavailable) {
		t.Fatalf("resolver with catalog error = %v, want ErrModelSecretUnavailable", err)
	}
}
