package agent

import (
	"context"
	"log/slog"

	"github.com/jninng/trpc_agent_go_ack"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// noopGrants 是许可源的 no-op 空实现：查询永不命中、保存恒成功。
// ack 的 Remember（会话级/全局级许可）在 C1 阶段被静默忽略（同会话
// 每次写操作仍会确认），C2 落地 agent_tool_grant 表后替换为存储实现。
type noopGrants struct{}

// Lookup 实现 ack.GrantSource：永不命中。
func (noopGrants) Lookup(context.Context, ack.GrantQuery) (ack.Grant, bool) {
	return ack.Grant{}, false
}

// Save 实现 ack.GrantSource：恒成功（C1 阶段无持久化目标）。
func (noopGrants) Save(context.Context, ack.Grant) error { return nil }

// noopPolicy 是外部策略源的 no-op 空实现：恒弃权（abstain），不介入
// 裁决。策略源故障的 fail-safe 语义由 ack 评估侧保证（出错按 confirm
// 升级处理）；C2/C3 接入真实策略后替换。
type noopPolicy struct{}

// Consult 实现 ack.PolicySource：恒弃权。
func (noopPolicy) Consult(context.Context, ack.ToolCall) (ack.Verdict, error) {
	return ack.VerdictAbstain, nil
}

// toolTiers 是工具分级静态表（TODO C1）：echo 与只读业务工具放行，
// 写工具需人工确认。未列入表中的工具走 ack 默认层级 TierAllow。
// 先静态声明，conf 化（分级可配置）留 C2/E3。
func toolTiers() map[string]ack.Tier {
	return map[string]ack.Tier{
		echoToolName:             ack.TierAllow,
		getCurrentUserToolName:   ack.TierAllow,
		findUsersByNameToolName:  ack.TierAllow,
		updateMyNicknameToolName: ack.TierConfirm,
	}
}

// newGateway 构造 ack 许可网关。app 名是会话存储键的组成部分，
// 必须与 AG-UI runner 一致；身份经 WithUserFunc 从认证 ctx 取与
// AG-UI UserIDResolver 相同的 userKey 编码；框架日志落 grape logger
// （trace_id 桥接归 E1）。WithStaticTools 把工具集登记进网关：恢复
// 路径（重启后孤儿确认）代执行时按工具名从这里解析，缺省则同意的
// 孤儿确认降级为"工具不可用"拒绝——工具集与 LLMAgent 注册的保持
// 同一份。许可源与策略源维持 no-op（C2 填充）。
func newGateway(sessions session.Service, appName string, logger *slog.Logger, userKey func(ctx context.Context) (string, error), tools []tool.Tool) *ack.Gateway {
	return ack.New(sessions,
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
		ack.WithStaticTools(tools...),
		ack.WithGrants(noopGrants{}),
		ack.WithPolicies(noopPolicy{}),
	)
}
