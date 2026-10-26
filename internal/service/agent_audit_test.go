package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
)

// auditFakeRepo 是审计查询端点单测的假仓储：只支撑读面与点查。
type auditFakeRepo struct {
	toolCall     *biz.ToolCallAudit
	confirmation *biz.ConfirmationAudit
}

func (r *auditFakeRepo) AppendToolCall(context.Context, *biz.ToolCallAudit) error {
	return errors.New("not implemented")
}

func (r *auditFakeRepo) AppendConfirmation(context.Context, *biz.ConfirmationAudit) error {
	return errors.New("not implemented")
}

func (r *auditFakeRepo) AppendBatch(context.Context, *biz.ToolCallAudit, *biz.ConfirmationAudit) error {
	return errors.New("not implemented")
}

func (r *auditFakeRepo) ListToolCalls(_ context.Context, f *biz.AuditFilter) (*biz.ToolCallAuditList, error) {
	if f.SessionID == "boom" {
		return nil, errors.New("storage down")
	}
	return &biz.ToolCallAuditList{Items: []*biz.ToolCallAudit{r.toolCall}, Total: 1}, nil
}

func (r *auditFakeRepo) ListConfirmations(_ context.Context, f *biz.AuditFilter) (*biz.ConfirmationAuditList, error) {
	if f.SessionID == "boom" {
		return nil, errors.New("storage down")
	}
	return &biz.ConfirmationAuditList{Items: []*biz.ConfirmationAudit{r.confirmation}, Total: 1}, nil
}

func (r *auditFakeRepo) GetToolCall(_ context.Context, id string) (*biz.ToolCallAudit, error) {
	if r.toolCall != nil && r.toolCall.ToolCallID == id {
		return r.toolCall, nil
	}
	return nil, biz.ErrAuditNotFound
}

func (r *auditFakeRepo) GetConfirmation(_ context.Context, id string) (*biz.ConfirmationAudit, error) {
	if r.confirmation != nil && r.confirmation.ConfirmationID == id {
		return r.confirmation, nil
	}
	return nil, biz.ErrAuditNotFound
}

// newAuditTestService 构造被测端点与种子行。
func newAuditTestService(t *testing.T) (*AgentAuditService, *auditFakeRepo) {
	t.Helper()
	repo := &auditFakeRepo{
		toolCall: &biz.ToolCallAudit{
			ID: 1, TenantID: "t-1", UserID: 7, TraceID: "0123456789abcdef0123456789abcdef",
			SessionID: "sess-1", ToolCallID: "call-1", ToolName: "echo",
			ArgumentsSummary: `{"query":"grape"}`, ResultSummary: "ok", RetryCount: 1,
			DurationMS: 42, Status: biz.ToolCallStatusSuccess, CalledAt: time.Unix(1700000000, 0),
		},
		confirmation: &biz.ConfirmationAudit{
			ID: 2, TenantID: "t-1", UserID: 7, ConfirmationID: "cf-1", SessionID: "sess-1",
			ToolCallID: "call-1", ToolName: "write", Decision: biz.ConfirmationDecisionApproved,
			Remember: biz.ConfirmationRememberSession, Path: biz.ConfirmationPathLive,
			DecidedBy: "t-1:7", DecidedAt: time.Unix(1700000001, 0),
		},
	}
	return NewAgentAuditService(biz.NewAuditUsecase(repo, nil)), repo
}

// auditIdentityRequest 以认证身份构造请求（生产由中间件注入）。
func auditIdentityRequest(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(biz.WithIdentity(r.Context(), biz.Identity{TenantID: "t-1", UserID: 7}))
}

func TestAgentAudit_tool_calls_point_query_hit_miss_and_mixed_params(t *testing.T) {
	svc, _ := newAuditTestService(t)

	rec := httptest.NewRecorder()
	svc.toolCalls(auditIdentityRequest("/v1/agent/audit/tool-calls?tool_call_id=call-1"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("point query status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"toolCallId":"call-1"`) || !strings.Contains(rec.Body.String(), `"retryCount":1`) {
		t.Fatalf("point query body mismatch: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	svc.toolCalls(auditIdentityRequest("/v1/agent/audit/tool-calls?tool_call_id=missing"), rec)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing point query status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}

	// 点查与列表参数混用：歧义请求显式 400。
	for _, mixed := range []string{
		"/v1/agent/audit/tool-calls?tool_call_id=call-1&session_id=sess-1",
		"/v1/agent/audit/tool-calls?tool_call_id=call-1&limit=5",
		"/v1/agent/audit/tool-calls?tool_call_id=call-1&offset=1",
	} {
		rec = httptest.NewRecorder()
		svc.toolCalls(auditIdentityRequest(mixed), rec)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mixed query %s status = %d, want 400\n%s", mixed, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentAudit_tool_calls_list_renders_items_and_total(t *testing.T) {
	svc, _ := newAuditTestService(t)
	rec := httptest.NewRecorder()
	svc.toolCalls(auditIdentityRequest("/v1/agent/audit/tool-calls?session_id=sess-1"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"total":1`) || !strings.Contains(rec.Body.String(), `"items":[{`) {
		t.Fatalf("list body mismatch: %s", rec.Body.String())
	}
	// 时间以 UTC RFC3339 渲染。
	if !strings.Contains(rec.Body.String(), `"calledAt":"2023-11-14T22:13:20Z"`) {
		t.Fatalf("calledAt not rendered as UTC RFC3339: %s", rec.Body.String())
	}
}

func TestAgentAudit_confirmations_point_query_and_list(t *testing.T) {
	svc, _ := newAuditTestService(t)

	rec := httptest.NewRecorder()
	svc.confirmations(auditIdentityRequest("/v1/agent/audit/confirmations?confirmation_id=cf-1"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("point query status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"decision":"approved"`) || !strings.Contains(rec.Body.String(), `"path":"live"`) {
		t.Fatalf("point query body mismatch: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	svc.confirmations(auditIdentityRequest("/v1/agent/audit/confirmations"), rec)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("list status/body = (%d, %s), want 200 with total", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	svc.confirmations(auditIdentityRequest("/v1/agent/audit/confirmations?confirmation_id=other"), rec)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing point query status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
}

func TestAgentAudit_pagination_params_fail_closed(t *testing.T) {
	svc, _ := newAuditTestService(t)
	for _, bad := range []string{
		"/v1/agent/audit/tool-calls?offset=-1",
		"/v1/agent/audit/tool-calls?limit=0",
		"/v1/agent/audit/tool-calls?limit=201",
		"/v1/agent/audit/tool-calls?offset=abc",
	} {
		rec := httptest.NewRecorder()
		svc.toolCalls(auditIdentityRequest(bad), rec)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400\n%s", bad, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentAudit_unauthenticated_request_is_unauthorized(t *testing.T) {
	svc, _ := newAuditTestService(t)
	for _, target := range []string{"/v1/agent/audit/tool-calls", "/v1/agent/audit/confirmations"} {
		rec := httptest.NewRecorder()
		svc.toolCalls(httptest.NewRequest(http.MethodGet, target, nil), rec)
		if target == "/v1/agent/audit/tool-calls" && rec.Code != http.StatusUnauthorized {
			t.Fatalf("tool-calls without identity status = %d, want 401", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	svc.confirmations(httptest.NewRequest(http.MethodGet, "/v1/agent/audit/confirmations", nil), rec)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("confirmations without identity status = %d, want 401", rec.Code)
	}
}

func TestAgentAudit_internal_error_is_fixed_message(t *testing.T) {
	svc, _ := newAuditTestService(t)
	rec := httptest.NewRecorder()
	svc.toolCalls(auditIdentityRequest("/v1/agent/audit/tool-calls?session_id=boom"), rec)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "storage down") {
		t.Fatalf("internal error leaks driver detail: %s", body)
	}
}
