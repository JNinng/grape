package httpapi

// ack 决定路由审计拦截器的回退边界单测：Pending 预读不可得时，已归集
// 的恢复路径代执行行以单边批量落库（不连坐丢失），无归集时零写入（不
// 伪造确认行）。决定路由的完整审计接线（live/recovery 两路径经真实网关
// 结算）由根包 audit_wire_test.go 覆盖。

import (
	"context"
	"log/slog"
	"testing"

	"grape/internal/agent/audit"
	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"

	"github.com/jninng/trpc_agent_go_ack"
)

// TestAuditGuard_when_pending_preread_fails_captured_tool_row_written_alone
// 验证预读失败的回退单边写：Pending 预读不可得（payload nil）但捕获器
// 已归集恢复路径代执行行时，工具行以单边批量落库（不因预读失败连坐
// 丢失），确认行不伪造。
func TestAuditGuard_when_pending_preread_fails_captured_tool_row_written_alone(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	g := &AckAuditGuard{hook: rec, logger: slog.New(slog.DiscardHandler)}
	capture := audit.NewRecoveryCapture("call-recover-orphan", "thread-recover-orphan")
	capture.Add(&biz.ToolCallRecordInput{
		SessionID:  "thread-recover-orphan",
		ToolCallID: "call-recover-orphan",
		ToolName:   tools.UpdateMyNicknameToolName,
		Status:     biz.ToolCallStatusSuccess,
	})
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: testsupport.Tenant, UserID: testsupport.OwnerID})
	decided := &ackDecisionBody{ThreadID: "thread-recover-orphan", ConfirmationID: "call-recover-orphan", Approved: true}

	g.record(ctx, decided, nil, ack.PathRecovery, capture)

	_, confs, batches := rec.Snapshot()
	if len(confs) != 0 {
		t.Fatalf("confirmation rows = %d, want 0 (facts unavailable)", len(confs))
	}
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1 (single-side tool row)", len(batches))
	}
	if batches[0].Tool == nil || batches[0].Conf != nil {
		t.Fatalf("batch = %+v, want tool row only", batches[0])
	}
	if batches[0].Tool.ToolCallID != "call-recover-orphan" || batches[0].Tool.SessionID != "thread-recover-orphan" {
		t.Fatalf("batch tool row = %+v, want orphan proxy execution", batches[0].Tool)
	}
}

// TestAuditGuard_when_pending_unavailable_without_capture_skips_audit 验证
// 回退的边界：预读不可得且捕获器无代执行行（live 路径或纯降级拒绝）时，
// 无任何写入——既不丢行也不伪造行。
func TestAuditGuard_when_pending_unavailable_without_capture_skips_audit(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	g := &AckAuditGuard{hook: rec, logger: slog.New(slog.DiscardHandler)}
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: testsupport.Tenant, UserID: testsupport.OwnerID})
	decided := &ackDecisionBody{ThreadID: "thread-live", ConfirmationID: "conf-live", Approved: false}

	g.record(ctx, decided, nil, ack.PathLive, nil)

	toolRows, confs, batches := rec.Snapshot()
	if len(toolRows)+len(confs)+len(batches) != 0 {
		t.Fatalf("unavailable payload without capture must not record, got %d/%d/%d", len(toolRows), len(confs), len(batches))
	}
}
