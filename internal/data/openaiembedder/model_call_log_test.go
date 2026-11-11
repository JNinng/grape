package openaiembedder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// usageStubEndpoint 起一个带 usage 段的替身 embeddings 端点（留痕面消费
// prompt_tokens）。
func usageStubEndpoint(t *testing.T, vector []float64, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":  []map[string]any{{"index": 0, "embedding": vector}},
				"usage": map[string]any{"prompt_tokens": 96},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestModelCallLog_success_row_records_usage_dimension_note_and_fallback_scene(t *testing.T) {
	srv := usageStubEndpoint(t, make([]float64, 4), http.StatusOK)
	obs := &recordingObserver{}
	e, cleanup, err := New(resolverFromConf(embeddingConf(srv.URL)), embeddingConf(srv.URL), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	// ctx 未声明场景：缺省回落 embed_document；声明文档 ID 时透传。
	ctx := biz.WithModelCallDocument(context.Background(), 7)
	vector, err := e.Embed(ctx, "test-embedding-model", "chunk body")
	if err != nil || len(vector) != 4 {
		t.Fatalf("Embed = (%d dims, %v), want 4 dims", len(vector), err)
	}
	if len(obs.entries) != 1 {
		t.Fatalf("observed rows = %d, want 1", len(obs.entries))
	}
	row := obs.entries[0]
	switch {
	case row.Scenario != biz.ModelCallScenarioEmbedDocument:
		t.Fatalf("scenario = %q, want fallback embed_document", row.Scenario)
	case row.ModelKey != "test-embedding-model":
		t.Fatalf("model key = %q, want resolved space key", row.ModelKey)
	case row.Endpoint != srv.URL+"/embeddings":
		t.Fatalf("endpoint = %q, want protocol path url", row.Endpoint)
	case row.InputSummary != "chunk body":
		t.Fatalf("input = %q, want embed text", row.InputSummary)
	case row.InputTokens != 96:
		t.Fatalf("input tokens = %d, want endpoint usage", row.InputTokens)
	case row.OutputSummary != "embedding vector of 4 dimensions (vector not persisted)":
		t.Fatalf("output = %q, want dimension note without vector content", row.OutputSummary)
	case row.DocumentID != 7:
		t.Fatalf("document id = %d, want ctx fact", row.DocumentID)
	case row.Status != biz.ModelCallStatusSuccess || row.Error != "":
		t.Fatalf("status = (%s, %q), want success without error", row.Status, row.Error)
	}
}

func TestModelCallLog_declared_scene_honored(t *testing.T) {
	srv := usageStubEndpoint(t, make([]float64, 4), http.StatusOK)
	obs := &recordingObserver{}
	e, cleanup, err := New(resolverFromConf(embeddingConf(srv.URL)), embeddingConf(srv.URL), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	// 检索链路显式声明 embed_query（无文档事实）。
	ctx := biz.WithModelCallScene(context.Background(), biz.ModelCallScenarioEmbedQuery)
	if _, err := e.Embed(ctx, "test-embedding-model", "search query"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	row := obs.entries[0]
	if row.Scenario != biz.ModelCallScenarioEmbedQuery {
		t.Fatalf("scenario = %q, want declared embed_query", row.Scenario)
	}
	if row.DocumentID != 0 {
		t.Fatalf("document id = %d, want NULL semantics (query embedding has no document)", row.DocumentID)
	}
}

func TestModelCallLog_error_row_records_failure(t *testing.T) {
	// 维度漂移：调用失败同样留行，tokens 取端点已上报值。
	srv := usageStubEndpoint(t, []float64{0.1, 0.2, 0.3}, http.StatusOK)
	obs := &recordingObserver{}
	e, cleanup, err := New(resolverFromConf(embeddingConf(srv.URL)), embeddingConf(srv.URL), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "test-embedding-model", "text"); err == nil {
		t.Fatal("Embed with drifted dimension = nil error, want failure")
	}
	row := obs.entries[0]
	switch {
	case row.Status != biz.ModelCallStatusError:
		t.Fatalf("status = %s, want error", row.Status)
	case row.Error == "" || !strings.Contains(row.Error, "dimension"):
		t.Fatalf("error = %q, want dimension drift summary", row.Error)
	case row.OutputSummary != "":
		t.Fatalf("output = %q, want empty on failed call", row.OutputSummary)
	case row.InputTokens != 96:
		t.Fatalf("input tokens = %d, want endpoint-reported usage retained on failure", row.InputTokens)
	}
}

func TestModelCallLog_space_resolution_failure_writes_no_row(t *testing.T) {
	// 空间解析失败（无端点事实）不留行：留痕点从解析成功后开始。
	srv := usageStubEndpoint(t, make([]float64, 4), http.StatusOK)
	obs := &recordingObserver{}
	e, cleanup, err := New(resolverFromConf(embeddingConf(srv.URL)), embeddingConf(srv.URL), obs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "undeclared-space", "text"); err == nil {
		t.Fatal("Embed(unknown space) = nil error, want failure")
	}
	if len(obs.entries) != 0 {
		t.Fatalf("observed rows = %d, want 0 (no endpoint facts)", len(obs.entries))
	}
}

func TestModelCallLog_nil_observer_zero_cost(t *testing.T) {
	srv := usageStubEndpoint(t, make([]float64, 4), http.StatusOK)
	e, cleanup, err := New(resolverFromConf(embeddingConf(srv.URL)), embeddingConf(srv.URL), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if vector, err := e.Embed(context.Background(), "test-embedding-model", "text"); err != nil || len(vector) != 4 {
		t.Fatalf("Embed = (%d dims, %v), want success with nil observer", len(vector), err)
	}
}
