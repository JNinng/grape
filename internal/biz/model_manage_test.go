package biz

import (
	"errors"
	"testing"
)

// 模型目录用例的入参校验单测：嵌入维度本期固定 1024、model_key 白名单
// 允许冒号、文本模型不接受维度、更新路径必填状态。

func ptrInt32(v int32) *int32 { return &v }

func validEmbeddingModel() *ModelCfg {
	return &ModelCfg{
		Status:    ModelStatusUnspecified,
		Kind:      ModelKindEmbedding,
		Name:      "Embedding",
		ModelKey:  "qwen3-embedding:0.6b",
		Provider:  "openai",
		Model:     "qwen3-embedding:0.6b",
		Dimension: ptrInt32(ModelEmbeddingDimension),
	}
}

func TestValidateModelCfg_embedding_dimension_is_1024_only(t *testing.T) {
	if err := validateModelCfg(validEmbeddingModel(), false); err != nil {
		t.Fatalf("valid embedding model rejected: %v", err)
	}
	for _, dim := range []int32{768, 1536, 0} {
		m := validEmbeddingModel()
		m.Dimension = ptrInt32(dim)
		if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
			t.Fatalf("embedding dimension %d = %v, want ErrModelInvalidArgument", dim, err)
		}
	}
	missing := validEmbeddingModel()
	missing.Dimension = nil
	if err := validateModelCfg(missing, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("embedding without dimension = %v, want ErrModelInvalidArgument", err)
	}
}

func TestValidateModelCfg_model_key_whitelist_and_text_dimension(t *testing.T) {
	// 冒号承载 Ollama 标签，属合法键字符。
	m := validEmbeddingModel()
	m.ModelKey = "qwen3-embedding:0.6b"
	if err := validateModelCfg(m, false); err != nil {
		t.Fatalf("colon in model key rejected: %v", err)
	}
	// 斜杠与空格不在白名单内。
	for _, key := range []string{"bad/key", "bad key", ""} {
		m := validEmbeddingModel()
		m.ModelKey = key
		if err := validateModelCfg(m, false); !errors.Is(err, ErrModelInvalidArgument) {
			t.Fatalf("model key %q = %v, want ErrModelInvalidArgument", key, err)
		}
	}
	// 文本模型不接受维度。
	text := &ModelCfg{Kind: ModelKindText, Name: "Text", Model: "qwen3:8b", ModelKey: "qwen3:8b", Dimension: ptrInt32(ModelEmbeddingDimension)}
	if err := validateModelCfg(text, false); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("text model with dimension = %v, want ErrModelInvalidArgument", err)
	}
	// 更新路径状态必填（0/1），创建路径接受未指定哨兵。
	text.Dimension = nil
	text.Status = ModelStatusUnspecified
	if err := validateModelCfg(text, true); !errors.Is(err, ErrModelInvalidArgument) {
		t.Fatalf("update with unspecified status = %v, want ErrModelInvalidArgument", err)
	}
	if err := validateModelCfg(text, false); err != nil {
		t.Fatalf("create with unspecified status rejected: %v", err)
	}
}
