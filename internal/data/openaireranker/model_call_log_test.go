package openaireranker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"grape/internal/biz"
)

// recordingObserver 是留痕观察面的替身：记录收到的载体，供行字段断言。
type recordingObserver struct {
	entries []*biz.ModelCallEntry
}

func (o *recordingObserver) ObserveModelCall(_ context.Context, entry *biz.ModelCallEntry) {
	o.entries = append(o.entries, entry)
}

// rerankEndpoint 起一个带 usage 段的替身 /rerank 端点（留痕面消费
// prompt_tokens/completion_tokens）。
func rerankEndpoint(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"index": 0, "relevance_score": 0.2},
					{"index": 1, "relevance_score": 0.7},
				},
				"usage": map[string]any{"prompt_tokens": 96, "completion_tokens": 0},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestModelCallLog_success_row_records_endpoint_usage_and_fallback_scene(t *testing.T) {
	srv := rerankEndpoint(t, http.StatusOK)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", BaseURL: srv.URL, Model: "bge-reranker"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	// ctx 未声明场景：缺省回落 rerank；检索链路无 kb_document 行上下文，
	// 文档 ID 恒缺省（零值）。
	if _, err := c.Rerank(context.Background(), "年假", []string{"doc-a", "doc-b"}); err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(obs.entries) != 1 {
		t.Fatalf("observed rows = %d, want 1", len(obs.entries))
	}
	row := obs.entries[0]
	switch {
	case row.Scenario != biz.ModelCallScenarioRerank:
		t.Fatalf("scenario = %q, want fallback rerank", row.Scenario)
	case row.ModelKey != "bge-reranker":
		t.Fatalf("model key = %q, want resolved rerank model key", row.ModelKey)
	case row.Endpoint != srv.URL+"/rerank":
		t.Fatalf("endpoint = %q, want protocol path url", row.Endpoint)
	case row.InputSummary != "年假":
		t.Fatalf("input = %q, want query text", row.InputSummary)
	case row.OutputSummary == "":
		t.Fatalf("output = %q, want document count description", row.OutputSummary)
	case row.InputTokens != 96 || row.OutputTokens != 0:
		t.Fatalf("tokens = (%d, %d), want endpoint usage with unreported completion", row.InputTokens, row.OutputTokens)
	case row.DocumentID != 0:
		t.Fatalf("document id = %d, want 0 (no kb_document facts)", row.DocumentID)
	case row.Status != biz.ModelCallStatusSuccess || row.Error != "":
		t.Fatalf("status = (%s, %q), want success without error", row.Status, row.Error)
	}
}

func TestModelCallLog_declared_scene_honored(t *testing.T) {
	srv := rerankEndpoint(t, http.StatusOK)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", BaseURL: srv.URL, Model: "bge-reranker"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	ctx := biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioEmbedQuery)
	if _, err := c.Rerank(ctx, "query", []string{"doc-a", "doc-b"}); err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if obs.entries[0].Scenario != biz.ModelCallScenarioEmbedQuery {
		t.Fatalf("scenario = %q, want declared scene to win", obs.entries[0].Scenario)
	}
}

func TestModelCallLog_error_row_records_failure_and_keeps_call_error(t *testing.T) {
	srv := rerankEndpoint(t, http.StatusUnauthorized)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", BaseURL: srv.URL, Model: "bge-reranker"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	if _, err := c.Rerank(context.Background(), "query", []string{"doc-a", "doc-b"}); err == nil {
		t.Fatal("Rerank on 401 = nil error, want failure")
	}
	if len(obs.entries) != 1 {
		t.Fatalf("observed rows = %d, want 1 (failure is evidence too)", len(obs.entries))
	}
	row := obs.entries[0]
	switch {
	case row.Status != biz.ModelCallStatusError:
		t.Fatalf("status = %s, want error", row.Status)
	case row.Error == "":
		t.Fatalf("error = %q, want failure summary", row.Error)
	case row.OutputSummary != "":
		t.Fatalf("output = %q, want empty on failed call", row.OutputSummary)
	case row.InputTokens != 0 || row.OutputTokens != 0:
		t.Fatalf("tokens = (%d, %d), want NULL semantics on failed call", row.InputTokens, row.OutputTokens)
	}
}

func TestModelCallLog_catalog_failure_writes_no_row(t *testing.T) {
	// 模型解析失败（无端点事实）不留行：留痕点从解析成功后开始。
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{err: biz.ErrRerankUnavailable}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc"}); err == nil {
		t.Fatal("Rerank = nil error, want model unavailable failure")
	}
	if len(obs.entries) != 0 {
		t.Fatalf("observed rows = %d, want 0 (no endpoint facts)", len(obs.entries))
	}
}

func TestModelCallLog_nil_observer_zero_cost(t *testing.T) {
	// 观察面缺省 nil：调用照常成功，无任何留痕副作用。
	srv := rerankEndpoint(t, http.StatusOK)
	c, cleanup, err := New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", BaseURL: srv.URL, Model: "bge-reranker"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc-a", "doc-b"}); err != nil {
		t.Fatalf("Rerank with nil observer = %v, want success", err)
	}
}
