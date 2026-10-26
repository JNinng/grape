package agent

import (
	"fmt"

	"grape/internal/conf"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// newLLMModel 按配置构造 OpenAI 兼容模型适配器（决策 5：OpenAI 兼容直连）。
// provider 命中框架变体时直接透传：base_url 为空由框架取该变体的内置
// 默认端点，API key 也支持从变体约定环境变量回退。模型调用超时落在
// HTTP client 上，未配置时用框架默认传输参数。
func newLLMModel(c *conf.Agent_LLM) (model.Model, error) {
	if c == nil {
		return nil, fmt.Errorf("agent: llm config is required")
	}
	if c.GetModel() == "" {
		return nil, fmt.Errorf("agent: llm model is required")
	}
	opts := []openai.Option{}
	if v, ok := providerVariant(c.GetProvider()); ok {
		opts = append(opts, openai.WithVariant(v))
	}
	if c.GetBaseUrl() != "" {
		opts = append(opts, openai.WithBaseURL(c.GetBaseUrl()))
	}
	if c.GetApiKey() != "" {
		opts = append(opts, openai.WithAPIKey(c.GetApiKey()))
	}
	if d := c.GetTimeout().AsDuration(); d > 0 {
		opts = append(opts, openai.WithHTTPClientOptions(
			openai.WithHTTPClientTimeout(d),
		))
	}
	return openai.New(c.GetModel(), opts...), nil
}

// providerVariant 把配置的 provider 名映射为框架模型变体；空值与未识别
// 名按 openai 处理（变体未设置时框架还会按 base_url 自动推断）。
func providerVariant(name string) (openai.Variant, bool) {
	switch name {
	case "", "openai":
		return openai.VariantOpenAI, false
	case "deepseek":
		return openai.VariantDeepSeek, true
	case "qwen":
		return openai.VariantQwen, true
	case "glm":
		return openai.VariantGLM, true
	case "minimax":
		return openai.VariantMiniMax, true
	case "kimi":
		return openai.VariantKimi, true
	case "hunyuan":
		return openai.VariantHunyuan, true
	default:
		return openai.VariantOpenAI, false
	}
}
