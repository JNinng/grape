package audit

// 工具执行终态 wrapper 与写入纪律的单元面：无调用 ID 跳过、失败终态、
// panic 不传播、恢复捕获器归集、短超时预算与观测计数；装配级接线由根包
// audit_wire_test.go 覆盖。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// stubInput / stubOutput 是失败与 panic 内部工具的桩形状（与 echo 同形，
// 声明名沿用 echo 以对齐审计行断言）。
type stubInput struct {
	Text string `json:"text"`
}

type stubOutput struct {
	Echo string `json:"echo"`
}

// newAuditEchoTool 构造直调 wrapper 单测用的 echo 工具清单。
func newAuditEchoTool() []tool.Tool {
	return []tool.Tool{tools.NewEchoTool()}
}

// TestAuditTool_when_context_has_no_call_id_skips_record 验证无调用 ID
// 的执行不落审计行（biz 契约 ToolCallID 必填，缺 ID 只记日志跳过），
// 工具结果本身不受影响。
func TestAuditTool_when_context_has_no_call_id_skips_record(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	logger := slog.New(slog.DiscardHandler)
	wrapped := AuditedTools(newAuditEchoTool(), rec, nil, logger)
	ct, ok := wrapped[0].(tool.CallableTool)
	if !ok {
		t.Fatalf("wrapped tool must stay callable")
	}
	if got := ct.Declaration().Name; got != tools.EchoToolName {
		t.Fatalf("declaration passthrough = %s, want %s", got, tools.EchoToolName)
	}
	res, err := ct.Call(context.Background(), []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res == nil {
		t.Fatalf("call result must pass through")
	}
	toolRows, confs, batches := rec.Snapshot()
	if len(toolRows) != 0 || len(confs) != 0 || len(batches) != 0 {
		t.Fatalf("call without tool call id must not record, got %d/%d/%d", len(toolRows), len(confs), len(batches))
	}
}

// TestAuditTool_when_inner_tool_fails_records_error_status 验证失败终态：
// 内部工具报错时审计行记 error 状态，结果摘要为错误文本，错误本身原样
// 透传给调用方。
func TestAuditTool_when_inner_tool_fails_records_error_status(t *testing.T) {
	inner := function.NewFunctionTool(
		func(_ context.Context, in stubInput) (stubOutput, error) {
			if strings.TrimSpace(in.Text) == "" {
				return stubOutput{}, fmt.Errorf("text is required")
			}
			return stubOutput{Echo: in.Text}, nil
		},
		function.WithName(tools.EchoToolName),
		function.WithDescription("echo"),
	)
	rec := testsupport.NewAuditRecorder()
	wrapped := AuditedTools([]tool.Tool{inner}, rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-err-1")
	if _, err := ct.Call(ctx, []byte(`{"text":""}`)); err == nil {
		t.Fatalf("inner error must pass through")
	}
	toolRows, _, _ := rec.Snapshot()
	if len(toolRows) != 1 {
		t.Fatalf("tool audit rows = %d, want 1", len(toolRows))
	}
	if toolRows[0].Status != biz.ToolCallStatusError {
		t.Fatalf("tool row status = %s, want error", toolRows[0].Status)
	}
	if toolRows[0].ResultSummary != "text is required" {
		t.Fatalf("tool row result summary = %q, want error text", toolRows[0].ResultSummary)
	}
	if toolRows[0].ToolCallID != "call-err-1" {
		t.Fatalf("tool row id = %q, want ctx-injected call id", toolRows[0].ToolCallID)
	}
}

// TestAuditTool_when_hook_panics_error_does_not_propagate 验证挂点 panic
// 不向工具调用方传播：wrapper 直写路径的 hook panic 被吸收，工具照常
// 返回结果。
func TestAuditTool_when_hook_panics_error_does_not_propagate(t *testing.T) {
	rec := &testsupport.AuditRecorder{PanicOn: "tool"}
	wrapped := AuditedTools(newAuditEchoTool(), rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-panic-1")
	res, err := ct.Call(ctx, []byte(`{"text":"hi"}`))
	if err != nil || res == nil {
		t.Fatalf("tool call must succeed despite hook panic, got res=%v err=%v", res, err)
	}
}

// TestAuditTool_when_capture_target_matches_records_into_capture 验证
// 捕获器归集：ctx 携带目标一致的捕获器时代执行行只归集不直写，会话 ID
// 由捕获器补齐；目标不一致（续跑运行的新调用）仍走直写。
func TestAuditTool_when_capture_target_matches_records_into_capture(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	wrapped := AuditedTools(newAuditEchoTool(), rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)

	capture := NewRecoveryCapture("call-recovery-1", "thread-recovery")
	ctx := ContextWithCapture(context.Background(), capture)
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolRows, _, _ := rec.Snapshot()
	if len(toolRows) != 0 {
		t.Fatalf("captured execution must not direct-write, got %d rows", len(toolRows))
	}
	taken := capture.Take()
	if taken == nil {
		t.Fatalf("capture must hold the proxy execution row")
	}
	if taken.SessionID != "thread-recovery" || taken.ToolCallID != "call-recovery-1" {
		t.Fatalf("captured row = %s/%s, want thread-recovery/call-recovery-1", taken.SessionID, taken.ToolCallID)
	}

	// 目标不一致：ctx 仍是捕获器所在链，但框架注入了新调用 ID。
	ctx2 := context.WithValue(context.Background(), captureKeyType{}, capture)
	ctx2 = context.WithValue(ctx2, tool.ContextKeyToolCallID{}, "call-resumed-1")
	if _, err := ct.Call(ctx2, []byte(`{"text":"yo"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolRows, _, _ = rec.Snapshot()
	if len(toolRows) != 1 || toolRows[0].ToolCallID != "call-resumed-1" {
		t.Fatalf("mismatched capture target must direct-write, got %+v", toolRows)
	}
}

// TestAuditTool_when_inner_tool_panics_records_error_row_and_repanics 验证
// 工具自身 panic 的审计面：defer recover 先落一条 status=error 审计行
// （结果摘要含 panic 值），再原样 re-panic——框架对 panic 的处理语义
// 不变，工具行不再因 panic 被执行边界吞掉而缺失。
func TestAuditTool_when_inner_tool_panics_records_error_row_and_repanics(t *testing.T) {
	inner := function.NewFunctionTool(
		func(context.Context, stubInput) (stubOutput, error) {
			panic("boom in tool")
		},
		function.WithName(tools.EchoToolName),
		function.WithDescription("echo"),
	)
	rec := testsupport.NewAuditRecorder()
	wrapped := AuditedTools([]tool.Tool{inner}, rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-panic-tool-1")

	panicked := func() (r any) {
		defer func() { r = recover() }()
		_, _ = ct.Call(ctx, []byte(`{"text":"hi"}`))
		return
	}()
	if panicked == nil {
		t.Fatal("tool panic must propagate unchanged")
	}
	toolRows, confs, batches := rec.Snapshot()
	if len(toolRows) != 1 || len(confs) != 0 || len(batches) != 0 {
		t.Fatalf("panic audit rows = %d/%d/%d, want 1/0/0", len(toolRows), len(confs), len(batches))
	}
	row := toolRows[0]
	if row.ToolCallID != "call-panic-tool-1" || row.ToolName != tools.EchoToolName {
		t.Fatalf("panic row id/name = %s/%s, want call-panic-tool-1/%s", row.ToolCallID, row.ToolName, tools.EchoToolName)
	}
	if row.Status != biz.ToolCallStatusError {
		t.Fatalf("panic row status = %s, want error", row.Status)
	}
	if !strings.Contains(row.ResultSummary, "tool panicked") || !strings.Contains(row.ResultSummary, "boom in tool") {
		t.Fatalf("panic row result summary = %q, want panic value", row.ResultSummary)
	}
}

// TestWriteAudit_when_hook_blocks_write_abandoned_after_budget 验证审计
// 写入的短超时预算：挂点悬挂时写入在预算内放弃并返回，调用方不阻塞
// （审计旁路面不得反噬业务主流程）。
func TestWriteAudit_when_hook_blocks_write_abandoned_after_budget(t *testing.T) {
	prev := auditWriteTimeout
	auditWriteTimeout = 50 * time.Millisecond
	t.Cleanup(func() { auditWriteTimeout = prev })

	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		WriteAudit(context.Background(), slog.New(slog.DiscardHandler), "blocked write",
			func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("write never started")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked write must be abandoned after the budget")
	}
}

// TestAuditTool_when_observer_present_counts_terminal_states 验证仅观测
// 形态（hook 缺省、observer 非 nil）：成功/失败/panic 三种终态都推送
// 工具计数（panic 记 error），无审计行写入；观测先于捕获器归集分支，
// 恢复路径代执行同样被计数。
func TestAuditTool_when_observer_present_counts_terminal_states(t *testing.T) {
	obs := testsupport.NewFakeObserver()
	wrapped := AuditedTools(newAuditEchoTool(), nil, obs, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)

	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-obs-1")
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := obs.ToolCount(tools.EchoToolName, biz.ToolCallStatusSuccess); got != 1 {
		t.Fatalf("echo success count = %d, want 1", got)
	}

	// panic 终态同样计数（error），panic 本身照常传播。
	boom := function.NewFunctionTool(
		func(context.Context, stubInput) (stubOutput, error) {
			panic("boom")
		},
		function.WithName(tools.EchoToolName),
		function.WithDescription("echo"),
	)
	boomWrapped := AuditedTools([]tool.Tool{boom}, nil, obs, slog.New(slog.DiscardHandler))[0].(tool.CallableTool)
	func() {
		defer func() { _ = recover() }()
		_, _ = boomWrapped.Call(ctx, []byte(`{"text":"hi"}`))
	}()
	if got := obs.ToolCount(tools.EchoToolName, biz.ToolCallStatusError); got != 1 {
		t.Fatalf("echo error count (panic terminal) = %d, want 1", got)
	}
}

// TestAuditTool_when_hook_and_observer_both_present_both_fired 验证组合
// 形态：审计挂点与观测契约同时注入时，单一包装点两路都生效——审计行
// 照写、观测计数照推。
func TestAuditTool_when_hook_and_observer_both_present_both_fired(t *testing.T) {
	rec := testsupport.NewAuditRecorder()
	obs := testsupport.NewFakeObserver()
	wrapped := AuditedTools(newAuditEchoTool(), rec, obs, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-both-1")
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolRows, _, _ := rec.Snapshot()
	if len(toolRows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(toolRows))
	}
	if got := obs.ToolCount(tools.EchoToolName, biz.ToolCallStatusSuccess); got != 1 {
		t.Fatalf("observed count = %d, want 1", got)
	}
}
