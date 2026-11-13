package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// thinkingForwardedProp 是前端经 AG-UI forwardedProps 下发的思考强度键。
// 取值是封闭词表（off / low / medium / high）：缺失、非字符串或词表外
// 取值视为未指定，沿用装配期的生成配置，绝不猜测。
const thinkingForwardedProp = "thinking"

const (
	thinkingLevelOff    = "off"
	thinkingLevelLow    = "low"
	thinkingLevelMedium = "medium"
	thinkingLevelHigh   = "high"
)

// thinkingLevel 是一次运行的思考强度：关闭时不指定 effort；开启时 effort
// 取 provider 支持的取值。provider 不支持的档位由服务端自行归并（如
// DeepSeek v4 把 low / medium 映射为 high），本层不做替换。
type thinkingLevel struct {
	enabled bool
	effort  string
}

var thinkingLevels = map[string]thinkingLevel{
	thinkingLevelOff:    {enabled: false},
	thinkingLevelLow:    {enabled: true, effort: "low"},
	thinkingLevelMedium: {enabled: true, effort: "medium"},
	thinkingLevelHigh:   {enabled: true, effort: "high"},
}

// thinkingLevelFromForwardedProps 解析本次运行的思考强度：forwardedProps
// 是任意 JSON 对象（反序列化后为 map[string]any），非对象、缺键、非字符串
// 与词表外取值一律按未指定处理。
func thinkingLevelFromForwardedProps(input *adapter.RunAgentInput) (thinkingLevel, bool) {
	if input == nil {
		return thinkingLevel{}, false
	}
	props, ok := input.ForwardedProps.(map[string]any)
	if !ok {
		return thinkingLevel{}, false
	}
	raw, ok := props[thinkingForwardedProp]
	if !ok {
		return thinkingLevel{}, false
	}
	value, ok := raw.(string)
	if !ok {
		return thinkingLevel{}, false
	}
	level, ok := thinkingLevels[value]
	return level, ok
}

// thinkingModel 在每次模型调用前把本次运行的思考强度写入请求的生成配置：
// LLMAgent 的生成配置在装配期固定，按运行覆盖只能落在请求侧。请求浅拷贝
// 后修改，调用方持有的原请求保持不变。
type thinkingModel struct {
	model.Model
	level thinkingLevel
}

// GenerateContent 覆盖本次调用的思考开关与强度后转交下层模型。
func (m *thinkingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	return m.Model.GenerateContent(ctx, withThinkingLevel(req, m.level))
}

// thinkingIterModel 保留底层模型的迭代式流能力（IterModel），包装后不退化
// 到通道路径。
type thinkingIterModel struct {
	*thinkingModel
	iter model.IterModel
}

// GenerateContentIter 同 GenerateContent，走底层模型的迭代式接口。
func (m *thinkingIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	return m.iter.GenerateContentIter(ctx, withThinkingLevel(req, m.level))
}

// wrapThinkingModel 按强度包装模型；包装层无状态，可安全用于并发运行。
func wrapThinkingModel(m model.Model, level thinkingLevel) model.Model {
	if m == nil {
		return nil
	}
	base := &thinkingModel{Model: m, level: level}
	if iter, ok := m.(model.IterModel); ok {
		return &thinkingIterModel{thinkingModel: base, iter: iter}
	}
	return base
}

// withThinkingLevel 复制请求并把思考开关与强度写入生成配置：只在副本上
// 改，原请求不共享可变状态。关闭时清空 effort，避免沿用装配期的残留值。
func withThinkingLevel(req *model.Request, level thinkingLevel) *model.Request {
	if req == nil {
		req = &model.Request{}
	}
	cloned := *req
	enabled := level.enabled
	cloned.GenerationConfig.ThinkingEnabled = &enabled
	if level.enabled && level.effort != "" {
		effort := level.effort
		cloned.GenerationConfig.ReasoningEffort = &effort
	} else {
		cloned.GenerationConfig.ReasoningEffort = nil
	}
	return &cloned
}
