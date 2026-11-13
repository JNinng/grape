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
	llm   *conf.Agent_LLM
	mu    sync.Mutex
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

// runModelOptionResolver 包装 ack 的许可策略解析器：先保留原许可
// RunOption，再确定本次运行的文本模型——catalog 非 nil 时按租户解析，
// 否则沿用装配期静态模型——并按 forwardedProps 的思考强度覆盖本次运行的
// 生成配置，最后追加 agent.WithModel。身份缺失、模型停用/未知、密钥不可用
// 一律拒绝本次运行（fail-closed），绝不回落静态默认模型。
func runModelOptionResolver(
	catalog biz.TextModelCatalog,
	cache *textModelCache,
	fallback model.Model,
	base func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error),
) func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
	return func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
		opts, err := base(ctx, input)
		if err != nil {
			return nil, err
		}
		m := fallback
		var spec biz.TextModelSpec
		if catalog != nil {
			if _, ok := biz.IdentityFromContext(ctx); !ok {
				return nil, fmt.Errorf("agent: per-run model resolution requires an authenticated identity")
			}
			spec, err = catalog.ResolveTextModel(ctx)
			if err != nil {
				return nil, err
			}
			resolved, err := cache.get(spec)
			if err != nil {
				return nil, err
			}
			m = resolved
		}
		if m == nil {
			return opts, nil
		}
		if level, ok := thinkingLevelFromForwardedProps(input); ok {
			m = wrapThinkingModel(m, level)
		}
		// 生成预算来自模型目录：输出上限覆盖本次调用请求，上下文窗口交给
		// 框架做上下文裁剪与会话摘要。两者未声明（0）时沿用装配默认。
		m = wrapMaxOutputModel(m, spec.MaxOutputTokens)
		opts = append(opts, agent.WithModel(m))
		if spec.ContextWindow > 0 {
			opts = append(opts, agent.WithModelContextWindow(spec.ContextWindow))
		}
		return opts, nil
	}
}

// maxOutputModel 在每次模型调用前把模型目录声明的输出上限写入请求副本：
// 请求浅拷贝后修改，调用方持有的原请求保持不变。思考 token 计入
// MaxTokens，故上限必须覆盖推理与正文之和（目录侧保证不大于上下文窗口）。
type maxOutputModel struct {
	model.Model
	maxTokens int
}

// GenerateContent 覆盖本次调用的生成上限后转交下层模型。
func (m *maxOutputModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return m.Model.GenerateContent(ctx, withMaxOutputTokens(req, m.maxTokens))
}

// maxOutputIterModel 保留底层模型的迭代式流能力，包装后不退化到通道路径。
type maxOutputIterModel struct {
	*maxOutputModel
	iter model.IterModel
}

// GenerateContentIter 同 GenerateContent，走底层模型的迭代式接口。
func (m *maxOutputIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return m.iter.GenerateContentIter(ctx, withMaxOutputTokens(req, m.maxTokens))
}

// wrapMaxOutputModel 按输出上限包装模型；上限未声明（<=0）或模型为空时
// 原样返回。包装层无状态，可安全用于并发运行。
func wrapMaxOutputModel(m model.Model, maxTokens int) model.Model {
	if m == nil || maxTokens <= 0 {
		return m
	}
	base := &maxOutputModel{Model: m, maxTokens: maxTokens}
	if iter, ok := m.(model.IterModel); ok {
		return &maxOutputIterModel{maxOutputModel: base, iter: iter}
	}
	return base
}

// withMaxOutputTokens 复制请求并把生成上限写入副本。
func withMaxOutputTokens(req *model.Request, maxTokens int) *model.Request {
	if req == nil {
		req = &model.Request{}
	}
	cloned := *req
	cloned.GenerationConfig.MaxTokens = &maxTokens
	return &cloned
}
