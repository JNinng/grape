package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"grape/internal/conf"
	"grape/pkg/redact"

	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/failover"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// newLLMModel 按配置构造模型链（OpenAI 兼容直连；模型层
// 可靠性）。装配层次从内到外固定为：裸 provider（主 + failover 备选）→
// hedge（只包主模型）→ failover（包全部候选）→ logging（每台候选内层，
// 见 llmCandidates）。各可靠性段 enabled=false（默认）时跳过对应包装，
// 全部缺省即返回裸 provider——与未引入可靠性段时逐字节等价。
//
// 层次取舍说明：hedge 只包主模型——备选路径本身是故障路径，再叠加重复
// 请求只会放大故障面的调用量；failover 不与 hedge 叠加语义（备选不做
// hedge），全部候选失败时以框架聚合错误响应上抛。模型层重试：框架
// model 层无内建重试（仅 tool 层有 ToolCallRetryPolicy，与模型调用非同
// 一面），跨 provider 的 failover 即本层的重试维度，不另造自制重试包装。
func newLLMModel(c *conf.Agent_LLM) (model.Model, error) {
	candidates, err := llmCandidates(c)
	if err != nil {
		return nil, err
	}
	return composeModel(c, candidates)
}

// llmCandidates 构造候选模型清单：主模型在前，failover 备选按配置顺序
// 在后。每台候选独立套 logging 包装（enabled 时），使切换过程每次候选
// 尝试都有调用记录。备选的 provider/base_url/api_key/model/timeout 与
// 主 provider 同构；备选 model 名必填，缺失属装配错误拒绝启动。
func llmCandidates(c *conf.Agent_LLM) ([]model.Model, error) {
	if c == nil {
		return nil, fmt.Errorf("agent: llm config is required")
	}
	if c.GetModel() == "" {
		return nil, fmt.Errorf("agent: llm model is required")
	}
	failoverConf := c.GetFailover()
	if failoverConf.GetEnabled() && len(failoverConf.GetCandidates()) == 0 {
		return nil, fmt.Errorf("agent: llm failover enabled but no candidates configured")
	}
	logging := c.GetLogging()
	candidates := make([]model.Model, 0, len(failoverConf.GetCandidates())+1)
	candidates = append(candidates, wrapLogging(
		newOpenAIModel(c.GetProvider(), c.GetBaseUrl(), c.GetApiKey(), c.GetModel(), c.GetTimeout()),
		logging,
	))
	for i, cand := range failoverConf.GetCandidates() {
		if cand.GetModel() == "" {
			return nil, fmt.Errorf("agent: llm failover candidate %d model is required", i)
		}
		candidates = append(candidates, wrapLogging(
			newOpenAIModel(cand.GetProvider(), cand.GetBaseUrl(), cand.GetApiKey(), cand.GetModel(), cand.GetTimeout()),
			logging,
		))
	}
	return candidates, nil
}

// newOpenAIModel 构造单台 OpenAI 兼容模型适配器。provider 命中框架变体
// 时直接透传：base_url 为空由框架取该变体的内置默认端点，API key 也支持
// 从变体约定环境变量回退。模型调用超时落在 HTTP client 上（conf 逐候选
// 参数化），未配置时用框架默认传输参数。api_key 只进入 HTTP client 配置
// 面，模型请求/响应载体（model.Request/Response）不含凭据，日志面天然
// 无密钥（测试有守卫断言）。
func newOpenAIModel(provider, baseURL, apiKey, name string, timeout *durationpb.Duration) model.Model {
	opts := []openai.Option{}
	if v, ok := providerVariant(provider); ok {
		opts = append(opts, openai.WithVariant(v))
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}
	if d := timeout.AsDuration(); d > 0 {
		opts = append(opts, openai.WithHTTPClientOptions(
			openai.WithHTTPClientTimeout(d),
		))
	}
	return openai.New(name, opts...)
}

// composeModel 把可靠性包装按固定次序套到候选清单上（hedge → failover），
// logging 已在 llmCandidates 内套到每台候选。单独成函数以便以替身候选
// 单测组合语义（不触真实 provider）。candidates 切片入参不回写。
func composeModel(c *conf.Agent_LLM, candidates []model.Model) (model.Model, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("agent: llm requires at least one candidate model")
	}
	composed := append([]model.Model(nil), candidates...)
	if hedgeConf := c.GetHedge(); hedgeConf.GetEnabled() {
		hedged, err := newHedgeModel(hedgeConf, composed[0])
		if err != nil {
			return nil, err
		}
		composed[0] = hedged
	}
	if c.GetFailover().GetEnabled() {
		return failover.New(failover.WithCandidates(composed...))
	}
	return composed[0], nil
}

// newHedgeModel 把主模型包装为 hedge 模型（语义见 conf.proto
// Hedge 段注释）：原始请求先发，delay 后未出有效内容即发副本（同一
// provider 的重复请求），首个有效内容获胜、其余全部取消。delay 0 取框
// 架默认 100ms；副本数启用时必须 ≥ 2（单副本 hedge 无意义，按误配拒绝
// 启动），上限即 max_copies 本身——框架按副本清单错峰启动，无隐式上限。
func newHedgeModel(c *conf.Agent_LLM_Hedge, primary model.Model) (model.Model, error) {
	copies := int(c.GetMaxCopies())
	if copies < 2 {
		return nil, fmt.Errorf("agent: llm hedge enabled requires max_copies >= 2, got %d", copies)
	}
	models := make([]model.Model, copies)
	for i := range models {
		models[i] = primary
	}
	opts := []hedge.Option{hedge.WithCandidates(models...)}
	// delay 负值按误配拒绝启动（与 max_copies 的校验口径一致）；0 取
	// 框架默认 100ms。
	if d := c.GetDelay().AsDuration(); d < 0 {
		return nil, fmt.Errorf("agent: llm.hedge.delay must not be negative, got %s", d)
	} else if d > 0 {
		opts = append(opts, hedge.WithDelay(d))
	}
	return hedge.New(opts...)
}

// wrapLogging 按 logging 配置决定是否套回调日志包装：enabled=false（默
// 认）原样返回，零包装零日志。
func wrapLogging(m model.Model, c *conf.Agent_LLM_Logging) model.Model {
	if c == nil || !c.GetEnabled() {
		return m
	}
	return newLoggingModel(m, c)
}

// promptLogLimit 是提示词/补全文本入日志的截断上限（rune）。
const promptLogLimit = 512

// llmContentRedactor 是入日志提示词/补全的截断器：沿用 pkg/redact 检索
// 内容的截断形态（单一脱敏代码点）。本处只做开关门控与截断；内容级脱
// 敏（工具敏感参数/检索内容等）的脱敏归审计与复现视图面。
var llmContentRedactor = redact.NewTruncate(promptLogLimit, "...(truncated)")

// llmLoggerOf 返回 LLM 日志落点：生产为进程默认 logger（main.go 经
// kratos log.SetDefault 注入 grape JSON logger，且 kratos v3 的
// SetDefault 即 slog.SetDefault）——ctx 携带 SpanContext 时 trace_id /
// span_id 由 logger extractor 自动展开（E1 桥接口径）。变量间接层仅供
// 测试替换捕获输出。
var llmLoggerOf = func() *slog.Logger { return slog.Default() }

// callCompletionsKey 携带单次调用的补全累积器：包装层在请求 ctx 上放入，
// after 回调取出。流式补全正文分散在中间分片，包装层在透传时顺带累积，
// 调用结束一并记录；ctx 不改任何既有值，对下层调用无副作用。
type callCompletionsKey struct{}

// loggingModel 是 LLM 调用的观测包装：经框架
// model.Callbacks 注册前后回调（model/callbacks.go 形态），把每次模型
// 调用落 grape logger。与 llmagent.WithModelCallbacks 的完整回调语义
// （可改写请求、短路返回 CustomResponse）不同，本包装的回调是纯观测：
// 不消费 CustomResponse、不改写请求；回调执行错误只记不传——观测面不
// 打断模型调用。形态迁移路径：把 log 字段注册到 LLMAgent 选项即可升级
// 为框架完整回调面（届时补全文本改由聚合响应携带，ctx 累积器可弃）。
type loggingModel struct {
	inner model.Model
	log   *model.Callbacks
	conf  *conf.Agent_LLM_Logging
}

// newLoggingModel 构造观测包装并注册前后回调。回调经 Register 注册而非
// 直接调用，保持框架 model.Callbacks 的执行面（panic 恢复、顺序执行）。
func newLoggingModel(inner model.Model, c *conf.Agent_LLM_Logging) *loggingModel {
	m := &loggingModel{inner: inner, conf: c, log: model.NewCallbacks()}
	m.log.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		logLLMRequest(ctx, c, inner.Info().Name, args.Request)
		return &model.BeforeModelResult{}, nil
	})
	m.log.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		logLLMResponse(ctx, c, inner.Info().Name, args.Response, args.Error, completionsFromContext(ctx))
		return &model.AfterModelResult{}, nil
	})
	return m
}

// Info 透传内层模型信息（failover/hedge 依赖 Info().Name 标识候选）。
func (m *loggingModel) Info() model.Info { return m.inner.Info() }

// InputTokenBudget 透传内层模型的可选预算接口：failover/hedge 聚合跨候
// 选最小预算、框架 summarizer 取 token 预算时都按该接口类型断言——包装
// 层不透传会静默把预算退化为 0，改变「开日志不改语义」的缺省等价口径。
func (m *loggingModel) InputTokenBudget(ctx context.Context, request *model.Request) int {
	type budgeter interface {
		InputTokenBudget(ctx context.Context, request *model.Request) int
	}
	b, ok := m.inner.(budgeter)
	if !ok {
		return 0
	}
	return b.InputTokenBudget(ctx, request)
}

// GenerateContent 实现流式模型调用：前回调（请求日志）→ 内层调用 →
// 透传分片并累积补全 → 流结束后回调（响应日志）。内层系统级错误同样
// 走 after 回调（记系统错误）后原样上抛。
func (m *loggingModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ctx, err := m.before(ctx, request)
	if err != nil {
		return nil, err
	}
	ch, err := m.inner.GenerateContent(ctx, request)
	if err != nil {
		m.runAfter(ctx, request, nil, err)
		return nil, err
	}
	out := make(chan *model.Response)
	go func() {
		defer close(out)
		var last *model.Response
		var completions strings.Builder
		for resp := range ch {
			if resp != nil {
				last = resp
				appendCompletion(&completions, resp)
			}
			select {
			case out <- resp:
			case <-ctx.Done():
				return
			}
		}
		m.runAfter(withCompletions(ctx, completions.String()), request, last, nil)
	}()
	return out, nil
}

// GenerateContentIter 实现调用方协程内的流式序列（框架 llmflow 优先走
// 此形态）：内层实现 IterModel 时直接透传序列，否则把内层 channel 适配
// 为序列。补全累积与前/后回调语义同 GenerateContent。
func (m *loggingModel) GenerateContentIter(
	ctx context.Context,
	request *model.Request,
) (model.Seq[*model.Response], error) {
	ctx, err := m.before(ctx, request)
	if err != nil {
		return nil, err
	}
	var seq model.Seq[*model.Response]
	if im, ok := m.inner.(model.IterModel); ok {
		seq, err = im.GenerateContentIter(ctx, request)
	} else {
		var ch <-chan *model.Response
		ch, err = m.inner.GenerateContent(ctx, request)
		if err == nil {
			seq = func(yield func(*model.Response) bool) {
				for resp := range ch {
					if !yield(resp) {
						return
					}
				}
			}
		}
	}
	if err != nil {
		m.runAfter(ctx, request, nil, err)
		return nil, err
	}
	return func(yield func(*model.Response) bool) {
		var last *model.Response
		var completions strings.Builder
		seq(func(resp *model.Response) bool {
			if resp != nil {
				last = resp
				appendCompletion(&completions, resp)
			}
			return yield(resp)
		})
		m.runAfter(withCompletions(ctx, completions.String()), request, last, nil)
	}, nil
}

// before 执行前回调（请求日志），并把补全累积器挂上 ctx。回调错误原样
// 上抛（与框架语义一致：前回调失败即中止本次模型调用；本包装注册的回
// 调只记日志，不会返回错误）。
func (m *loggingModel) before(ctx context.Context, request *model.Request) (context.Context, error) {
	ctx = withCompletions(ctx, "")
	if _, err := m.log.RunBeforeModel(ctx, &model.BeforeModelArgs{Request: request}); err != nil {
		llmLoggerOf().ErrorContext(ctx, "llm model before callback failed", slog.String("model", m.inner.Info().Name), slog.Any("error", err))
		return ctx, err
	}
	return ctx, nil
}

// runAfter 执行后回调（响应日志）。流已开始（channel/序列路径）后回调
// 错误无处上抛，只记日志——观测面不改变调用结果。
func (m *loggingModel) runAfter(ctx context.Context, request *model.Request, resp *model.Response, sysErr error) {
	var modelErr error
	if resp != nil && resp.Error != nil {
		modelErr = fmt.Errorf("%s: %s", resp.Error.Type, resp.Error.Message)
	}
	if _, err := m.log.RunAfterModel(ctx, &model.AfterModelArgs{Request: request, Response: resp, Error: modelErr}); err != nil {
		llmLoggerOf().ErrorContext(ctx, "llm model after callback failed", slog.String("model", m.inner.Info().Name), slog.Any("error", err))
	}
}

// logLLMRequest 记录单次模型调用请求：调用面元数据恒记（模型名、消息
// 数），提示词文本仅 log_prompts 开启时记（按 rune 截断）。Info 级：每
// 次模型调用一行；本包装整体由 logging.enabled 门控，关闭时零日志。
func logLLMRequest(ctx context.Context, c *conf.Agent_LLM_Logging, name string, req *model.Request) {
	logger := llmLoggerOf()
	if req == nil {
		logger.InfoContext(ctx, "llm model request", slog.String("model", name), slog.String("request", "nil"))
		return
	}
	args := []any{
		slog.String("model", name),
		slog.Int("message_count", len(req.Messages)),
	}
	if c.GetLogPrompts() {
		args = append(args, slog.String("prompt", llmContentRedactor.Redact(renderPrompt(req.Messages))))
	}
	logger.InfoContext(ctx, "llm model request", args...)
}

// logLLMResponse 记录单次模型调用响应：模型名、终结标志恒记；错误响应
// 或系统错误升 Warn 并带错误信息（provider 错误文案，非凭据面）；补全
// 文本仅 log_completions 开启时记（累积后截断）；用量在响应携带时记。
func logLLMResponse(ctx context.Context, c *conf.Agent_LLM_Logging, name string, resp *model.Response, modelErr error, completions string) {
	logger := llmLoggerOf()
	if resp == nil {
		logger.WarnContext(ctx, "llm model response missing", slog.String("model", name), slog.Any("error", modelErr))
		return
	}
	args := []any{
		slog.String("model", name),
		slog.Bool("done", resp.Done),
	}
	failed := modelErr != nil
	if errResp := resp.Error; errResp != nil {
		failed = true
		args = append(args, slog.String("error_type", errResp.Type), slog.String("error", errResp.Message))
	}
	if u := resp.Usage; u != nil {
		args = append(args, slog.Int("prompt_tokens", u.PromptTokens), slog.Int("completion_tokens", u.CompletionTokens))
	}
	if c.GetLogCompletions() {
		args = append(args, slog.String("completion", llmContentRedactor.Redact(completions)))
	}
	if failed {
		logger.WarnContext(ctx, "llm model response", args...)
		return
	}
	logger.InfoContext(ctx, "llm model response", args...)
}

// renderPrompt 把消息清单平铺为 role: content 文本，供提示词日志。
func renderPrompt(msgs []model.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// appendCompletion 把响应块的选择文本累积进补全缓冲：非流式响应正文在
// Message，流式分片正文在 Delta，两处都收（错误块无正文，天然跳过）。
func appendCompletion(b *strings.Builder, resp *model.Response) {
	for _, choice := range resp.Choices {
		b.WriteString(choice.Message.Content)
		b.WriteString(choice.Delta.Content)
	}
}

// withCompletions / completionsFromContext 是补全累积器的 ctx 存取。
func withCompletions(ctx context.Context, s string) context.Context {
	return context.WithValue(ctx, callCompletionsKey{}, s)
}

func completionsFromContext(ctx context.Context) string {
	s, _ := ctx.Value(callCompletionsKey{}).(string)
	return s
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
