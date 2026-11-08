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

// noopPolicy 是外部策略源的弃权空实现：恒返回 abstain，不介入裁决。
// 接入真实策略面时以实现 ack.PolicySource 的类型替换本类型；需要让生效
// 依据进入事件与审计行时改挂 ack.NamedPolicy，名字须满足 biz 的来源
// 命名空间约束（见 README「迁移暂存」），不合规名字的授权行会被转写器
// fail-closed 丢弃。
type noopPolicy struct{}

// Consult 实现 ack.PolicySource：恒弃权。
func (noopPolicy) Consult(context.Context, ack.ToolCall) (ack.Verdict, error) {
	return ack.VerdictAbstain, nil
}

// toolTiers 是工具分级静态表：echo 与只读业务工具放行，
// 写工具需人工确认。未列入表中的工具走 ack 默认层级 TierAllow。
// 分级当前静态声明，未做 conf 化配置。
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

// newGateway 构造 ack 许可网关：app 名与 AG-UI runner 一致；身份经
// WithUserFunc 取 userKey；工具集登记支撑恢复路径按名代执行；分级表见
// toolTiers、拒绝文案见 denyTexts、策略源恒弃权；grants 与 observer 非
// nil 时分别接许可存储与授权终态观察。约束细节见 README「关键约束」。
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
