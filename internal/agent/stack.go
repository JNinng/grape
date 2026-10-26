// Package agent 是 trpc-agent-go / ack 框架的适配层：模型、LLMAgent、
// runner、AG-UI handler 与 ack 网关只在这里构造，由 server 经 service
// 唯一挂载。分层约束（见 internal/README.md）：biz 禁止 import 本包与
// 任何框架包；service 只做手写端点与 DTO，不重复装配框架对象。
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/jninng/trpc_agent_go_ack"
	"github.com/jninng/trpc_agent_go_ack/ackhttp"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// agentDefault* 是配置缺省时的骨架默认值：AG-UI 运行超时的框架默认 1h
// 对将来未决确认的结算过宽；心跳不开则空闲长流可能被中间设备掐断。
const (
	defaultBasePath       = "/agui"
	defaultRunTimeout     = 30 * time.Minute
	defaultHeartbeat      = 15 * time.Second
	defaultAckSuffix      = "/ack"
	defaultInstruction    = "You are the grape assistant. Use the echo tool when the user asks you to repeat text."
	defaultMaxTokens      = 512
	defaultTemperature    = 0.7
	errMissingIdentityMsg = "agent: missing identity in context"
)

// errMissingIdentity 是认证身份缺失的统一错误：UserIDResolver 与 ack
// WithUserFunc 都以它 fail-closed，禁止回落框架默认用户 "user"。
var errMissingIdentity = errors.New(errMissingIdentityMsg)

// Stack 是 Agent 服务骨架的装配物。handler 是外层 ServeMux（AG-UI 路由
// + ackhttp 路由），server 层经 service 把它挂进 Kratos 中间件链；runner
// 持引用只为 Close 释放框架资源。
type Stack struct {
	handler  nethttp.Handler
	runner   runner.Runner
	basePath string
}

// StackOption 定制骨架装配，供测试注入替身（如 stub 模型）。
type StackOption func(*stackOptions)

type stackOptions struct {
	modelOverride model.Model
}

// WithModelOverride 用给定模型替换按配置构造的 LLM provider。
func WithModelOverride(m model.Model) StackOption {
	return func(o *stackOptions) { o.modelOverride = m }
}

// NewStack 按 conf 与注入的会话服务装配 Agent 骨架。装配顺序固定为
// 模型 → 工具 → LLMAgent → core runner → ack 网关 → AG-UI runner
// （ack 恢复路径续跑用它）→ AG-UI server → 外层 ServeMux；任一环失败
// 即返回错误，装配失败拒绝启动。
func NewStackWithOptions(c *conf.Agent, logger *slog.Logger, sessions session.Service, opts ...StackOption) (*Stack, error) {
	o := &stackOptions{}
	for _, opt := range opts {
		opt(o)
	}
	if c.GetAppName() == "" {
		return nil, fmt.Errorf("agent: app_name is required")
	}
	if sessions == nil {
		return nil, fmt.Errorf("agent: session service is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	llm, err := buildModel(c, o)
	if err != nil {
		return nil, err
	}

	// B1 骨架只挂 echo 只读工具；C1 起业务工具经 biz 用例接入。
	echoTools := []tool.Tool{newEchoTool()}
	ag := llmagent.New(c.GetAppName(),
		llmagent.WithModel(llm),
		llmagent.WithTools(echoTools),
		llmagent.WithInstruction(defaultInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      true,
			MaxTokens:   ptr(defaultMaxTokens),
			Temperature: ptr(defaultTemperature),
		}),
	)

	coreRunner := runner.NewRunner(c.GetAppName(), ag, runner.WithSessionService(sessions))

	// ack 网关骨架：app 名与 AG-UI runner 一致（会话定位的前提），
	// 分级/许可/策略在 B1 走默认与空实现，C1/C2 填充。
	gateway := newGateway(sessions, c.GetAppName(), logger, userKeyFromContext)

	runTimeout := runTimeoutOf(c)
	aguiRunner := aguirunner.New(coreRunner,
		aguirunner.WithAppName(c.GetAppName()),
		aguirunner.WithSessionService(sessions),
		aguirunner.WithUserIDResolver(resolveUserID),
		aguirunner.WithTimeout(runTimeout),
	)
	if err := gateway.Bind(aguiRunner); err != nil {
		return nil, fmt.Errorf("agent: bind ack resume runner: %w", err)
	}

	aguiServer, err := agui.New(coreRunner,
		agui.WithAppName(c.GetAppName()),
		agui.WithSessionService(sessions),
		agui.WithBasePath(basePathOf(c)),
		agui.WithCancelEnabled(true),
		agui.WithHeartbeatInterval(heartbeatOf(c)),
		agui.WithTimeout(runTimeout),
		// 复用上面带完整选项的 AG-UI runner。硬要求：网关 Bind 的实例
		// 与实际服务请求的实例必须是同一个，恢复路径才能续跑，不要改
		// 成工厂内每次新建。
		// 陷阱声明：本工厂会收到 agui.New 依自身选项（WithAppName/
		// WithSessionService/WithTimeout 等）聚合出的 aguirunner
		// Option 并在此静默丢弃——预构建 runner 的选项必须与传给
		// agui.New 的保持一致才自洽（当前三者一致）；给 agui.New
		// 追加 runner 级选项时必须同步到上面的 aguirunner.New。
		agui.WithRunnerFactory(func(runner.Runner, ...aguirunner.Option) (aguirunner.Runner, error) {
			return aguiRunner, nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("agent: create agui server: %w", err)
	}

	base := basePathOf(c)
	return &Stack{
		handler:  newMux(aguiServer.Handler(), gateway, base),
		runner:   coreRunner,
		basePath: base,
	}, nil
}

// Close 释放骨架持有的框架资源（core runner 及其随建会话存储）。
func (s *Stack) Close() {
	if s.runner != nil {
		_ = s.runner.Close()
	}
}

// Handler 返回外层 ServeMux：AG-UI（chat / cancel，B2 加 /history）与
// ackhttp（confirmation 决定与待确认查询）共用一个前缀空间。子路径
// 的方法分发在 mux 内完成，外层只需前缀整体入 Kratos 链。
func (s *Stack) Handler() nethttp.Handler { return s.handler }

// BasePath 返回归一化后的 AG-UI 路由前缀。挂载方（service 注册路由、
// server 路由级超时的跳过名单）都以它为准，不自行推导。
func (s *Stack) BasePath() string { return s.basePath }

// newMux 构造外层 ServeMux：AG-UI handler 按前缀子树挂载（内部按完整
// 路径匹配，"/agui" 由 ServeMux 301 到 "/agui/"），ackhttp 挂在更深的
// /agui/ack 前缀上（方法级模式更具体，Go ServeMux 最长优先命中）。
func newMux(aguiHandler nethttp.Handler, gateway *ack.Gateway, basePath string) nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.Handle(basePath+"/", aguiHandler)
	ackhttp.Mount(mux, gateway, ackhttp.WithPrefix(basePath+defaultAckSuffix))
	return mux
}

// buildModel 构造模型：优先测试替身，否则按配置走 OpenAI 兼容适配器。
func buildModel(c *conf.Agent, o *stackOptions) (model.Model, error) {
	if o.modelOverride != nil {
		return o.modelOverride, nil
	}
	return newLLMModel(c.GetLlm())
}

// basePathOf 归一化 AG-UI 路由前缀：空值取默认，保证以 / 开头、不以
// / 结尾（挂载点自行拼接子路径）。
func basePathOf(c *conf.Agent) string {
	base := c.GetAgui().GetBasePath()
	if base == "" {
		base = defaultBasePath
	}
	if base[0] != '/' {
		base = "/" + base
	}
	for len(base) > 1 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return base
}

// runTimeoutOf 取单次运行超时（服务端兜底，将来未决确认由它结算）。
func runTimeoutOf(c *conf.Agent) time.Duration {
	if d := c.GetAgui().GetTimeout().AsDuration(); d > 0 {
		return d
	}
	return defaultRunTimeout
}

// heartbeatOf 取 SSE 心跳间隔。
func heartbeatOf(c *conf.Agent) time.Duration {
	if d := c.GetAgui().GetHeartbeatInterval().AsDuration(); d > 0 {
		return d
	}
	return defaultHeartbeat
}

// resolveUserID 是 AG-UI 的 UserIDResolver：从认证 ctx 取身份并编码为
// userKey。身份缺失即拒绝本次运行（fail-closed），绝不回落框架默认
// 的 "user"——那会让跨租户会话混入同一命名空间。
func resolveUserID(ctx context.Context, _ *adapter.RunAgentInput) (string, error) {
	return userKeyFromContext(ctx)
}

// userKeyFromContext 把认证身份编码为框架会话存储键（单一代码点，
// ack WithUserFunc 共用）。
func userKeyFromContext(ctx context.Context) (string, error) {
	id, ok := biz.IdentityFromContext(ctx)
	if !ok {
		return "", errMissingIdentity
	}
	return EncodeUserKey(id.TenantID, id.UserID)
}

// ptr 是标量取指针的小工具（GenerationConfig 字段皆为指针形态）。
func ptr[T any](v T) *T { return &v }
