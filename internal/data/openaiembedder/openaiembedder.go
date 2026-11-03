// Package openaiembedder 是 biz.Embedder 的 OpenAI 兼容端点实现（与 LLM provider 同口径：base_url/api_key/model 进 conf，密钥经 ${ENV:...} 占位
// 注入）。只实现单文本 embeddings 调用的最小面，不引入框架 embedder 与
// 其 SDK 依赖；空间注册表（键→端点/维度）经 conf.VectorSpaces 单一解析
// 源构建，每次 Embed 响应按所属空间维度复核，端点维度漂移在调用点显式
// 报错。测试一律以 httptest 替身服务进行，禁真实网络。
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
	// defaultTimeout 是 conf 未设置超时的单次调用上限。
	defaultTimeout = 30 * time.Second
)

// New 构造 biz.Embedder。conf.vector.enabled=false 时返回禁用占位实现
// （所有调用按设施缺失拒绝，不发网络请求）；启用时经 conf.VectorSpaces
// 校验空间清单（键唯一、维度合法、default_embedder 引用已声明键）并
// fail-fast，逐空间要求 base_url 必填。embedding 模型的真实产出维度
// 无法离线探知，维度对账的执行点在每次 Embed 响应复核 + 存储层按注册
// 表对账（biz 契约声明三方一致才落库），与 DDL 探针共同构成空间纪律
// 的校验链。
func New(c *conf.Vector) (biz.Embedder, func(), error) {
	if !c.GetEnabled() {
		return disabledEmbedder{}, func() {}, nil
	}
	spaces, _, err := conf.VectorSpaces(c)
	if err != nil {
		return nil, nil, err
	}
	e := &multiEmbedder{spaces: make(map[string]*spaceEndpoint, len(c.GetEmbedders()))}
	for _, emb := range c.GetEmbedders() {
		key := conf.EmbedderSpaceKey(emb.GetModel(), emb.GetVersion())
		baseURL := strings.TrimRight(emb.GetBaseUrl(), "/")
		if baseURL == "" {
			return nil, nil, fmt.Errorf("openaiembedder: vector.embedders (%s) base_url is required when the vector facility is enabled", key)
		}
		timeout := emb.GetTimeout().AsDuration()
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		e.spaces[key] = &spaceEndpoint{
			model:     emb.GetModel(),
			baseURL:   baseURL,
			apiKey:    emb.GetApiKey(),
			dimension: int(spaces[key]),
			client:    &http.Client{Timeout: timeout},
		}
	}
	return e, func() {}, nil
}

// spaceEndpoint 是单个嵌入空间的端点客户端：空间即路由单位，模型与
// 维度声明随空间持有。
type spaceEndpoint struct {
	model     string
	baseURL   string
	apiKey    string
	dimension int
	client    *http.Client
}

// multiEmbedder 是多空间 embeddings 客户端：Embed 按空间键路由，未知
// 空间显式拒绝（不静默换空间）。
type multiEmbedder struct {
	spaces map[string]*spaceEndpoint
}

var _ biz.Embedder = (*multiEmbedder)(nil)

// Dimension 实现 biz.Embedder：返回空间声明的期望维度。
func (e *multiEmbedder) Dimension(embedder string) (int, error) {
	s, ok := e.spaces[embedder]
	if !ok {
		return 0, fmt.Errorf("%w: %q is not declared in conf.vector.embedders", biz.ErrEmbedderUnknown, embedder)
	}
	return s.dimension, nil
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

// Embed 实现 biz.Embedder：单文本向量化。空白文本与未知空间拒绝；非
// 2xx、响应缺 data[0]、向量长度与该空间声明维度不符均为错误（绝不返回
// 零值向量落库）。
func (e *multiEmbedder) Embed(ctx context.Context, embedder string, text string) ([]float32, error) {
	s, ok := e.spaces[embedder]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not declared in conf.vector.embedders", biz.ErrEmbedderUnknown, embedder)
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("openaiembedder: embedding text is required")
	}
	payload, err := json.Marshal(embedRequest{Model: s.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaiembedder: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.client.Do(req)
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
	if len(vector) != s.dimension {
		return nil, fmt.Errorf("openaiembedder: embedding dimension %d does not match configured %d for space %s; align conf.vector.embedders dimension with the embedding model",
			len(vector), s.dimension, embedder)
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

func (disabledEmbedder) Embed(context.Context, string, string) ([]float32, error) {
	return nil, biz.ErrEmbedderDisabled
}

func (disabledEmbedder) Dimension(string) (int, error) { return 0, biz.ErrEmbedderDisabled }
