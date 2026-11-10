// Package openaicompleter 是 biz.Completer 的 OpenAI 兼容端点实现（chat
// completions，与 openaiembedder 同口径：base_url/api_key/model 由租户
// 文本模型目录或 conf 提供）。模型端点不在装配期固化：每次 Complete 经
// biz.TextModelCatalog 按当前身份动态解析（租户配置 > 全局配置 > conf 选
// 键，sys_model 租户/平台行优先），HTTP 客户端按 base_url + timeout 缓存。
// 只实现单轮 user message 生成的最小面，不引入框架 model 与其 SDK 依赖；
// biz 禁止触达 agent 框架适配层，本目录即管线侧文本生成的唯一落点。测试
// 一律以 httptest 替身服务进行，禁真实网络。
package openaicompleter

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

	"github.com/google/wire"
)

// ProviderSet 是文本生成设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// defaultTimeout 是解析结果未设置超时的单次调用上限。
const defaultTimeout = 30 * time.Second

// New 构造 biz.Completer。无独立设施开关：可用性完全由文本模型解析决定
// （租户未配置文本模型时 Complete 报 ErrTextModelUnavailable，消费方按
// 质量增强降级处理）。
func New(catalog biz.TextModelCatalog) (biz.Completer, func(), error) {
	if catalog == nil {
		return nil, nil, errors.New("openaicompleter: text model catalog is required")
	}
	return &dynamicCompleter{catalog: catalog, clients: map[clientKey]*http.Client{}}, func() {}, nil
}

// clientKey 是 HTTP 客户端缓存键：端点与超时共同决定连接行为。
type clientKey struct {
	baseURL string
	timeout time.Duration
}

// dynamicCompleter 是按当前身份动态解析文本模型端点的 chat completions
// 客户端。
type dynamicCompleter struct {
	catalog biz.TextModelCatalog

	mu      sync.Mutex
	clients map[clientKey]*http.Client
}

var _ biz.Completer = (*dynamicCompleter)(nil)

// completeRequest / completeResponse 是端点协议形状（只声明消费的字段）。
type completeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completeRequest struct {
	Model    string            `json:"model"`
	Messages []completeMessage `json:"messages"`
}

type completeResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Complete 实现 biz.Completer：按当前身份解析文本模型端点，单轮生成。
// 空白 prompt 拒绝；非 2xx、响应缺 choices[0] 均为错误。
func (c *dynamicCompleter) Complete(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("openaicompleter: completion prompt is required")
	}
	spec, err := c.catalog.ResolveTextModel(ctx)
	if err != nil {
		return "", err
	}
	baseURL := strings.TrimRight(spec.BaseURL, "/")
	if baseURL == "" {
		return "", fmt.Errorf("openaicompleter: text model %s has no base_url", spec.Key)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	payload, err := json.Marshal(completeRequest{
		Model:    spec.Model,
		Messages: []completeMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("openaicompleter: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("openaicompleter: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if spec.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+spec.APIKey)
	}
	resp, err := c.client(clientKey{baseURL: baseURL, timeout: timeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("openaicompleter: call chat completions endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("openaicompleter: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("openaicompleter: chat completions endpoint returned status %d: %s", resp.StatusCode, truncateBody(body))
	}
	var parsed completeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("openaicompleter: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("openaicompleter: chat completions response carries no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// client 取或构造按端点 + 超时复用的 HTTP 客户端。
func (c *dynamicCompleter) client(key clientKey) *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.clients[key]; ok {
		return client
	}
	client := &http.Client{Timeout: key.timeout}
	c.clients[key] = client
	return client
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
