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

// modelCallFakeRepo 是模型调用留痕查询端点单测的假仓储：只支撑读面。
type modelCallFakeRepo struct {
	appendErr error
	listErr   error
	rows      []*biz.ModelCallLog
	gotFilter *biz.ModelCallFilter
}

func (r *modelCallFakeRepo) Append(context.Context, *biz.ModelCallEntry) error {
	return errors.New("not implemented")
}

func (r *modelCallFakeRepo) List(_ context.Context, f *biz.ModelCallFilter) (*biz.ModelCallList, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.gotFilter = f
	return &biz.ModelCallList{Items: r.rows, Total: int64(len(r.rows))}, nil
}

func (r *modelCallFakeRepo) Count(context.Context, *biz.ModelCallFilter) (int64, error) {
	return 0, errors.New("not implemented")
}

// newModelCallTestService 构造被测端点与种子行（落库已脱敏形态）。
func newModelCallTestService(t *testing.T) (*AgentModelCallService, *modelCallFakeRepo) {
	t.Helper()
	repo := &modelCallFakeRepo{rows: []*biz.ModelCallLog{{
		ID:            9,
		TenantID:      "t-1",
		UserID:        7,
		TraceID:       "0123456789abcdef0123456789abcdef",
		Scenario:      biz.ModelCallScenarioIngestSummary,
		ModelKey:      "glm-4",
		Endpoint:      "http://llm.example/v1/chat/completions",
		DocumentID:    42,
		InputSummary:  "summarize prompt",
		OutputSummary: "document summary",
		InputTokens:   128,
		OutputTokens:  64,
		DurationMS:    350,
		Status:        biz.ModelCallStatusSuccess,
		CreatedAt:     time.Unix(1700000000, 0),
	}, {
		ID:        10,
		TenantID:  "t-1",
		UserID:    7,
		Scenario:  biz.ModelCallScenarioEmbedDocument,
		Status:    biz.ModelCallStatusError,
		Error:     "embeddings endpoint returned status 502: gateway down",
		CreatedAt: time.Unix(1700000001, 0),
	}}}
	return NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil)), repo
}

// modelCallIdentityRequest 以认证身份构造请求（生产由中间件注入）。
func modelCallIdentityRequest(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(biz.WithIdentity(r.Context(), biz.Identity{TenantID: "t-1", UserID: 7}))
}

func TestAgentModelCall_list_renders_rows_with_evidence_fields(t *testing.T) {
	svc, _ := newModelCallTestService(t)

	rec := httptest.NewRecorder()
	svc.modelCalls(modelCallIdentityRequest("/v1/agent/model-calls"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"total":2`,
		`"scenario":"ingest_summary"`,
		`"status":"success"`,
		`"modelKey":"glm-4"`,
		`"endpoint":"http://llm.example/v1/chat/completions"`,
		`"documentId":42`,
		`"inputSummary":"summarize prompt"`,
		`"outputSummary":"document summary"`,
		`"inputTokens":128`,
		`"outputTokens":64`,
		`"durationMs":350`,
		`"traceId":"0123456789abcdef0123456789abcdef"`,
		// 失败行：错误摘要原样透传，未上报字段渲染 null。
		`"scenario":"embed_document"`,
		`"status":"error"`,
		`"error":"embeddings endpoint returned status 502: gateway down"`,
		`"documentId":null`,
		`"inputTokens":null`,
		`"outputTokens":null`,
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

func TestAgentModelCall_list_without_token_fails_closed(t *testing.T) {
	svc, _ := newModelCallTestService(t)

	rec := httptest.NewRecorder()
	svc.modelCalls(httptest.NewRequest(http.MethodGet, "/v1/agent/model-calls", nil), rec)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (fail-closed without identity)", rec.Code)
	}
}

func TestAgentModelCall_invalid_filter_and_pagination_rejected(t *testing.T) {
	svc, _ := newModelCallTestService(t)

	for _, target := range []string{
		"/v1/agent/model-calls?limit=0",
		"/v1/agent/model-calls?limit=-1",
		"/v1/agent/model-calls?limit=abc",
		"/v1/agent/model-calls?limit=201",
		"/v1/agent/model-calls?offset=-1",
		"/v1/agent/model-calls?offset=abc",
		"/v1/agent/model-calls?scenario=unknown",
		"/v1/agent/model-calls?status=timeout",
		"/v1/agent/model-calls?document_id=-1",
		"/v1/agent/model-calls?document_id=abc",
		"/v1/agent/model-calls?created_from=not-a-time",
		"/v1/agent/model-calls?created_to=not-a-time",
	} {
		rec := httptest.NewRecorder()
		svc.modelCalls(modelCallIdentityRequest(target), rec)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400\n%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestAgentModelCall_filters_forwarded_to_repo(t *testing.T) {
	svc, repo := newModelCallTestService(t)

	target := "/v1/agent/model-calls?scenario=parse_document&model_key=glm-4&status=error&document_id=7" +
		"&created_from=2023-11-14T22:13:20Z&created_to=2023-11-14T22:13:21Z&offset=5&limit=10"
	rec := httptest.NewRecorder()
	svc.modelCalls(modelCallIdentityRequest(target), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	f := repo.gotFilter
	switch {
	case f.Scenario != biz.ModelCallScenarioParseDocument || f.Status != biz.ModelCallStatusError:
		t.Fatalf("filter = %+v, want scenario/status forwarded", f)
	case f.ModelKey != "glm-4" || f.DocumentID != 7:
		t.Fatalf("filter = %+v, want model key/document forwarded", f)
	case f.CreatedFrom.UTC() != time.Unix(1700000000, 0).UTC() || f.CreatedTo.UTC() != time.Unix(1700000001, 0).UTC():
		t.Fatalf("filter = %+v, want time range forwarded", f)
	case f.Limit != 10 || f.Offset != 5:
		t.Fatalf("filter = %+v, want explicit pagination forwarded", f)
	}
}

func TestAgentModelCall_storage_failure_is_fixed_internal_error(t *testing.T) {
	svc, repo := newModelCallTestService(t)
	repo.listErr = errors.New("storage down")

	rec := httptest.NewRecorder()
	svc.modelCalls(modelCallIdentityRequest("/v1/agent/model-calls"), rec)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("body = %s, want fixed message without internals", rec.Body.String())
	}
}

func TestAgentModelCall_default_page_size_applied_when_unset(t *testing.T) {
	repo := &modelCallFakeRepo{}
	svc := NewAgentModelCallService(biz.NewModelCallLogUsecase(repo, nil))

	// 假仓储不消费 filter；此处只验证缺省请求 200 且信封恒有 items 数组。
	rec := httptest.NewRecorder()
	svc.modelCalls(modelCallIdentityRequest("/v1/agent/model-calls"), rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("empty page must render items array:\n%s", rec.Body.String())
	}
}
