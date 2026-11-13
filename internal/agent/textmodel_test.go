package agent

import (
	"context"
	"errors"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"

	trpcagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

// maxOutputWrapperOf 取出输出上限包装层：迭代式模型包装在
// maxOutputIterModel 内。
func maxOutputWrapperOf(m model.Model) (*maxOutputModel, bool) {
	switch wrapped := m.(type) {
	case *maxOutputModel:
		return wrapped, true
	case *maxOutputIterModel:
		return wrapped.maxOutputModel, true
	default:
		return nil, false
	}
}

func TestWrapMaxOutputModel_overrides_request_without_mutating_the_caller(t *testing.T) {
	original := 512
	req := &model.Request{GenerationConfig: model.GenerationConfig{MaxTokens: &original}}
	inner := &captureThinkingModel{}

	wrapped := wrapMaxOutputModel(inner, 393216)
	if _, err := wrapped.GenerateContent(context.Background(), req); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if inner.got == nil || inner.got.GenerationConfig.MaxTokens == nil {
		t.Fatal("inner model received no max tokens")
	}
	if got := *inner.got.GenerationConfig.MaxTokens; got != 393216 {
		t.Fatalf("inner model received max tokens %d, want 393216", got)
	}
	if inner.got == req {
		t.Fatal("inner model received the caller request instead of a copy")
	}
	if *req.GenerationConfig.MaxTokens != 512 {
		t.Fatal("caller request was mutated")
	}
}

func TestWrapMaxOutputModel_preserves_iter_model(t *testing.T) {
	inner := &captureThinkingIterModel{captureThinkingModel: &captureThinkingModel{}}
	wrapped := wrapMaxOutputModel(inner, 4096)

	iter, ok := wrapped.(model.IterModel)
	if !ok {
		t.Fatal("wrapped model lost IterModel capability")
	}
	if _, err := iter.GenerateContentIter(context.Background(), &model.Request{}); err != nil {
		t.Fatalf("generate iter: %v", err)
	}
	if inner.iterGot == nil || inner.iterGot.GenerationConfig.MaxTokens == nil {
		t.Fatal("iter model received no max tokens")
	}
	if got := *inner.iterGot.GenerationConfig.MaxTokens; got != 4096 {
		t.Fatalf("iter model received max tokens %d, want 4096", got)
	}
}

func TestWrapMaxOutputModel_undeclared_limit_keeps_model(t *testing.T) {
	inner := &captureThinkingModel{}
	if got := wrapMaxOutputModel(inner, 0); got != model.Model(inner) {
		t.Fatalf("wrapMaxOutputModel(0) = %T, want the original model", got)
	}
	if got := wrapMaxOutputModel(nil, 4096); got != nil {
		t.Fatalf("wrapMaxOutputModel(nil) = %v, want nil", got)
	}
}

func TestRunModelOptionResolver_applies_generation_budget(t *testing.T) {
	catalog := &textModelCatalogStub{byTenant: map[string]biz.TextModelSpec{
		"t-a": {
			Key:             "a",
			Provider:        "openai",
			Model:           "model-a",
			BaseURL:         "http://a",
			ContextWindow:   1048576,
			MaxOutputTokens: 393216,
		},
	}}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) { return nil, nil }
	resolver := runModelOptionResolver(catalog, cache, nil, base)

	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-a", UserID: 1})
	opts, err := resolver(ctx, &adapter.RunAgentInput{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	applied := &trpcagent.RunOptions{}
	for _, opt := range opts {
		opt(applied)
	}
	if applied.ModelContextWindow != 1048576 {
		t.Fatalf("run context window = %d, want 1048576", applied.ModelContextWindow)
	}
	wrapped, ok := maxOutputWrapperOf(applied.Model)
	if !ok {
		t.Fatalf("run model = %T, want a max-output wrapper", applied.Model)
	}
	if wrapped.maxTokens != 393216 {
		t.Fatalf("wrapped max tokens = %d, want 393216", wrapped.maxTokens)
	}
}

func TestRunModelOptionResolver_skips_undeclared_generation_budget(t *testing.T) {
	catalog := &textModelCatalogStub{byTenant: map[string]biz.TextModelSpec{
		"t-a": {Key: "a", Provider: "openai", Model: "model-a", BaseURL: "http://a"},
	}}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) { return nil, nil }
	resolver := runModelOptionResolver(catalog, cache, nil, base)

	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-a", UserID: 1})
	opts, err := resolver(ctx, &adapter.RunAgentInput{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	applied := &trpcagent.RunOptions{}
	for _, opt := range opts {
		opt(applied)
	}
	if applied.ModelContextWindow != 0 {
		t.Fatalf("run context window = %d, want 0", applied.ModelContextWindow)
	}
	if _, ok := maxOutputWrapperOf(applied.Model); ok {
		t.Fatal("undeclared budget produced a max-output wrapper")
	}
}
