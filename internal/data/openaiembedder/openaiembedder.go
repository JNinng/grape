// Package openaiembedder 是 biz.Embedder 的 OpenAI 兼容端点实现（TODO
// 决策 5 同口径：base_url/api_key/model 进 conf，密钥经 ${ENV:...} 占位
// 注入）。只实现单文本 embeddings 调用的最小面，不引入框架 embedder 与
// 其 SDK 依赖；维度在构造期取 conf.vector.dimension 并在每次响应复核，
// 端点维度漂移在调用点显式报错。测试一律以 httptest 替身服务进行，
// 禁真实网络。
package openaiembedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
)

// ProviderSet 是 embedding 设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

const (
	// defaultTimeout 是 conf 未设置超时时的单次调用上限。
	defaultTimeout = 30 * time.Second
	// maxDimension 与 pg_sqlc 侧的向量列维度上限一致（pgvector 单列
	// 维度的类型系统硬上限），构造期对 conf.vector.dimension 复核。
	maxDimension = 16000
)

// New 构造 biz.Embedder。conf.vector.enabled=false 时返回禁用占位实现
// （所有调用按设施缺失拒绝，不发网络请求）；启用时校验配置完整并
// fail-fast：dimension 在 (0, 16000]、base_url 与 model 必填。embedding
// 模型的真实产出维度无法离线探知，维度对账的执行点在每次 Embed 响应
// 复核 + DocumentUsecase 装配期与 conf 对账（biz 侧），与 D1 探针
// （conf vs DDL）共同构成维度链路的三道校验。
func New(c *conf.Vector) (biz.Embedder, func(), error) {
	if !c.GetEnabled() {
		return disabledEmbedder{}, func() {}, nil
	}
	dimension := c.GetDimension()
	if dimension <= 0 || dimension > maxDimension {
		return nil, nil, fmt.Errorf("openaiembedder: vector dimension must be in (0, %d], got %d", maxDimension, dimension)
	}
	emb := c.GetEmbedding()
	baseURL := strings.TrimRight(emb.GetBaseUrl(), "/")
	if baseURL == "" {
		return nil, nil, errors.New("openaiembedder: vector.embedding.base_url is required when the vector facility is enabled")
	}
	if emb.GetModel() == "" {
		return nil, nil, errors.New("openaiembedder: vector.embedding.model is required when the vector facility is enabled")
	}
	timeout := emb.GetTimeout().AsDuration()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	e := &openAIEmbedder{
		baseURL:   baseURL,
		apiKey:    emb.GetApiKey(),
		model:     emb.GetModel(),
		dimension: int(dimension),
		client:    &http.Client{Timeout: timeout},
	}
	return e, func() {}, nil
}

// openAIEmbedder 是 OpenAI 兼容 embeddings 端点的轻量客户端：POST
// {base_url}/embeddings，请求体 {"model":..., "input":...}，响应取
// data[0].embedding。API key 以 Bearer 头下发（配置留空则不带该头，
// 适配本地网关等免认证端点）。
type openAIEmbedder struct {
	baseURL   string
	apiKey    string
	model     string
	dimension int
	client    *http.Client
}

var _ biz.Embedder = (*openAIEmbedder)(nil)

// Dimensions 实现 biz.Embedder：返回 conf 声明的期望维度。
func (e *openAIEmbedder) Dimensions() int { return e.dimension }

// embedRequest / embedResponse 是端点协议形状（只声明消费的字段）。
type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed 实现 biz.Embedder：单文本向量化。空白文本拒绝；非 2xx、响应
// 缺 data[0]、向量长度与声明维度不符均为错误（绝不返回零值向量落库）。
func (e *openAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("openaiembedder: embedding text is required")
	}
	payload, err := json.Marshal(embedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: call embeddings endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("openaiembedder: embeddings endpoint returned status %d: %s", resp.StatusCode, truncateBody(body))
	}
	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openaiembedder: decode response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("openaiembedder: embeddings response carries no data")
	}
	vector := parsed.Data[0].Embedding
	if len(vector) != e.dimension {
		return nil, fmt.Errorf("openaiembedder: embedding dimension %d does not match configured %d; align conf.vector.dimension with the embedding model",
			len(vector), e.dimension)
	}
	out := make([]float32, len(vector))
	for i, v := range vector {
		out[i] = float32(v)
	}
	return out, nil
}

// truncateBody 收敛错误响应体长度，避免网关错误页撑爆日志；按 rune
// 边界截断，不产出非法 UTF-8。
func truncateBody(body []byte) string {
	const limit = 512
	runes := []rune(strings.TrimSpace(string(body)))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

// disabledEmbedder 是设施未启用时的占位实现：所有调用按设施缺失拒绝。
type disabledEmbedder struct{}

var _ biz.Embedder = (*disabledEmbedder)(nil)

func (disabledEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, biz.ErrEmbedderDisabled
}

func (disabledEmbedder) Dimensions() int { return 0 }
