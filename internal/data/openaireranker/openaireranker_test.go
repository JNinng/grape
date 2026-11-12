package openaireranker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
)

// stubCatalog 是测试用重排模型目录：返回固定端点事实或注入解析错误
// （与生产解析器「键完全未配置即 ErrRerankUnavailable」同语义）。
type stubCatalog struct {
	spec biz.RerankModelSpec
	err  error
}

func (c *stubCatalog) ResolveRerankModel(context.Context) (biz.RerankModelSpec, error) {
	if c.err != nil {
		return biz.RerankModelSpec{}, c.err
	}
	return c.spec, nil
}

// stubEndpoint 起一个记录请求的替身 /rerank 端点，按 results 返回固定
// 应答；断言请求形态（路径/方法/model/query/documents/鉴权头）后由用例
// 消费。
func stubEndpoint(t *testing.T, results []map[string]any, status int) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if results != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func newRerankerForTest(baseURL string) (biz.Reranker, func(), error) {
	return New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", BaseURL: baseURL, Model: "bge-reranker", APIKey: "test-key", Timeout: time.Second}}, nil)
}

func TestReranker_when_nil_catalog_rejected(t *testing.T) {
	if _, _, err := New(nil, nil); err == nil {
		t.Fatal("New(nil catalog) = nil error, want rejection")
	}
}

func TestReranker_when_rerank_posts_jina_style_request(t *testing.T) {
	srv, captured := stubEndpoint(t, []map[string]any{
		{"index": 0, "relevance_score": 0.3},
		{"index": 1, "relevance_score": 0.8},
	}, http.StatusOK)
	c, cleanup, err := newRerankerForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	got, err := c.Rerank(context.Background(), "年假政策", []string{"doc-a", "doc-b"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 2 || got[0] != 0.3 || got[1] != 0.8 {
		t.Fatalf("scores = %v, want input-aligned [0.3, 0.8]", got)
	}
	var req struct {
		Model     string   `json:"model"`
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}
	if err := json.Unmarshal(*captured, &req); err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	if req.Model != "bge-reranker" || req.Query != "年假政策" {
		t.Fatalf("request = (%q, %q), want resolved model and query passthrough", req.Model, req.Query)
	}
	if len(req.Documents) != 2 || req.Documents[0] != "doc-a" || req.Documents[1] != "doc-b" {
		t.Fatalf("documents = %v, want input order preserved", req.Documents)
	}
}

func TestReranker_when_results_out_of_order_restored_to_input_alignment(t *testing.T) {
	// 端点按相关性序返回（乱序 index）：分数必须按 index 还原为与输入
	// 对齐的切片，不做任何重排。
	srv, _ := stubEndpoint(t, []map[string]any{
		{"index": 2, "relevance_score": 0.9},
		{"index": 0, "relevance_score": 0.1},
		{"index": 1, "relevance_score": 0.5},
	}, http.StatusOK)
	c, cleanup, err := newRerankerForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	got, err := c.Rerank(context.Background(), "query", []string{"doc-a", "doc-b", "doc-c"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 3 || got[0] != 0.1 || got[1] != 0.5 || got[2] != 0.9 {
		t.Fatalf("scores = %v, want index-restored [0.1, 0.5, 0.9]", got)
	}
}

func TestReranker_when_request_carries_bearer_key_and_rerank_path(t *testing.T) {
	var authHeader, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
			{"index": 0, "relevance_score": 0.5},
		}})
	}))
	defer srv.Close()
	c, cleanup, err := newRerankerForTest(srv.URL + "/")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc"}); err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if authHeader != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", authHeader)
	}
	if path != "/rerank" {
		t.Fatalf("path = %q, want /rerank (base_url trailing slash normalized)", path)
	}
}

func TestReranker_when_results_incomplete_or_out_of_range_rejected(t *testing.T) {
	cases := map[string][]map[string]any{
		"missing index":   {{"index": 0, "relevance_score": 0.5}},
		"duplicate index": {{"index": 0, "relevance_score": 0.1}, {"index": 0, "relevance_score": 0.2}},
		"index negative":  {{"index": 0, "relevance_score": 0.1}, {"index": -1, "relevance_score": 0.2}},
		"index too large": {{"index": 0, "relevance_score": 0.1}, {"index": 2, "relevance_score": 0.2}},
		"empty results":   {},
	}
	for name, results := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := stubEndpoint(t, results, http.StatusOK)
			c, cleanup, err := newRerankerForTest(srv.URL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer cleanup()
			if _, err := c.Rerank(context.Background(), "query", []string{"doc-a", "doc-b"}); err == nil {
				t.Fatalf("Rerank with %s = nil error, want protocol rejection", name)
			}
		})
	}
}

func TestReranker_when_endpoint_errors_mapped(t *testing.T) {
	srv, _ := stubEndpoint(t, nil, http.StatusUnauthorized)
	c, cleanup, err := newRerankerForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Rerank on 401 = %v, want status error", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer bad.Close()
	c2, cleanup2, err := newRerankerForTest(bad.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup2()
	if _, err := c2.Rerank(context.Background(), "query", []string{"doc"}); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("Rerank on malformed body = %v, want decode error", err)
	}
}

func TestReranker_when_query_blank_rejected(t *testing.T) {
	srv, _ := stubEndpoint(t, []map[string]any{{"index": 0, "relevance_score": 0.5}}, http.StatusOK)
	c, cleanup, err := newRerankerForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "   ", []string{"doc"}); err == nil {
		t.Fatal("Rerank(blank query) = nil error, want rejection")
	}
}

func TestReranker_when_documents_empty_returns_without_network(t *testing.T) {
	// 空 documents 直接返回空分数：不解析模型、不触网（端点被命中即失败）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("rerank endpoint must not be called for empty documents")
	}))
	defer srv.Close()
	c, cleanup, err := New(&stubCatalog{err: biz.ErrRerankUnavailable}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	// 端点指向 catalog 报错的替身（URL 不会被消费）：即便未配置也先按
	// 空 documents 短路。真实端点替身不会被命中。
	got, err := c.Rerank(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("Rerank(empty documents) = %v, want empty scores", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("scores = %#v, want empty non-nil slice", got)
	}
}

func TestReranker_when_catalog_unavailable_passthrough_sentinel(t *testing.T) {
	// 键完全未配置：ErrRerankUnavailable 原样透传，消费方按跳过该层处理。
	c, cleanup, err := New(&stubCatalog{err: biz.ErrRerankUnavailable}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc"}); !errors.Is(err, biz.ErrRerankUnavailable) {
		t.Fatalf("Rerank with unconfigured model = %v, want ErrRerankUnavailable", err)
	}
}

func TestReranker_when_catalog_fails_other_error_passthrough(t *testing.T) {
	// 配置了但坏了（行停用等，由目录解析为普通错误）：透传给调用方按
	// 调用失败降级。
	c, cleanup, err := New(&stubCatalog{err: errors.New("pg_sqlc: rerank model is disabled")}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	_, err = c.Rerank(context.Background(), "query", []string{"doc"})
	if err == nil || errors.Is(err, biz.ErrRerankUnavailable) || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Rerank with broken model = %v, want plain passthrough error", err)
	}
}

func TestReranker_when_spec_missing_base_url_rejected(t *testing.T) {
	c, cleanup, err := New(&stubCatalog{spec: biz.RerankModelSpec{Key: "bge-reranker", Model: "bge-reranker"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Rerank(context.Background(), "query", []string{"doc"}); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("Rerank without base_url = %v, want base_url error", err)
	}
}
