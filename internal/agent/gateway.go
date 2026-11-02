package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jninng/trpc_agent_go_ack"
	"grape/internal/agent/tools"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// noopPolicy 是外部策略源的 no-op 空实现：恒弃权（abstain），不介入
// 裁决——与 ack 对 abstain 的既有语义一致（评估管线里 abstain 不改变
// 处置，只在日志中留痕）。这是显式决策而非占位缺陷：当前没有
// 真实策略需求，接入策略面时以实现 ack.PolicySource 的类型替换本
// 类型（接入点即 newGateway 中 ack.WithPolicies 的实参）；需要让生效
// 依据进入授权终态事件与审计行时改挂 ack.NamedPolicy——名字即事件
// Source、落审计为来源命名空间决定词形（{name}_auto / {name}_deny），
// 因此名字必须满足 biz 的来源命名空间约束（小写字母开头、仅
// [a-z0-9_]、≠ auto、加后缀总长 ≤16），不合规名字的授权行会在转写器
// fail-closed 丢弃（见 httpapi 包? no）。策略源故障的 fail-safe
// 语义由 ack 评估侧保证（出错按 confirm 升级处理）。
type noopPolicy struct{}

// Consult 实现 ack.PolicySource：恒弃权。
func (noopPolicy) Consult(context.Context, ack.ToolCall) (ack.Verdict, error) {
	return ack.VerdictAbstain, nil
}

// toolTiers 是工具分级静态表：echo 与只读业务工具放行，
// 写工具需人工确认。未列入表中的工具走 ack 默认层级 TierAllow。
// 先静态声明，conf 化（分级可配置）暂缓。
func toolTiers() map[string]ack.Tier {
	return map[string]ack.Tier{
		tools.EchoToolName:             ack.TierAllow,
		tools.GetCurrentUserToolName:   ack.TierAllow,
		tools.FindUsersByNameToolName:  ack.TierAllow,
		tools.UpdateMyNicknameToolName: ack.TierConfirm,
	}
}

// denyTexts 声明五类拒绝的模型面文案。
func denyTexts() ack.DenyTexts {
	return ack.DenyTexts{
		// 用户在确认提示中拒绝了工具 %s 的执行。
		HumanDenied: func(call ack.ToolCall) string {
			return fmt.Sprintf("User denied the execution of tool %s in the confirmation prompt.", call.ToolName)
		},
		// 工具 %s 被权限层级或策略禁用。请勿再次调用该工具。
		TierDisabled: func(toolName string) string {
			return fmt.Sprintf("Tool %s is disabled by the permission tier or policy. Do not call this tool again.", toolName)
		},
		// 工具执行前运行已被取消或超时。
		Canceled: "The run was canceled or timed out before the tool executed.",
		// 待确认记录无法落库；工具未执行。
		PersistFailed: "The pending confirmation could not be recorded; the tool was not executed.",
		// 工具 %s 不可用（%s）；未执行。请勿再次调用该工具。
		Unavailable: func(toolName, detail string) string {
			return fmt.Sprintf("Tool %s is unavailable (%s); it was not executed. Do not call this tool again.", toolName, detail)
		},
	}
}

// newGateway 构造 ack 许可网关。app 名是会话存储键的组成部分，
// 必须与 AG-UI runner 一致；身份经 WithUserFunc 从认证 ctx 取与
// AG-UI UserIDResolver 相同的 userKey 编码；grape logger 经 WithLogger
// 注入（ack 日志 API 无 ctx 参数，故不带 trace_id——trpc-agent-go 全框
// 架日志面的桥接与 trace_id 注入归 internal/telemetry，见其 README 的
// 桥接局限声明）。WithStaticTools 把工具集登记进网关：恢复路径（重启
// 后孤儿确认）代执行时按工具名从这里解析，缺省则同意的孤儿确认降级
// 为"工具不可用"拒绝——工具集与 LLMAgent 注册的保持同一份。分级表
// 见 toolTiers（读 allow/写 confirm）；许可源是 grants 非 nil 时的存储
// 实现（agent_tool_grant 表，见 grants.go 的 storedGrants），nil
// 时维持无许可源形态（评估永不命中、remember 被 ack 忽略并记日志）；拒绝
// 文案经 WithDenyTexts 显式声明（见 denyTexts）；策略
// 源恒为弃权空实现（接入点，见 noopPolicy）；observer 非 nil 时挂授权
// 终态观察挂点——自动授权终态转写为授权确认审计行（见
// httpapi 包? no），人工决定仍由决定路由审计面落行，两路互斥。
func newGateway(sessions session.Service, appName string, logger *slog.Logger, userKey func(ctx context.Context) (string, error), tools []tool.Tool, grants ack.GrantSource, observer ack.PermissionObserver) *ack.Gateway {
	opts := []ack.Option{
		ack.WithApp(appName),
		ack.WithLogger(logger),
		ack.WithUserFunc(func(ctx context.Context, _ string) string {
			// ack 的 WithUserFunc 只返回 string：身份缺失时返回空串，
			// 会话定位与许可查询自然落空——fail-closed 的第二道防线
			//（第一道是外层认证中间件的 401）。
			key, err := userKey(ctx)
			if err != nil {
				return ""
			}
			return key
		}),
		ack.WithTiers(toolTiers()),
		ack.WithDenyTexts(denyTexts()),
		ack.WithStaticTools(tools...),
		ack.WithPolicies(noopPolicy{}),
	}
	if grants != nil {
		opts = append(opts, ack.WithGrants(grants))
	}
	if observer != nil {
		opts = append(opts, ack.WithPermissionObserver(observer))
	}
	return ack.New(sessions, opts...)
}
