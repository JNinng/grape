package agent

import (
	"testing"
	"time"

	"grape/internal/conf"

	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNewLLMModelRejectsMissingModelName(t *testing.T) {
	if _, err := newLLMModel(&conf.Agent_LLM{}); err == nil {
		t.Fatalf("missing model: want error, got none")
	}
}

func TestProviderVariantMapsKnownNames(t *testing.T) {
	cases := map[string]bool{
		"":         false, // 空值按 openai，无需显式变体
		"openai":   false,
		"deepseek": true,
		"qwen":     true,
		"glm":      true,
		"minimax":  true,
		"kimi":     true,
		"hunyuan":  true,
		"unknown":  false,
	}
	for name, explicit := range cases {
		if _, ok := providerVariant(name); ok != explicit {
			t.Fatalf("provider %q: explicit variant = %v, want %v", name, ok, explicit)
		}
	}
}

func TestStackHelpersApplyConfiguredDurations(t *testing.T) {
	c := &conf.Agent{
		Agui: &conf.Agent_AGUI{
			Timeout:           durationpb.New(5 * time.Minute),
			HeartbeatInterval: durationpb.New(3 * time.Second),
			BasePath:          "agent-ui/",
		},
	}
	if got := basePathOf(c); got != "/agent-ui" {
		t.Fatalf("basePathOf = %q, want /agent-ui", got)
	}
	if got := runTimeoutOf(c); got != 5*time.Minute {
		t.Fatalf("runTimeoutOf = %v, want 5m", got)
	}
	if got := heartbeatOf(c); got != 3*time.Second {
		t.Fatalf("heartbeatOf = %v, want 3s", got)
	}
}

func TestStackHelpersFallBackToDefaultsWhenUnset(t *testing.T) {
	c := &conf.Agent{}
	if got := basePathOf(c); got != defaultBasePath {
		t.Fatalf("basePathOf = %q, want %q", got, defaultBasePath)
	}
	if got := runTimeoutOf(c); got != defaultRunTimeout {
		t.Fatalf("runTimeoutOf = %v, want %v", got, defaultRunTimeout)
	}
	if got := heartbeatOf(c); got != defaultHeartbeat {
		t.Fatalf("heartbeatOf = %v, want %v", got, defaultHeartbeat)
	}
}
