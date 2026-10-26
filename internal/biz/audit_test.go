package biz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"grape/pkg/redact"
	"go.opentelemetry.io/otel/trace"
)

// auditFakeRepo 捕获用例转发的审计行，供断言构造与脱敏结果。
type auditFakeRepo struct {
	toolCalls     []*ToolCallAudit
	confirmations []*ConfirmationAudit
	batchTools    []*ToolCallAudit
	batchConfirms []*ConfirmationAudit
}

func (r *auditFakeRepo) AppendToolCall(_ context.Context, rec *ToolCallAudit) error {
	r.toolCalls = append(r.toolCalls, rec)
	return nil
}

func (r *auditFakeRepo) AppendConfirmation(_ context.Context, rec *ConfirmationAudit) error {
	r.confirmations = append(r.confirmations, rec)
	return nil
}

func (r *auditFakeRepo) AppendBatch(_ context.Context, tool *ToolCallAudit, confirmation *ConfirmationAudit) error {
	r.batchTools = append(r.batchTools, tool)
	r.batchConfirms = append(r.batchConfirms, confirmation)
	return nil
}

func (r *auditFakeRepo) ListToolCalls(context.Context, *AuditFilter) (*ToolCallAuditList, error) {
	return &ToolCallAuditList{Items: nil, Total: 0}, nil
}

func (r *auditFakeRepo) ListConfirmations(context.Context, *AuditFilter) (*ConfirmationAuditList, error) {
	return &ConfirmationAuditList{Items: nil, Total: 0}, nil
}

func (r *auditFakeRepo) GetToolCall(context.Context, string) (*ToolCallAudit, error) {
	return nil, ErrAuditNotFound
}

func (r *auditFakeRepo) GetConfirmation(context.Context, string) (*ConfirmationAudit, error) {
	return nil, ErrAuditNotFound
}

// auditIdentityContext 注入认证身份；traceContext 为真时附带 SpanContext。
func auditIdentityContext(traceContext bool) context.Context {
	ctx := WithIdentity(context.Background(), Identity{TenantID: "t-1", UserID: 7})
	if !traceContext {
		return ctx
	}
	return TraceContextWithSpan(ctx)
}

// auditTestTraceID 是用例的固定链路 ID（合法非零 16 字节）。
var auditTestTraceID = [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

// TraceContextWithSpan 在 ctx 上附加 SpanContext（模拟链路中间件的注入，
// 生产注入点在 internal/middleware）。
func TraceContextWithSpan(ctx context.Context) context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    auditTestTraceID,
		SpanID:     [8]byte{8, 7, 6, 5, 4, 3, 2, 1},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	return trace.ContextWithSpanContext(ctx, sc)
}

func TestAuditUsecase_redacts_registered_categories_before_repo(t *testing.T) {
	uc := NewAuditUsecase(&auditFakeRepo{}, redact.NewStandardRegistry())
	err := uc.RecordToolCall(auditIdentityContext(true), &ToolCallRecordInput{
		SessionID:        "sess-1",
		ToolCallID:       "call-1",
		ToolName:         "create_order",
		ArgumentsSummary: `{"password":"hunter2","query":"grape"}`,
		ResultSummary:    strings.Repeat("result", 200), // 1200 rune，超过 retrieval 的 512 截断
		Status:           ToolCallStatusSuccess,
	})
	if err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	repo, ok := uc.repo.(*auditFakeRepo)
	if !ok || len(repo.toolCalls) != 1 {
		t.Fatalf("repo captured %d tool call rows, want 1", len(repo.toolCalls))
	}
	rec := repo.toolCalls[0]
	// 命中键名脱敏：password 值替换为掩码，未命中键原样保留。
	if strings.Contains(rec.ArgumentsSummary, "hunter2") {
		t.Fatalf("arguments summary leaks sensitive value: %q", rec.ArgumentsSummary)
	}
	if !strings.Contains(rec.ArgumentsSummary, `"query":"grape"`) {
		t.Fatalf("arguments summary lost unfiltered key: %q", rec.ArgumentsSummary)
	}
	// retrieval 类别自带 512 rune 截断：1200 rune 输入压到 512 + 截断标记。
	if got := len([]rune(rec.ResultSummary)); got > 512+20 {
		t.Fatalf("result summary not truncated by retrieval redactor: %d runes", got)
	}
	if !strings.Contains(rec.ResultSummary, "(truncated)") {
		t.Fatalf("result summary lacks truncation marker: %q", rec.ResultSummary)
	}
	// 身份与链路事实由用例从 ctx 补齐，不是入参。
	if rec.UserID != 7 {
		t.Fatalf("user id = %d, want 7 from ctx identity", rec.UserID)
	}
	if rec.TraceID != TraceIDFromContext(auditIdentityContext(true)) || rec.TraceID == "" {
		t.Fatalf("trace id = %q, want ctx span trace id", rec.TraceID)
	}
}

func TestAuditUsecase_unregistered_category_passes_through_with_truncation(t *testing.T) {
	uc := NewAuditUsecase(&auditFakeRepo{}, redact.NewRegistry()) // 空注册表：tool_param/retrieval 未登记
	long := strings.Repeat("文", AuditSummaryMaxRunes + 50)
	err := uc.RecordToolCall(auditIdentityContext(false), &ToolCallRecordInput{
		ToolCallID:       "call-2",
		ToolName:         "echo",
		ArgumentsSummary: "plain args without json shape",
		ResultSummary:    long,
		Status:           ToolCallStatusError,
	})
	if err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	rec := uc.repo.(*auditFakeRepo).toolCalls[0]
	// 未登记类别放行原值（脱敏语义未声明不丢弃审计行），截断兜底仍生效。
	if rec.ArgumentsSummary != "plain args without json shape" {
		t.Fatalf("arguments summary altered without registered category: %q", rec.ArgumentsSummary)
	}
	if got := rec.ResultSummary; len([]rune(got)) != AuditSummaryMaxRunes+len("...(truncated)") {
		t.Fatalf("result summary = %d runes, want capped %d + marker", len([]rune(got)), AuditSummaryMaxRunes)
	}
	if !strings.HasSuffix(rec.ResultSummary, "...(truncated)") {
		t.Fatalf("result summary lacks truncation marker: %q", rec.ResultSummary[len(rec.ResultSummary)-20:])
	}
	if rec.TraceID != "" {
		t.Fatalf("trace id = %q, want empty without span context", rec.TraceID)
	}
}

func TestAuditUsecase_validates_record_inputs(t *testing.T) {
	uc := NewAuditUsecase(&auditFakeRepo{}, redact.NewStandardRegistry())
	ctx := auditIdentityContext(false)
	cases := []struct {
		name string
		call func() error
	}{
		{"tool call missing id", func() error {
			return uc.RecordToolCall(ctx, &ToolCallRecordInput{ToolName: "echo", Status: ToolCallStatusSuccess})
		}},
		{"tool call bad status", func() error {
			return uc.RecordToolCall(ctx, &ToolCallRecordInput{ToolCallID: "c", ToolName: "echo", Status: "pending"})
		}},
		{"tool call negative duration", func() error {
			return uc.RecordToolCall(ctx, &ToolCallRecordInput{ToolCallID: "c", ToolName: "echo", Status: ToolCallStatusSuccess, DurationMS: -1})
		}},
		{"tool call duration overflow", func() error {
			return uc.RecordToolCall(ctx, &ToolCallRecordInput{ToolCallID: "c", ToolName: "echo", Status: ToolCallStatusSuccess, DurationMS: int64(maxInt32) + 1})
		}},
		{"confirmation denied with remember", func() error {
			return uc.RecordConfirmation(ctx, &ConfirmationRecordInput{
				ConfirmationID: "cf-1", SessionID: "s", ToolName: "write", Decision: ConfirmationDecisionDenied,
				Path: ConfirmationPathLive, DecidedBy: "t-1:7", Remember: ConfirmationRememberGlobal,
			})
		}},
		{"confirmation bad path", func() error {
			return uc.RecordConfirmation(ctx, &ConfirmationRecordInput{
				ConfirmationID: "cf-1", SessionID: "s", ToolName: "write", Decision: ConfirmationDecisionApproved,
				Path: "offline", DecidedBy: "t-1:7",
			})
		}},
		{"confirmation missing decider", func() error {
			return uc.RecordConfirmation(ctx, &ConfirmationRecordInput{
				ConfirmationID: "cf-1", SessionID: "s", ToolName: "write", Decision: ConfirmationDecisionApproved,
				Path: ConfirmationPathLive,
			})
		}},
		{"nil batch", func() error {
			return uc.RecordToolCallWithConfirmation(ctx, nil, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrAuditInvalidArgument) {
				t.Fatalf("err = %v, want ErrAuditInvalidArgument", err)
			}
		})
	}
	// 身份缺失 fail-closed：合法入参在无认证 ctx 下一律拒绝。
	err := uc.RecordToolCall(context.Background(), &ToolCallRecordInput{
		ToolCallID: "c", ToolName: "echo", Status: ToolCallStatusSuccess,
	})
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("unauthenticated record = %v, want ErrSessionInvalid", err)
	}
}

func TestAuditUsecase_batch_forwards_both_records(t *testing.T) {
	repo := &auditFakeRepo{}
	uc := NewAuditUsecase(repo, redact.NewStandardRegistry())
	ctx := auditIdentityContext(false)
	err := uc.RecordToolCallWithConfirmation(ctx,
		&ToolCallRecordInput{ToolCallID: "call-b", ToolName: "write", Status: ToolCallStatusSuccess},
		&ConfirmationRecordInput{ConfirmationID: "call-b", SessionID: "sess-b", ToolName: "write",
			Decision: ConfirmationDecisionApproved, Path: ConfirmationPathLive, DecidedBy: "t-1:7"})
	if err != nil {
		t.Fatalf("record batch: %v", err)
	}
	if len(repo.batchTools) != 1 || len(repo.batchConfirms) != 1 {
		t.Fatalf("batch capture = (%d tools, %d confirms), want (1, 1)", len(repo.batchTools), len(repo.batchConfirms))
	}
	if repo.batchTools[0] == nil || repo.batchConfirms[0] == nil {
		t.Fatal("batch rows must both be built")
	}
	// 半组合（仅确认）同样合法：批量写入允许单边为空。
	repo2 := &auditFakeRepo{}
	uc2 := NewAuditUsecase(repo2, redact.NewStandardRegistry())
	if err := uc2.RecordToolCallWithConfirmation(ctx, nil, &ConfirmationRecordInput{
		ConfirmationID: "cf-2", SessionID: "s", ToolName: "write", Decision: ConfirmationDecisionDenied,
		Path: ConfirmationPathRecovery, DecidedBy: "t-1:7",
	}); err != nil {
		t.Fatalf("record confirmation-only batch: %v", err)
	}
	if len(repo2.batchTools) != 1 || repo2.batchTools[0] != nil {
		t.Fatalf("confirmation-only batch tool row = %v, want nil", repo2.batchTools[0])
	}
	if len(repo2.batchConfirms) != 1 || repo2.batchConfirms[0] == nil {
		t.Fatal("confirmation-only batch confirm row missing")
	}
}

func TestAuditUsecase_query_filter_boundaries(t *testing.T) {
	uc := NewAuditUsecase(&auditFakeRepo{}, redact.NewStandardRegistry())
	ctx := auditIdentityContext(false)
	// 合法边界：缺省页大小由用例补齐。
	f, err := auditFilterOf(&AuditFilter{})
	if err != nil {
		t.Fatalf("default filter: %v", err)
	}
	if f.Limit != 20 || f.Offset != 0 {
		t.Fatalf("default filter = (%d, %d), want (20, 0)", f.Limit, f.Offset)
	}
	for _, bad := range []AuditFilter{{Limit: -1}, {Limit: 201}, {Offset: -1}} {
		if _, err := uc.ListToolCalls(ctx, &bad); !errors.Is(err, ErrAuditInvalidArgument) {
			t.Fatalf("ListToolCalls(%+v) = %v, want ErrAuditInvalidArgument", bad, err)
		}
		if _, err := uc.ListConfirmations(ctx, &bad); !errors.Is(err, ErrAuditInvalidArgument) {
			t.Fatalf("ListConfirmations(%+v) = %v, want ErrAuditInvalidArgument", bad, err)
		}
	}
	if _, err := uc.GetToolCall(ctx, "  "); !errors.Is(err, ErrAuditInvalidArgument) {
		t.Fatalf("GetToolCall(blank) = %v, want ErrAuditInvalidArgument", err)
	}
}

// maxInt32 是 duration_ms 列的数值上限（int4）。
const maxInt32 = int64(2147483647)
