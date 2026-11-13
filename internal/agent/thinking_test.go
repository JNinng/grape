package agent

import (
	"context"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"

	trpcagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// captureThinkingModel 记录收到的请求，用于断言思考强度落在请求副本上。
type captureThinkingModel struct {
	got *model.Request
}

func (m *captureThinkingModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.got = req
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

func (m *captureThinkingModel) Info() model.Info { return model.Info{Name: "capture"} }

// captureThinkingIterModel 在基础记录模型之上实现 IterModel，用于断言
// 包装后仍保留迭代式流能力。
type captureThinkingIterModel struct {
	*captureThinkingModel
	iterGot *model.Request
}

func (m *captureThinkingIterModel) GenerateContentIter(
	_ context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.iterGot = req
	return func(func(*model.Response) bool) {}, nil
}

// thinkingWrapperOf 取出包装层：迭代式模型包装在 thinkingIterModel 内。
func thinkingWrapperOf(m model.Model) (*thinkingModel, bool) {
	switch wrapped := m.(type) {
	case *thinkingModel:
		return wrapped, true
	case *thinkingIterModel:
		return wrapped.thinkingModel, true
	default:
		return nil, false
	}
}

func TestThinkingLevelFromForwardedProps(t *testing.T) {
	cases := []struct {
		name   string
		input  *adapter.RunAgentInput
		want   thinkingLevel
		wantOK bool
	}{
		{name: "nil input", input: nil, want: thinkingLevel{}, wantOK: false},
		{name: "nil props", input: &adapter.RunAgentInput{}, want: thinkingLevel{}, wantOK: false},
		{
			name:   "non object props",
			input:  &adapter.RunAgentInput{ForwardedProps: "off"},
			want:   thinkingLevel{},
			wantOK: false,
		},
		{
			name:   "missing key",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{"other": "high"}},
			want:   thinkingLevel{},
			wantOK: false,
		},
		{
			name:   "non string value",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: true}},
			want:   thinkingLevel{},
			wantOK: false,
		},
		{
			name:   "unknown level",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: "extreme"}},
			want:   thinkingLevel{},
			wantOK: false,
		},
		{
			name:   "off",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: "off"}},
			want:   thinkingLevel{enabled: false},
			wantOK: true,
		},
		{
			name:   "low",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: "low"}},
			want:   thinkingLevel{enabled: true, effort: "low"},
			wantOK: true,
		},
		{
			name:   "medium",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: "medium"}},
			want:   thinkingLevel{enabled: true, effort: "medium"},
			wantOK: true,
		},
		{
			name:   "high",
			input:  &adapter.RunAgentInput{ForwardedProps: map[string]any{thinkingForwardedProp: "high"}},
			want:   thinkingLevel{enabled: true, effort: "high"},
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := thinkingLevelFromForwardedProps(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("thinkingLevelFromForwardedProps = (%+v, %v), want (%+v, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestWrapThinkingModel_overrides_request_without_mutating_the_caller(t *testing.T) {
	originalEnabled := false
	originalEffort := "low"
	req := &model.Request{GenerationConfig: model.GenerationConfig{
		ThinkingEnabled: &originalEnabled,
		ReasoningEffort: &originalEffort,
	}}
	inner := &captureThinkingModel{}

	wrapped := wrapThinkingModel(inner, thinkingLevel{enabled: true, effort: "high"})
	if _, err := wrapped.GenerateContent(context.Background(), req); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if inner.got == nil || inner.got.GenerationConfig.ThinkingEnabled == nil {
		t.Fatal("inner model received no thinking toggle")
	}
	if !*inner.got.GenerationConfig.ThinkingEnabled {
		t.Fatal("inner model received thinking=false, want true")
	}
	if inner.got.GenerationConfig.ReasoningEffort == nil || *inner.got.GenerationConfig.ReasoningEffort != "high" {
		t.Fatalf("inner model received effort %v, want high", inner.got.GenerationConfig.ReasoningEffort)
	}
	if inner.got == req {
		t.Fatal("inner model received the caller request instead of a copy")
	}
	if *req.GenerationConfig.ThinkingEnabled || *req.GenerationConfig.ReasoningEffort != "low" {
		t.Fatal("caller request was mutated")
	}
}

func TestWrapThinkingModel_clears_effort_when_disabled(t *testing.T) {
	effort := "high"
	req := &model.Request{GenerationConfig: model.GenerationConfig{ReasoningEffort: &effort}}
	inner := &captureThinkingModel{}

	wrapped := wrapThinkingModel(inner, thinkingLevel{enabled: false})
	if _, err := wrapped.GenerateContent(context.Background(), req); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if inner.got == nil || inner.got.GenerationConfig.ThinkingEnabled == nil {
		t.Fatal("inner model received no thinking toggle")
	}
	if *inner.got.GenerationConfig.ThinkingEnabled {
		t.Fatal("inner model received thinking=true, want false")
	}
	if inner.got.GenerationConfig.ReasoningEffort != nil {
		t.Fatalf("inner model received effort %v, want nil", *inner.got.GenerationConfig.ReasoningEffort)
	}
}

func TestWrapThinkingModel_preserves_iter_model(t *testing.T) {
	inner := &captureThinkingIterModel{captureThinkingModel: &captureThinkingModel{}}
	wrapped := wrapThinkingModel(inner, thinkingLevel{enabled: true, effort: "medium"})

	iter, ok := wrapped.(model.IterModel)
	if !ok {
		t.Fatal("wrapped model lost IterModel capability")
	}
	if _, err := iter.GenerateContentIter(context.Background(), &model.Request{}); err != nil {
		t.Fatalf("generate iter: %v", err)
	}
	if inner.iterGot == nil || inner.iterGot.GenerationConfig.ReasoningEffort == nil {
		t.Fatal("iter model received no thinking effort")
	}
	if *inner.iterGot.GenerationConfig.ReasoningEffort != "medium" {
		t.Fatalf("iter model received effort %q, want medium", *inner.iterGot.GenerationConfig.ReasoningEffort)
	}
}

func TestWrapThinkingModel_plain_model_does_not_claim_iter(t *testing.T) {
	wrapped := wrapThinkingModel(&captureThinkingModel{}, thinkingLevel{enabled: false})
	if _, ok := wrapped.(model.IterModel); ok {
		t.Fatal("plain model was wrapped into an IterModel")
	}
}

func TestRunModelOptionResolver_applies_thinking_to_the_static_model(t *testing.T) {
	static := &captureThinkingModel{}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) { return nil, nil }
	resolver := runModelOptionResolver(nil, cache, static, base)

	opts, err := resolver(context.Background(), &adapter.RunAgentInput{
		ForwardedProps: map[string]any{thinkingForwardedProp: "low"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	applied := &trpcagent.RunOptions{}
	for _, opt := range opts {
		opt(applied)
	}
	wrapped, ok := thinkingWrapperOf(applied.Model)
	if !ok {
		t.Fatalf("run model = %T, want a thinking wrapper", applied.Model)
	}
	if wrapped.level != (thinkingLevel{enabled: true, effort: "low"}) {
		t.Fatalf("thinking level = %+v, want low", wrapped.level)
	}
	if wrapped.Model != static {
		t.Fatal("wrapped model is not the static model")
	}
}

func TestRunModelOptionResolver_keeps_the_static_model_without_a_thinking_prop(t *testing.T) {
	static := &captureThinkingModel{}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) { return nil, nil }
	resolver := runModelOptionResolver(nil, cache, static, base)

	opts, err := resolver(context.Background(), &adapter.RunAgentInput{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	applied := &trpcagent.RunOptions{}
	for _, opt := range opts {
		opt(applied)
	}
	if applied.Model != static {
		t.Fatalf("run model = %T, want the static model unwrapped", applied.Model)
	}
}

func TestRunModelOptionResolver_applies_thinking_to_the_catalog_model(t *testing.T) {
	catalog := &textModelCatalogStub{byTenant: map[string]biz.TextModelSpec{
		"t-a": {Key: "a", Provider: "openai", Model: "model-a", BaseURL: "http://a"},
	}}
	cache := newTextModelCache(&conf.Agent_LLM{Provider: "openai", BaseUrl: "http://conf", Model: "conf-model"})
	base := func(context.Context, *adapter.RunAgentInput) ([]trpcagent.RunOption, error) { return nil, nil }
	resolver := runModelOptionResolver(catalog, cache, nil, base)

	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: "t-a", UserID: 1})
	opts, err := resolver(ctx, &adapter.RunAgentInput{
		ForwardedProps: map[string]any{thinkingForwardedProp: "high"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	applied := &trpcagent.RunOptions{}
	for _, opt := range opts {
		opt(applied)
	}
	wrapped, ok := thinkingWrapperOf(applied.Model)
	if !ok {
		t.Fatalf("run model = %T, want a thinking wrapper", applied.Model)
	}
	if wrapped.level != (thinkingLevel{enabled: true, effort: "high"}) {
		t.Fatalf("thinking level = %+v, want high", wrapped.level)
	}
}
