package agent

import (
	"context"

	"github.com/jninng/trpc_agent_go_ack"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// noopGrants 是许可源的 no-op 空实现：查询永不命中、保存恒成功。
// ack 的 Remember（会话级/全局级许可）在 B1 骨架阶段被静默忽略，
// C2 落地 agent_tool_grant 表后替换为存储实现。
type noopGrants struct{}

// Lookup 实现 ack.GrantSource：永不命中。
func (noopGrants) Lookup(context.Context, ack.GrantQuery) (ack.Grant, bool) {
	return ack.Grant{}, false
}

// Save 实现 ack.GrantSource：恒成功（骨架阶段无持久化目标）。
func (noopGrants) Save(context.Context, ack.Grant) error { return nil }

// noopPolicy 是外部策略源的 no-op 空实现：恒弃权（abstain），不介入
// 裁决。策略源故障的 fail-safe 语义由 ack 评估侧保证；C2/C3 接入真实
// 策略后替换。
type noopPolicy struct{}

// Consult 实现 ack.PolicySource：恒弃权。
func (noopPolicy) Consult(context.Context, ack.ToolCall) (ack.Verdict, error) {
	return ack.VerdictAbstain, nil
}

// newGateway 构造 ack 许可网关骨架。app 名是会话存储键的组成部分，
// 必须与 AG-UI runner 一致；身份经 WithUserFunc 从认证 ctx 取与
// AG-UI UserIDResolver 相同的 userKey 编码。分级表在 B1 不设置
// （未分级工具默认放行，echo 只读无风险），C1 按业务工具面填充。
func newGateway(sessions session.Service, appName string, userKey func(ctx context.Context) (string, error)) *ack.Gateway {
	return ack.New(sessions,
		ack.WithApp(appName),
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
		ack.WithGrants(noopGrants{}),
		ack.WithPolicies(noopPolicy{}),
	)
}
