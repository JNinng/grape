package conf

import (
	"strings"
	"testing"
)

// 向量设施嵌入空间的解析与校验单测（纯内存）：键派生、注册表合法性、
// 全局默认引用；校验语义只在此断言一次。

func spacesConf(embedders []*Vector_Embedding, def string) *Vector {
	return &Vector{Enabled: true, DefaultEmbedder: def, Embedders: embedders}
}

func TestEmbedderSpaceKey_derivation(t *testing.T) {
	cases := []struct {
		model, version, want string
	}{
		{"bge-m3", "", "bge-m3"},
		{"bge-m3", "v2", "bge-m3@v2"},
		{"text-embedding-3-small", "1", "text-embedding-3-small@1"},
		{"qwen3-embedding:0.6b", "", "qwen3-embedding:0.6b"},
		{"qwen3-embedding:0.6b", "v1", "qwen3-embedding:0.6b@v1"},
	}
	for _, tc := range cases {
		if got := EmbedderSpaceKey(tc.model, tc.version); got != tc.want {
			t.Fatalf("EmbedderSpaceKey(%q, %q) = %q, want %q", tc.model, tc.version, got, tc.want)
		}
	}
}

func TestVectorSpaces_valid_config_resolves_registry(t *testing.T) {
	// 键恰好 128 字符是白名单与列宽的公共上界（接受侧边界）。
	longKey := strings.Repeat("a", 128)
	spaces, def, err := VectorSpaces(spacesConf([]*Vector_Embedding{
		{Model: "bge-m3", Version: "v1", Dimension: 1024},
		{Model: "bge-m3", Version: "v2", Dimension: 1024},
		{Model: "other-model", Dimension: 4},
		{Model: longKey, Dimension: 8},
	}, longKey))
	if err != nil {
		t.Fatalf("VectorSpaces(valid) = %v, want nil", err)
	}
	if def != longKey {
		t.Fatalf("default = %q, want the 128-char key", def)
	}
	if len(spaces) != 4 || spaces["bge-m3@v1"] != 1024 || spaces["bge-m3@v2"] != 1024 || spaces["other-model"] != 4 || spaces[longKey] != 8 {
		t.Fatalf("spaces = %v, want four keys with declared dimensions", spaces)
	}
	// 维度恰好 VectorMaxDimension 是存储上界的接受侧。
	atCap, _, err := VectorSpaces(spacesConf([]*Vector_Embedding{
		{Model: "m", Dimension: VectorMaxDimension},
	}, "m"))
	if err != nil || atCap["m"] != VectorMaxDimension {
		t.Fatalf("VectorSpaces(dimension at cap) = (%v, %v), want accepted at %d", atCap, err, VectorMaxDimension)
	}
}

func TestVectorSpaces_accepts_tagged_model_key(t *testing.T) {
	// Ollama 等本地端点的模型名以冒号带标签，标签属于空间身份的一部分；
	// 键只按白名单校验字符，不拆分冒号。
	const key = "qwen3-embedding:0.6b"
	spaces, def, err := VectorSpaces(spacesConf([]*Vector_Embedding{
		{Model: key, Dimension: 1024},
	}, key))
	if err != nil {
		t.Fatalf("VectorSpaces(tagged model) = %v, want nil", err)
	}
	if def != key || spaces[key] != 1024 {
		t.Fatalf("spaces = %v, default = %q, want tagged key with dimension 1024", spaces, def)
	}
}

func TestVectorSpaces_invalid_configs_rejected(t *testing.T) {
	valid := &Vector_Embedding{Model: "m", Dimension: 4}
	cases := []struct {
		name string
		conf *Vector
		want string
	}{
		{"no embedders", spacesConf(nil, "m"), "at least one embedding space"},
		{"missing model", spacesConf([]*Vector_Embedding{{Dimension: 4}}, "m"), "model is required"},
		{"zero dimension", spacesConf([]*Vector_Embedding{{Model: "m"}}, "m"), "dimension must be in"},
		{"oversize dimension", spacesConf([]*Vector_Embedding{{Model: "m", Dimension: VectorMaxDimension + 1}}, "m"), "dimension must be in"},
		{"bad key", spacesConf([]*Vector_Embedding{{Model: "bad model", Dimension: 4}}, "bad model"), "must match"},
		{"key too long", spacesConf([]*Vector_Embedding{{Model: strings.Repeat("a", 129), Dimension: 4}}, strings.Repeat("a", 129)), "must match"},
		{"duplicate keys", spacesConf([]*Vector_Embedding{valid, {Model: "m", Dimension: 8}}, "m"), "duplicate key"},
		{"empty default", spacesConf([]*Vector_Embedding{valid}, ""), "must reference a declared embedder key"},
		{"default undeclared", spacesConf([]*Vector_Embedding{valid}, "other"), "does not reference a declared embedder key"},
	}
	for _, tc := range cases {
		_, _, err := VectorSpaces(tc.conf)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: VectorSpaces = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}
