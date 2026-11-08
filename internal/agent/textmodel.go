package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// textModelCache 按 spec 可变字段缓存模型实例：租户切换模型只影响后续
// 运行，实例复用避免每次运行重建 provider 与可靠性包装。缓存键包含全部
// 可变字段（含密钥），端点变更即新实例，绝不残留旧配置。
type textModelCache struct {
	llm  *conf.Agent_LLM
	mu   sync.Mutex
	byKey map[string]model.Model
}

func newTextModelCache(llm *conf.Agent_LLM) *textModelCache {
	return &textModelCache{llm: llm, byKey: map[string]model.Model{}}
}

// get 按 spec 取或构造整条模型链：克隆全局 conf.Agent_LLM（保留
// failover/hedge/logging），只替换 provider/base_url/api_key/model/timeout
// 后走 newLLMModel——租户覆盖不改变可靠性策略。
func (c *textModelCache) get(spec biz.TextModelSpec) (model.Model, error) {
	if c.llm == nil {
		return nil, fmt.Errorf("agent: agent.llm config is required to build per-run models")
	}
	key := textModelCacheKey(spec)
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.byKey[key]; ok {
		return m, nil
	}
	cloned := proto.Clone(c.llm).(*conf.Agent_LLM)
	cloned.Provider = spec.Provider
	cloned.BaseUrl = spec.BaseURL
	cloned.Model = spec.Model
	cloned.ApiKey = spec.APIKey
	if spec.Timeout > 0 {
		cloned.Timeout = durationpb.New(spec.Timeout)
	}
	m, err := newLLMModel(cloned)
	if err != nil {
		return nil, err
	}
	c.byKey[key] = m
	return m, nil
}

// textModelCacheKey 拼接 spec 全部可变字段；仅内存使用，不入日志。
func textModelCacheKey(spec biz.TextModelSpec) string {
	return spec.Key + "\x00" + spec.Provider + "\x00" + spec.BaseURL + "\x00" +
		spec.Model + "\x00" + spec.APIKey + "\x00" + timeoutKey(spec.Timeout)
}

func timeoutKey(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return d.String()
}

// catalogRunOptionResolver 包装 ack 的许可策略解析器：先保留原许可
// RunOption，再按本次运行的租户解析文本模型并追加 agent.WithModel。
// 身份缺失、模型停用/未知、密钥不可用一律拒绝本次运行（fail-closed），
// 绝不回落静态默认模型。
func catalogRunOptionResolver(
	catalog biz.TextModelCatalog,
	cache *textModelCache,
	base func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error),
) func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
	return func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
		opts, err := base(ctx, input)
		if err != nil {
			return nil, err
		}
		if _, ok := biz.IdentityFromContext(ctx); !ok {
			return nil, fmt.Errorf("agent: per-run model resolution requires an authenticated identity")
		}
		spec, err := catalog.ResolveTextModel(ctx)
		if err != nil {
			return nil, err
		}
		m, err := cache.get(spec)
		if err != nil {
			return nil, err
		}
		return append(opts, agent.WithModel(m)), nil
	}
}
