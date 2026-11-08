package audit

// 授权终态观察转写器的契约测试：事件 → 授权确认行的映射（allow/deny、
// 来源命名空间、属主 userKey、path 恒 auto、confirmation_id 即
// tool_call_id），以及缺 ID/身份、来源名非法、写入失败与 panic 的旁路
// 纪律。

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/jninng/trpc_agent_go_ack"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"
)

// permissionEvent 构造观察事件的快捷方式。
func permissionEvent(toolCallID, toolName, source string, allowed bool) ack.PermissionEvent {
	return ack.PermissionEvent{
		Call: ack.ToolCall{
			SessionID:  "thread-permission-audit",
			ToolCallID: toolCallID,
			ToolName:   toolName,
		},
		Allowed: allowed,
		Basis:   ack.BasisTier,
		Source:  source,
	}
}

// observerIdentityContext 注入运行属主身份（转写器的 decided_by 来源）。
func observerIdentityContext() context.Context {
	return biz.WithIdentity(context.Background(), biz.Identity{TenantID: testsupport.Tenant, UserID: testsupport.OwnerID})
}

// TestPermissionAudit_when_allowed_maps_to_auto_row 验证放行事件的无来源
// 词形：decision=auto、path 恒 auto、confirmation_id 即 tool_call_id、
// decided_by 取认证 ctx 的属主 userKey、remember 恒空。
func TestPermissionAudit_when_allowed_maps_to_auto_row(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(observerIdentityContext(), permissionEvent("call-allow-1", tools.EchoToolName, "", true))

	_, confs, batches := rec.Snapshot()
	if len(confs) != 1 || len(batches) != 0 {
		t.Fatalf("rows = %d confirmations / %d batches, want 1/0", len(confs), len(batches))
	}
	cf := confs[0]
	if cf.Decision != biz.ConfirmationDecisionAuto || cf.Path != biz.ConfirmationPathAuto {
		t.Fatalf("decision/path = %s/%s, want auto/auto", cf.Decision, cf.Path)
	}
	if cf.ConfirmationID != "call-allow-1" || cf.ToolCallID != "call-allow-1" {
		t.Fatalf("ids = %s/%s, want confirmation id to be the tool call id", cf.ConfirmationID, cf.ToolCallID)
	}
	if cf.ToolName != tools.EchoToolName || cf.SessionID != "thread-permission-audit" {
		t.Fatalf("tool/session = %s/%q, want %s/thread-permission-audit", cf.ToolName, cf.SessionID, tools.EchoToolName)
	}
	if cf.Remember != "" {
		t.Fatalf("system decision must not remember, got %q", cf.Remember)
	}
	if want := testsupport.OwnerUserKey(t); cf.DecidedBy != want {
		t.Fatalf("decidedBy = %q, want owner userKey %q", cf.DecidedBy, want)
	}
}

// TestPermissionAudit_when_denied_maps_to_auto_deny_row 验证拒绝事件的
// 无来源词形：decision=auto_deny、其余口径同放行。
func TestPermissionAudit_when_denied_maps_to_auto_deny_row(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(observerIdentityContext(), permissionEvent("call-deny-1", tools.EchoToolName, "", false))

	_, confs, _ := rec.Snapshot()
	if len(confs) != 1 {
		t.Fatalf("confirmation rows = %d, want 1", len(confs))
	}
	if confs[0].Decision != biz.ConfirmationDecisionAutoDeny || confs[0].Path != biz.ConfirmationPathAuto {
		t.Fatalf("decision/path = %s/%s, want auto_deny/auto", confs[0].Decision, confs[0].Path)
	}
}

// TestPermissionAudit_when_named_source_maps_to_namespace_decisions 验证
// 来源命名空间：有 Source 的放行/拒绝分别落 {source}_auto / {source}_deny。
func TestPermissionAudit_when_named_source_maps_to_namespace_decisions(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(observerIdentityContext(), permissionEvent("call-risk-allow", tools.EchoToolName, "risk", true))
	ob(observerIdentityContext(), permissionEvent("call-risk-deny", tools.EchoToolName, "risk", false))

	_, confs, _ := rec.Snapshot()
	if len(confs) != 2 {
		t.Fatalf("confirmation rows = %d, want 2", len(confs))
	}
	if confs[0].Decision != "risk_auto" {
		t.Fatalf("named allow decision = %s, want risk_auto", confs[0].Decision)
	}
	if confs[1].Decision != "risk_deny" {
		t.Fatalf("named deny decision = %s, want risk_deny", confs[1].Decision)
	}
	for _, cf := range confs {
		if cf.Path != biz.ConfirmationPathAuto {
			t.Fatalf("named decision path = %s, want auto", cf.Path)
		}
	}
}

// TestPermissionAudit_when_event_has_no_call_id_skips_row 验证无调用 ID
// 的事件只记日志放弃：confirmation_id 即 tool_call_id，缺失即无法构造
// 合法授权行，不得伪造。
func TestPermissionAudit_when_event_has_no_call_id_skips_row(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(observerIdentityContext(), permissionEvent("", tools.EchoToolName, "", true))

	_, confs, _ := rec.Snapshot()
	if len(confs) != 0 {
		t.Fatalf("rows = %d, want 0 (no call id)", len(confs))
	}
}

// TestPermissionAudit_when_identity_missing_skips_row 验证身份缺失只记
// 日志放弃：decided_by 必填，无属主身份不得伪造决定人。
func TestPermissionAudit_when_identity_missing_skips_row(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(context.Background(), permissionEvent("call-noauth-1", tools.EchoToolName, "", true))

	_, confs, _ := rec.Snapshot()
	if len(confs) != 0 {
		t.Fatalf("rows = %d, want 0 (missing identity)", len(confs))
	}
}

// TestPermissionAudit_when_source_name_invalid_skips_row 验证来源名非法
// 的 fail-closed 丢弃：决定词表无法承载不合规来源名（破坏词形唯一性），
// 转写器放弃该行而非静默改写。
func TestPermissionAudit_when_source_name_invalid_skips_row(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	for _, source := range []string{"Risk", "1risk", "au-to", "auto"} {
		ob(observerIdentityContext(), permissionEvent("call-bad-source", tools.EchoToolName, source, true))
	}

	_, confs, _ := rec.Snapshot()
	if len(confs) != 0 {
		t.Fatalf("rows = %d, want 0 (invalid source names)", len(confs))
	}
}

// TestPermissionAudit_when_write_fails_does_not_propagate 验证审计旁路
// 纪律：挂点写入返回错误、panic、来源行组合批量 panic 都不向观察回调
// （进而向授权主流程）传播；正向对照两次写入确实到达挂点——失败被吞
// 而非被跳过（若转写器提前返回根本不调 hook，本用例即变红）。
func TestPermissionAudit_when_write_fails_does_not_propagate(t *testing.T) {
	rec := &testsupport.AuditRecorder{Err: errors.New("audit storage down")}
	ob := NewPermissionAuditor(rec, slog.New(slog.DiscardHandler))

	ob(observerIdentityContext(), permissionEvent("call-fail-1", tools.EchoToolName, "", true))
	rec.SetFailure(nil, "confirmation")
	ob(observerIdentityContext(), permissionEvent("call-panic-1", tools.EchoToolName, "", true))

	if got := rec.Attempts(); got != 2 {
		t.Fatalf("write attempts = %d, want 2 (failures absorbed, not skipped)", got)
	}
}
