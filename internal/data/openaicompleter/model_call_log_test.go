package openaicompleter

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

// usageEndpoint 起一个带 usage 段的替身 chat completions 端点（留痕面
// 消费 prompt_tokens/completion_tokens）。
func usageEndpoint(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": "summary text"}}},
				"usage":   map[string]any{"prompt_tokens": 128, "completion_tokens": 64},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestModelCallLog_success_row_records_endpoint_usage_and_fallback_scene(t *testing.T) {
	srv := usageEndpoint(t, http.StatusOK)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: srv.URL, Model: "glm-4"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	// ctx 未声明场景：缺省回落 ingest_prefix；声明文档 ID 时透传。
	ctx := biz.WithModelCallDocument(context.Background(), 42)
	got, err := c.Complete(ctx, "summarize this")
	if err != nil || got != "summary text" {
		t.Fatalf("Complete = (%q, %v), want content", got, err)
	}
	if len(obs.entries) != 1 {
		t.Fatalf("observed rows = %d, want 1", len(obs.entries))
	}
	row := obs.entries[0]
	switch {
	case row.Scenario != biz.ModelCallScenarioIngestPrefix:
		t.Fatalf("scenario = %q, want fallback ingest_prefix", row.Scenario)
	case row.ModelKey != "glm-4":
		t.Fatalf("model key = %q, want resolved text model key", row.ModelKey)
	case row.Endpoint != srv.URL+"/chat/completions":
		t.Fatalf("endpoint = %q, want protocol path url", row.Endpoint)
	case row.InputSummary != "summarize this" || row.OutputSummary != "summary text":
		t.Fatalf("summaries = (%q, %q), want prompt and content", row.InputSummary, row.OutputSummary)
	case row.InputTokens != 128 || row.OutputTokens != 64:
		t.Fatalf("tokens = (%d, %d), want endpoint usage", row.InputTokens, row.OutputTokens)
	case row.DocumentID != 42:
		t.Fatalf("document id = %d, want ctx fact", row.DocumentID)
	case row.Status != biz.ModelCallStatusSuccess || row.Error != "":
		t.Fatalf("status = (%s, %q), want success without error", row.Status, row.Error)
	case row.DurationMS < 0:
		t.Fatalf("duration = %d, want measured", row.DurationMS)
	}
}

func TestModelCallLog_declared_scene_honored(t *testing.T) {
	srv := usageEndpoint(t, http.StatusOK)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: srv.URL, Model: "glm-4"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	ctx := biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioIngestSummary)
	if _, err := c.Complete(ctx, "prompt"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if obs.entries[0].Scenario != biz.ModelCallScenarioIngestSummary {
		t.Fatalf("scenario = %q, want declared ingest_summary", obs.entries[0].Scenario)
	}
}

func TestModelCallLog_error_row_records_failure_and_keeps_call_error(t *testing.T) {
	srv := usageEndpoint(t, http.StatusUnauthorized)
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: srv.URL, Model: "glm-4"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	if _, err := c.Complete(context.Background(), "prompt"); err == nil {
		t.Fatal("Complete on 401 = nil error, want failure")
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

func TestModelCallLog_empty_output_recorded_as_empty(t *testing.T) {
	// 空产出（content 为空串）照样留行：output 记空串，落库 NULL。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": ""}}},
		})
	}))
	defer srv.Close()
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: srv.URL, Model: "glm-4"}}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if obs.entries[0].OutputSummary != "" {
		t.Fatalf("output = %q, want empty output recorded", obs.entries[0].OutputSummary)
	}
}

func TestModelCallLog_catalog_failure_writes_no_row(t *testing.T) {
	// 模型解析失败（无端点事实）不留行：留痕点从解析成功后开始。
	obs := &recordingObserver{}
	c, cleanup, err := New(&stubCatalog{err: biz.ErrTextModelUnavailable}, obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); err == nil {
		t.Fatal("Complete = nil error, want model unavailable failure")
	}
	if len(obs.entries) != 0 {
		t.Fatalf("observed rows = %d, want 0 (no endpoint facts)", len(obs.entries))
	}
}

func TestModelCallLog_nil_observer_zero_cost(t *testing.T) {
	// 观察面缺省 nil：调用照常成功，无任何留痕副作用。
	srv := usageEndpoint(t, http.StatusOK)
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: srv.URL, Model: "glm-4"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if got, err := c.Complete(context.Background(), "prompt"); err != nil || got != "summary text" {
		t.Fatalf("Complete = (%q, %v), want content with nil observer", got, err)
	}
}
