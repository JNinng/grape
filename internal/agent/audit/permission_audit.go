package audit

import (
	"context"
	"log/slog"

	"github.com/jninng/trpc_agent_go_ack"

	"grape/internal/agent/identity"
	"grape/internal/biz"
)

// 授权终态观察转写器：把 ack 网关的自动授权终态转写为授权确认审计行，
// 覆盖评估管线内自动终结的全部来源；人工决定仍由决定路由拦截器落行，
// 两类写入点互斥且每次工具调用恰好一条授权行。

// NewPermissionAuditor 构造授权终态观察回调。hook 是审计写入挂点，与
// 工具执行终态、决定路由共用同一实例；装配形态见根包 stack.go（audit 非
// nil 时随 ack 网关注入 ack.WithPermissionObserver）。
func NewPermissionAuditor(hook Hook, logger *slog.Logger) ack.PermissionObserver {
	return func(ctx context.Context, event ack.PermissionEvent) {
		// 决定映射：放行 auto、拒绝 auto_deny；带来源为来源命名空间
		// {source}_auto / {source}_deny，path 恒 auto（系统自动决定）。
		decision, ok := biz.SystemDecision(event.Source, event.Allowed)
		if !ok {
			// 来源名破坏决定词表唯一性（具名策略源的名字不合法）：
			// fail-closed 放弃该行，不静默改写——接入具名策略源的名字
			// 约束见根包 gateway.go 的 noopPolicy。
			logger.Warn("invalid permission source name, skip permission audit",
				"tool", event.Call.ToolName, "source", event.Source)
			return
		}
		if event.Call.ToolCallID == "" {
			// 无调用 ID 无法构造合法授权行（confirmation_id 即
			// tool_call_id），记日志放弃。评估管线对无 ID 调用按防御性
			// 放行处理、不进观察契约，此处是纵深防御。
			logger.Warn("permission event without tool call id, skip permission audit",
				"tool", event.Call.ToolName, "sessionId", event.Call.SessionID)
			return
		}
		decidedBy, err := identity.UserKeyFromContext(ctx)
		if err != nil {
			// 评估链正常时不可达（运行入口已 fail-closed）；无属主身份
			// 无法构造合法授权行，记日志放弃。
			logger.Warn("missing identity for permission audit, skip permission audit",
				"tool", event.Call.ToolName, "toolCallId", event.Call.ToolCallID, "error", err)
			return
		}
		WriteAudit(ctx, logger, "record permission decision", func(wctx context.Context) error {
			return hook.RecordConfirmation(wctx, &biz.ConfirmationRecordInput{
				// confirmation_id 即 tool_call_id：ack 对自动终态不发确认
				// ID，审计行以工具调用 ID 为对账锚点（与人工决定行同源）。
				ConfirmationID: event.Call.ToolCallID,
				SessionID:      event.Call.SessionID,
				ToolCallID:     event.Call.ToolCallID,
				ToolName:       event.Call.ToolName,
				Decision:       decision,
				Remember:       "",
				Path:           biz.ConfirmationPathAuto,
				DecidedBy:      decidedBy,
			})
		})
	}
}
