// Package openaiembedder 是 biz.Embedder 的 OpenAI 兼容端点实现（与 LLM provider 同口径：base_url/api_key/model 由模型目录或 conf 提供）。空间
// 端点不再在装配期固化：每次 Embed 经 biz.EmbedderResolver 按空间键动态
// 解析（模型目录租户/平台行优先，conf.vector.embedders 兜底），HTTP
// 客户端按 base_url + timeout 缓存。只实现单文本 embeddings 调用的最小面，
// 不引入框架 embedder 与其 SDK 依赖；每次响应按解析出的空间维度复核，
// 端点维度漂移在调用点显式报错。测试一律以 httptest 替身服务进行，禁真实
// 网络。
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
	"sync"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/google/wire"
)

// ProviderSet 是 embedding 设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// defaultTimeout 是解析结果未设置超时的单次调用上限。
const defaultTimeout = 30 * time.Second

// New 构造 biz.Embedder。conf.vector.enabled=false 时返回禁用占位实现（不发
// 网络请求）；启用时先经 conf.VectorSpaces 校验空间清单 fail-fast，实际
// 端点则由 resolver 在每次调用时解析。模型真实产出维度无法离线探知，
// 维度对账在每次 Embed 响应复核与存储层索引维度对账。
func New(resolver biz.EmbedderResolver, c *conf.Vector) (biz.Embedder, func(), error) {
	if !c.GetEnabled() {
		return disabledEmbedder{}, func() {}, nil
	}
	if resolver == nil {
		return nil, nil, errors.New("openaiembedder: embedder resolver is required")
	}
	if _, _, err := conf.VectorSpaces(c); err != nil {
		return nil, nil, err
	}
	for i, emb := range c.GetEmbedders() {
		if strings.TrimRight(emb.GetBaseUrl(), "/") == "" {
			return nil, nil, fmt.Errorf("openaiembedder: vector.embedders[%d] (%s) base_url is required when the vector facility is enabled",
				i, conf.EmbedderSpaceKey(emb.GetModel(), emb.GetVersion()))
		}
	}
	return &dynamicEmbedder{resolver: resolver, clients: map[clientKey]*http.Client{}, dims: map[string]int{}}, func() {}, nil
}

// clientKey 是 HTTP 客户端缓存键：端点与超时共同决定连接行为。
type clientKey struct {
	baseURL string
	timeout time.Duration
}

// dynamicEmbedder 是按空间键动态解析端点的 embeddings 客户端。
type dynamicEmbedder struct {
	resolver biz.EmbedderResolver

	mu      sync.Mutex
	clients map[clientKey]*http.Client
	dims    map[string]int // 最近一次解析/调用观察到的空间维度（Dimension 无 ctx）
}

var _ biz.Embedder = (*dynamicEmbedder)(nil)

// Dimension 实现 biz.Embedder：返回最近一次解析或调用观察到的维度；
// 未知空间报 ErrEmbedderUnknown（本接口无 ctx，无法现场解析）。
func (e *dynamicEmbedder) Dimension(embedder string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if dim, ok := e.dims[embedder]; ok {
		return dim, nil
	}
	return 0, fmt.Errorf("%w: %q has not been resolved yet", biz.ErrEmbedderUnknown, embedder)
}

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

// Embed 实现 biz.Embedder：按空间键经 resolver 解析端点，单文本向量化。
// 空白文本与未知空间拒绝；非 2xx、响应缺 data[0]、向量长度与该空间声明
// 维度不符均为错误（绝不返回零值向量落库）。
func (e *dynamicEmbedder) Embed(ctx context.Context, embedder string, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("openaiembedder: embedding text is required")
	}
	space, err := e.resolver.ResolveEmbeddingSpace(ctx, embedder)
	if err != nil {
		return nil, err
	}
	if space.Key == "" {
		space.Key = embedder
	}
	if space.Dimension <= 0 {
		return nil, fmt.Errorf("openaiembedder: space %s declares dimension %d", embedder, space.Dimension)
	}
	baseURL := strings.TrimRight(space.BaseURL, "/")
	if baseURL == "" {
		return nil, fmt.Errorf("openaiembedder: space %s has no base_url", embedder)
	}
	timeout := space.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	e.rememberDimension(embedder, space.Dimension)

	payload, err := json.Marshal(embedRequest{Model: space.Model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if space.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+space.APIKey)
	}
	resp, err := e.client(clientKey{baseURL: baseURL, timeout: timeout}).Do(req)
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
	if len(vector) != space.Dimension {
		return nil, fmt.Errorf("openaiembedder: embedding dimension %d does not match configured %d for space %s",
			len(vector), space.Dimension, embedder)
	}
	out := make([]float32, len(vector))
	for i, v := range vector {
		out[i] = float32(v)
	}
	return out, nil
}

// rememberDimension 记录空间维度供无 ctx 的 Dimension 查询。
func (e *dynamicEmbedder) rememberDimension(embedder string, dimension int) {
	e.mu.Lock()
	e.dims[embedder] = dimension
	e.mu.Unlock()
}

// client 取或构造按端点 + 超时复用的 HTTP 客户端。
func (e *dynamicEmbedder) client(key clientKey) *http.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.clients[key]; ok {
		return c
	}
	c := &http.Client{Timeout: key.timeout}
	e.clients[key] = c
	return c
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

func (disabledEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	return nil, biz.ErrEmbedderDisabled
}

func (disabledEmbedder) Dimension(string) (int, error) { return 0, biz.ErrEmbedderDisabled }
