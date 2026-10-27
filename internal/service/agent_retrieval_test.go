package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
)

// retrievalFakeRepo 是检索留痕查询端点单测的假仓储：只支撑读面。
type retrievalFakeRepo struct {
	listErr error
	rows    []*biz.RetrievalLog
}

func (r *retrievalFakeRepo) Append(context.Context, *biz.RetrievalLogRecord) error {
	return errors.New("not implemented")
}

func (r *retrievalFakeRepo) List(_ context.Context, f *biz.RetrievalLogFilter) (*biz.RetrievalLogList, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return &biz.RetrievalLogList{Items: r.rows, Total: int64(len(r.rows))}, nil
}

// newRetrievalTestService 构造被测端点与种子行（落库已脱敏形态）。
func newRetrievalTestService(t *testing.T) (*AgentRetrievalService, *retrievalFakeRepo) {
	t.Helper()
	repo := &retrievalFakeRepo{rows: []*biz.RetrievalLog{{
		ID: 5, TenantID: "t-1", UserID: 7, TraceID: "0123456789abcdef0123456789abcdef",
		SessionID: "sess-1", Source: biz.RetrievalSourceTool,
		QueryText: "grape 检索", RequestedTopK: 3, EffectiveTopK: 3,
		FilterSummary: json.RawMessage(`{"metadata":{"source_id":"doc"}}`),
		Hits:          json.RawMessage(`[{"document_id":"doc#0","score":0.9,"anchor":"runes 0-800"}]`),
		DurationMS:    12, Status: biz.RetrievalStatusSuccess, CreatedAt: time.Unix(1700000000, 0),
	}}}
	return NewAgentRetrievalService(biz.NewRetrievalLogUsecase(repo)), repo
}

// retrievalIdentityRequest 以认证身份构造请求（生产由中间件注入）。
func retrievalIdentityRequest(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(biz.WithIdentity(r.Context(), biz.Identity{TenantID: "t-1", UserID: 7}))
}

func TestAgentRetrieval_list_renders_redacted_row_with_evidence_fields(t *testing.T) {
	svc, _ := newRetrievalTestService(t)

	rec := httptest.NewRecorder()
	svc.retrievalLogs(retrievalIdentityRequest("/v1/agent/retrieval-logs"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"total":1`,
		`"source":"tool"`,
		`"status":"success"`,
		`"queryText":"grape 检索"`,
		`"requestedTopK":3`,
		`"effectiveTopK":3`,
		`"filterSummary":{"metadata":{"source_id":"doc"}}`,
		`"hits":[{"document_id":"doc#0","score":0.9,"anchor":"runes 0-800"}]`,
		`"sessionId":"sess-1"`,
		`"traceId":"0123456789abcdef0123456789abcdef"`,
		`"durationMs":12`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s:\n%s", want, body)
		}
	}
	// 时间以 UTC RFC3339 渲染。
	if !strings.Contains(body, time.Unix(1700000000, 0).UTC().Format(time.RFC3339)) {
		t.Fatalf("body missing RFC3339 created_at:\n%s", body)
	}
}

func TestAgentRetrieval_list_without_token_fails_closed(t *testing.T) {
	svc, _ := newRetrievalTestService(t)

	rec := httptest.NewRecorder()
	svc.retrievalLogs(httptest.NewRequest(http.MethodGet, "/v1/agent/retrieval-logs", nil), rec)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (fail-closed without identity)", rec.Code)
	}
}

func TestAgentRetrieval_invalid_pagination_and_source_rejected(t *testing.T) {
	svc, _ := newRetrievalTestService(t)

	for _, target := range []string{
		"/v1/agent/retrieval-logs?limit=0",
		"/v1/agent/retrieval-logs?limit=-1",
		"/v1/agent/retrieval-logs?limit=abc",
		"/v1/agent/retrieval-logs?limit=201",
		"/v1/agent/retrieval-logs?offset=-1",
		"/v1/agent/retrieval-logs?offset=abc",
		"/v1/agent/retrieval-logs?source=unknown",
	} {
		rec := httptest.NewRecorder()
		svc.retrievalLogs(retrievalIdentityRequest(target), rec)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400\n%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentRetrieval_storage_failure_is_fixed_internal_error(t *testing.T) {
	svc, repo := newRetrievalTestService(t)
	repo.listErr = errors.New("storage down")

	rec := httptest.NewRecorder()
	svc.retrievalLogs(retrievalIdentityRequest("/v1/agent/retrieval-logs"), rec)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("body = %s, want fixed message without internals", rec.Body.String())
	}
}

func TestAgentRetrieval_default_page_size_applied_when_unset(t *testing.T) {
	repo := &retrievalFakeRepo{}
	svc := NewAgentRetrievalService(biz.NewRetrievalLogUsecase(repo))

	// 假仓储不消费 filter；用例侧的缺省补齐经 Repository 契约透传校验
	// 在 biz 单测覆盖，这里验证缺省请求 200 且信封恒有 items 数组。
	rec := httptest.NewRecorder()
	svc.retrievalLogs(retrievalIdentityRequest("/v1/agent/retrieval-logs"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty page must render items array:\n%s", rec.Body.String())
	}
}
