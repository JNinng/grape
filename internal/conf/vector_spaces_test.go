package conf

import (
	"strings"
	"testing"
)

// 向量设施嵌入空间的解析与校验单测（纯内存）：键派生、注册表合法性、
// 全局默认引用。消费方（openaiembedder / 向量仓储 / 解析器）共用本文件，
// 校验语义只在此断言一次。

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
	}
	for _, tc := range cases {
		if got := EmbedderSpaceKey(tc.model, tc.version); got != tc.want {
			t.Fatalf("EmbedderSpaceKey(%q, %q) = %q, want %q", tc.model, tc.version, got, tc.want)
		}
	}
}

func TestVectorSpaces_valid_config_resolves_registry(t *testing.T) {
	spaces, def, err := VectorSpaces(spacesConf([]*Vector_Embedding{
		{Model: "bge-m3", Version: "v1", Dimension: 1024},
		{Model: "bge-m3", Version: "v2", Dimension: 1024},
		{Model: "other-model", Dimension: 4},
	}, "bge-m3@v1"))
	if err != nil {
		t.Fatalf("VectorSpaces(valid) = %v, want nil", err)
	}
	if def != "bge-m3@v1" {
		t.Fatalf("default = %q, want bge-m3@v1", def)
	}
	if len(spaces) != 3 || spaces["bge-m3@v1"] != 1024 || spaces["bge-m3@v2"] != 1024 || spaces["other-model"] != 4 {
		t.Fatalf("spaces = %v, want three keys with declared dimensions", spaces)
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
