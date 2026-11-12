// Package openaireranker 是 biz.Reranker 的 OpenAI 兼容端点实现（Jina /
// Cohere 风格 /rerank 协议，与 openaicompleter 同口径：base_url/api_key/
// model 由租户重排模型目录提供）。模型端点不在装配期固化：每次 Rerank 经
// biz.RerankModelCatalog 按当前身份动态解析（租户配置 > 全局配置选键，
// sys_model 租户/平台行优先），HTTP 客户端按 base_url + timeout 缓存。
// 只实现「query + documents 逐段打分」的最小面，不引入框架 reranker 与
// 其 SDK 依赖；biz 禁止触达 agent 框架适配层，本目录即检索侧重排的唯一
// 落点。无独立设施开关：可用性完全由模型解析决定（键完全未配置时
// Rerank 报 biz.ErrRerankUnavailable，消费方按跳过该层处理）。测试一律
// 以 httptest 替身服务进行，禁真实网络。
//
// 模型调用留痕：目录注入 biz.ModelCallObserver（nil 即零开销跳过），在
// 解析出端点事实之后、调用终态（含 error）观察一次调用一行；模型解析
// 失败（无端点事实）不留行。场景取 ctx 声明（biz.WithModelCallScene），
// 缺失回落 rerank；输入摘要记 query 文本（documents 即检索候选，已在
// 检索留痕承载），输出摘要只记分数条数说明（分数不落库）。
package openaireranker

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

// ProviderSet 是检索重排设施的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(New)

// defaultTimeout 是解析结果未设置超时的单次调用上限。
const defaultTimeout = 30 * time.Second

// New 构造 biz.Reranker。无独立设施开关：可用性完全由重排模型解析决定
// （租户与全局均未配置 ai.rerank.model 时 Rerank 报 biz.ErrRerankUnavailable，
// 消费方按跳过该层处理）。observer 是模型调用留痕的观察面挂点，nil 即
// 不留痕。
func New(catalog biz.RerankModelCatalog, observer biz.ModelCallObserver) (biz.Reranker, func(), error) {
	if catalog == nil {
		return nil, nil, errors.New("openaireranker: rerank model catalog is required")
	}
	return &dynamicReranker{catalog: catalog, observer: observer, clients: map[clientKey]*http.Client{}}, func() {}, nil
}

// clientKey 是 HTTP 客户端缓存键：端点与超时共同决定连接行为。
type clientKey struct {
	baseURL string
	timeout time.Duration
}

// dynamicReranker 是按当前身份动态解析重排模型端点的 /rerank 客户端。
type dynamicReranker struct {
	catalog  biz.RerankModelCatalog
	observer biz.ModelCallObserver

	mu      sync.Mutex
	clients map[clientKey]*http.Client
}

var _ biz.Reranker = (*dynamicReranker)(nil)

// rerankRequest / rerankResponse 是端点协议形状（只声明消费的字段）。
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerankUsage 是响应体的 token 用量段（留痕面消费；端点未上报为零值，
// 落库 NULL）。
type rerankUsage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CompletionTokens int32 `json:"completion_tokens"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
	Usage   rerankUsage    `json:"usage"`
}

// Rerank 实现 biz.Reranker：按当前身份解析重排模型端点，对 documents 逐
// 段打相关性分。空白 query 拒绝；空 documents 直接返回空切片（不解析
// 模型、不触网）；响应 results 必须恰好覆盖全部输入下标（缺失、越界或
// 重复均按协议错误处理）。模型解析成功即为端点事实成立，此后无论成败都
// 以一次调用一行留痕（调用终态含 error）；模型解析失败无端点事实，
// 不留行。
func (r *dynamicReranker) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("openaireranker: rerank query is required")
	}
	if len(documents) == 0 {
		return []float64{}, nil
	}
	spec, err := r.catalog.ResolveRerankModel(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	scores, usage, callErr := r.callRerank(ctx, spec, query, documents)
	r.observeCall(ctx, spec.Key, strings.TrimRight(spec.BaseURL, "/")+"/rerank", query, len(documents), usage, callErr, time.Since(start))
	return scores, callErr
}

// callRerank 执行协议调用并返回与输入顺序对齐的分数切片与用量段：端点
// 事实已解析，本函数内任何失败（配置残缺、协议错误、非 2xx、results
// 不覆盖全部输入）都是调用终态，由调用方留痕。
func (r *dynamicReranker) callRerank(ctx context.Context, spec biz.RerankModelSpec, query string, documents []string) ([]float64, rerankUsage, error) {
	var usage rerankUsage
	baseURL := strings.TrimRight(spec.BaseURL, "/")
	if baseURL == "" {
		return nil, usage, fmt.Errorf("openaireranker: rerank model %s has no base_url", spec.Key)
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	payload, err := json.Marshal(rerankRequest{Model: spec.Model, Query: query, Documents: documents})
	if err != nil {
		return nil, usage, fmt.Errorf("openaireranker: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/rerank", bytes.NewReader(payload))
	if err != nil {
		return nil, usage, fmt.Errorf("openaireranker: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if spec.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+spec.APIKey)
	}
	resp, err := r.client(clientKey{baseURL: baseURL, timeout: timeout}).Do(req)
	if err != nil {
		return nil, usage, fmt.Errorf("openaireranker: call rerank endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, usage, fmt.Errorf("openaireranker: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, usage, fmt.Errorf("openaireranker: rerank endpoint returned status %d: %s", resp.StatusCode, truncateBody(body))
	}
	var parsed rerankResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, usage, fmt.Errorf("openaireranker: decode response: %w", err)
	}
	scores, err := alignScores(parsed.Results, len(documents))
	if err != nil {
		return nil, parsed.Usage, err
	}
	return scores, parsed.Usage, nil
}

// alignScores 把端点 results 还原为与输入顺序对齐的分数切片：results
// 必须恰好覆盖 [0, n) 全部下标——条数不符、越界或重复都是协议错误
// （分数错位即重排结果错误，显式失败优于静默错序）。
func alignScores(results []rerankResult, n int) ([]float64, error) {
	if len(results) != n {
		return nil, fmt.Errorf("openaireranker: rerank response carries %d results, want %d", len(results), n)
	}
	scores := make([]float64, n)
	seen := make([]bool, n)
	for _, result := range results {
		if result.Index < 0 || result.Index >= n {
			return nil, fmt.Errorf("openaireranker: rerank result index %d out of range [0, %d)", result.Index, n)
		}
		if seen[result.Index] {
			return nil, fmt.Errorf("openaireranker: rerank result index %d duplicated", result.Index)
		}
		seen[result.Index] = true
		scores[result.Index] = result.RelevanceScore
	}
	return scores, nil
}

// observeCall 构造留痕载体并交给观察面：场景取 ctx 声明，缺失回落
// rerank；输入摘要记 query 文本，输出摘要只记分数条数说明（分数不落
// 留痕）；rerank 调用无 kb_document 行上下文，文档 ID 恒缺省 NULL。
// 观察面为 nil 时零开销跳过。
func (r *dynamicReranker) observeCall(ctx context.Context, modelKey, endpoint, query string, documents int, usage rerankUsage, callErr error, elapsed time.Duration) {
	if r.observer == nil {
		return
	}
	scenario := biz.ModelCallSceneFromContext(ctx)
	if scenario == "" {
		scenario = biz.ModelCallScenarioRerank
	}
	output := ""
	if callErr == nil {
		output = fmt.Sprintf("relevance scores of %d documents (scores not persisted)", documents)
	}
	entry := &biz.ModelCallEntry{
		Scenario:      scenario,
		ModelKey:      modelKey,
		Endpoint:      endpoint,
		InputSummary:  query,
		OutputSummary: output,
		InputTokens:   usage.PromptTokens,
		OutputTokens:  usage.CompletionTokens,
		DurationMS:    elapsed.Milliseconds(),
		Status:        biz.ModelCallStatusSuccess,
	}
	if callErr != nil {
		entry.Status = biz.ModelCallStatusError
		entry.Error = callErr.Error()
	}
	r.observer.ObserveModelCall(ctx, entry)
}

// client 取或构造按端点 + 超时复用的 HTTP 客户端。
func (r *dynamicReranker) client(key clientKey) *http.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	if client, ok := r.clients[key]; ok {
		return client
	}
	client := &http.Client{Timeout: key.timeout}
	r.clients[key] = client
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
