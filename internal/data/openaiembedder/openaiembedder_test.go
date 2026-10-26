package openaiembedder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
)

// embeddingConf 构造启用态配置：dimension 4 便于小向量断言。
func embeddingConf(baseURL string) *conf.Vector {
	return &conf.Vector{
		Enabled:   true,
		Dimension: 4,
		Embedding: &conf.Vector_Embedding{
			BaseUrl: baseURL,
			ApiKey:  "test-key",
			Model:   "test-embedding-model",
		},
	}
}

// stubEndpoint 起一个记录请求的替身 embeddings 端点，按 vector 返回
// 固定向量；断言请求形态（路径/方法/model/input/鉴权头）后由用例消费。
func stubEndpoint(t *testing.T, vector []float64, status int) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": vector}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestEmbedder_when_facility_disabled_returns_placeholder(t *testing.T) {
	e, cleanup, err := New(&conf.Vector{Enabled: false})
	if err != nil {
		t.Fatalf("New(disabled) = %v, want disabled placeholder", err)
	}
	defer cleanup()
	if e.Dimensions() != 0 {
		t.Fatalf("disabled Dimensions = %d, want 0", e.Dimensions())
	}
	if _, err := e.Embed(context.Background(), "text"); !errors.Is(err, biz.ErrEmbedderDisabled) {
		t.Fatalf("disabled Embed = %v, want ErrEmbedderDisabled", err)
	}
}

func TestEmbedder_when_enabled_config_incomplete_fails_fast(t *testing.T) {
	for name, c := range map[string]*conf.Vector{
		"missing base_url": {Enabled: true, Dimension: 4, Embedding: &conf.Vector_Embedding{Model: "m"}},
		"missing model":    {Enabled: true, Dimension: 4, Embedding: &conf.Vector_Embedding{BaseUrl: "http://localhost"}},
		"missing dimension": {Enabled: true, Embedding: &conf.Vector_Embedding{
			BaseUrl: "http://localhost", Model: "m"}},
	} {
		_, _, err := New(c)
		if err == nil {
			t.Fatalf("New(%s) = nil error, want rejection", name)
		}
	}
}

func TestEmbedder_when_embed_posts_openai_compatible_request(t *testing.T) {
	srv, captured := stubEndpoint(t, []float64{0.25, 0.5, 0.25, 0.5}, http.StatusOK)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	got, err := e.Embed(context.Background(), "hello document")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// 向量按响应回填且转 float32。
	if len(got) != 4 || got[0] != 0.25 || got[1] != 0.5 {
		t.Fatalf("embedding = %v, want [0.25 0.5 0.25 0.5]", got)
	}
	var req map[string]any
	if err := json.Unmarshal(*captured, &req); err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	if req["model"] != "test-embedding-model" || req["input"] != "hello document" {
		t.Fatalf("request payload = %v, want model/input passthrough", req)
	}
}

func TestEmbedder_when_request_carries_bearer_key_and_embeddings_path(t *testing.T) {
	var authHeader, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"index": 0, "embedding": make([]float64, 4)}},
		})
	}))
	defer srv.Close()
	e, cleanup, err := New(embeddingConf(srv.URL + "/"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "text"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if authHeader != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", authHeader)
	}
	if path != "/embeddings" {
		t.Fatalf("path = %q, want /embeddings (base_url trailing slash normalized)", path)
	}
}

func TestEmbedder_when_response_dimension_drifts_rejected(t *testing.T) {
	// 端点返回 3 维向量而配置 4 维：调用点显式报错，绝不产出零值向量。
	srv, _ := stubEndpoint(t, []float64{0.1, 0.2, 0.3}, http.StatusOK)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	_, err = e.Embed(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("Embed with drifted dimension = %v, want dimension error", err)
	}
}

func TestEmbedder_when_endpoint_errors_mapped(t *testing.T) {
	srv, _ := stubEndpoint(t, nil, http.StatusUnauthorized)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "text"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Embed on 401 = %v, want status error", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer empty.Close()
	e2, cleanup2, err := New(embeddingConf(empty.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup2()
	if _, err := e2.Embed(context.Background(), "text"); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("Embed on empty data = %v, want no-data error", err)
	}
}

func TestEmbedder_when_text_blank_rejected(t *testing.T) {
	srv, _ := stubEndpoint(t, make([]float64, 4), http.StatusOK)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "   "); err == nil {
		t.Fatal("Embed(blank) = nil error, want rejection")
	}
}

func TestEmbedder_when_timeout_unset_falls_back_to_default(t *testing.T) {
	// 只验证构造成功与缺省时长落位；具体传输行为由 http.Client 语义保证。
	c := embeddingConf("http://127.0.0.1:1")
	c.Embedding.Timeout = nil
	e, cleanup, err := New(c)
	if err != nil {
		t.Fatalf("New without timeout = %v, want default fallback", err)
	}
	defer cleanup()
	if e.(*openAIEmbedder).client.Timeout != defaultTimeout {
		t.Fatalf("client timeout = %v, want %v", e.(*openAIEmbedder).client.Timeout, defaultTimeout)
	}
}
