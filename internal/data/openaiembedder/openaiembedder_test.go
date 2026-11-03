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
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"google.golang.org/protobuf/types/known/durationpb"
)

// embeddingConf 构造启用态单空间配置：dimension 4 便于小向量断言。
func embeddingConf(baseURL string) *conf.Vector {
	return &conf.Vector{
		Enabled:         true,
		DefaultEmbedder: "test-embedding-model",
		Embedders: []*conf.Vector_Embedding{
			{
				BaseUrl:   baseURL,
				ApiKey:    "test-key",
				Model:     "test-embedding-model",
				Dimension: 4,
			},
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
	if _, err := e.Dimension("any-space"); !errors.Is(err, biz.ErrEmbedderDisabled) {
		t.Fatalf("disabled Dimension = %v, want ErrEmbedderDisabled", err)
	}
	if _, err := e.Embed(context.Background(), "any-space", "text"); !errors.Is(err, biz.ErrEmbedderDisabled) {
		t.Fatalf("disabled Embed = %v, want ErrEmbedderDisabled", err)
	}
}

func TestEmbedder_when_enabled_config_incomplete_fails_fast(t *testing.T) {
	ok := &conf.Vector_Embedding{BaseUrl: "http://localhost", Model: "m", Dimension: 4}
	for name, c := range map[string]*conf.Vector{
		"missing base_url": {Enabled: true, DefaultEmbedder: "m", Embedders: []*conf.Vector_Embedding{
			{Model: "m", Dimension: 4}}},
		"missing model": {Enabled: true, DefaultEmbedder: "m", Embedders: []*conf.Vector_Embedding{
			{BaseUrl: "http://localhost", Dimension: 4}}},
		"missing dimension": {Enabled: true, DefaultEmbedder: "m", Embedders: []*conf.Vector_Embedding{
			{BaseUrl: "http://localhost", Model: "m"}}},
		"no embedders":       {Enabled: true, DefaultEmbedder: "m"},
		"default undeclared": {Enabled: true, DefaultEmbedder: "other", Embedders: []*conf.Vector_Embedding{ok}},
		"duplicate keys": {Enabled: true, DefaultEmbedder: "m", Embedders: []*conf.Vector_Embedding{ok, {
			BaseUrl: "http://localhost", Model: "m", Dimension: 8}}},
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

	got, err := e.Embed(context.Background(), "test-embedding-model", "hello document")
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
	if _, err := e.Embed(context.Background(), "test-embedding-model", "text"); err != nil {
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
	// 端点返回 3 维向量而空间声明 4 维：调用点显式报错，绝不产出零值向量。
	srv, _ := stubEndpoint(t, []float64{0.1, 0.2, 0.3}, http.StatusOK)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	_, err = e.Embed(context.Background(), "test-embedding-model", "text")
	if err == nil || !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("Embed with drifted dimension = %v, want dimension error", err)
	}
}

func TestEmbedder_when_unknown_space_rejected(t *testing.T) {
	srv, _ := stubEndpoint(t, make([]float64, 4), http.StatusOK)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "undeclared-space", "text"); !errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("Embed(unknown space) = %v, want ErrEmbedderUnknown", err)
	}
	if _, err := e.Dimension("undeclared-space"); !errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("Dimension(unknown space) = %v, want ErrEmbedderUnknown", err)
	}
}

func TestEmbedder_when_version_suffix_derives_space_key(t *testing.T) {
	// version 非空：空间键 = model@version；Dimension 按派生键解析。
	c := embeddingConf("http://localhost")
	c.Embedders[0].Version = "v2"
	c.DefaultEmbedder = "test-embedding-model@v2"
	e, cleanup, err := New(c)
	if err != nil {
		t.Fatalf("New(versioned): %v", err)
	}
	defer cleanup()
	dim, err := e.Dimension("test-embedding-model@v2")
	if err != nil || dim != 4 {
		t.Fatalf("Dimension(versioned key) = %d/%v, want 4/nil", dim, err)
	}
	if _, err := e.Dimension("test-embedding-model"); !errors.Is(err, biz.ErrEmbedderUnknown) {
		t.Fatalf("Dimension(bare model key) = %v, want ErrEmbedderUnknown (versioned conf derives versioned key)", err)
	}
}

func TestEmbedder_when_multi_space_routes_to_own_endpoint(t *testing.T) {
	// 双空间并存：请求按空间键路由到各自端点，model/维度随空间走。
	var modelA, modelB string
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelA = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": make([]float64, 4)}}})
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		modelB = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": make([]float64, 8)}}})
	}))
	defer srvB.Close()
	e, cleanup, err := New(&conf.Vector{
		Enabled:         true,
		DefaultEmbedder: "model-a",
		Embedders: []*conf.Vector_Embedding{
			{Model: "model-a", BaseUrl: srvA.URL, Dimension: 4},
			{Model: "model-b", Version: "v2", BaseUrl: srvB.URL, Dimension: 8},
		},
	})
	if err != nil {
		t.Fatalf("New(multi-space): %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "model-a", "text a"); err != nil {
		t.Fatalf("Embed(space a): %v", err)
	}
	if _, err := e.Embed(context.Background(), "model-b@v2", "text b"); err != nil {
		t.Fatalf("Embed(space b): %v", err)
	}
	if !strings.Contains(modelA, `"model":"model-a"`) {
		t.Fatalf("space a request = %s, want model-a", modelA)
	}
	if !strings.Contains(modelB, `"model":"model-b"`) {
		t.Fatalf("space b request = %s, want model-b", modelB)
	}
}

func TestEmbedder_when_space_timeout_expires_request_fails(t *testing.T) {
	// 每空间独立超时的行为验证：慢端点（500ms 才响应）配 50ms 空间超时
	// 必失败，另一空间 2s 超时正常返回——超时随空间持有、互不牵连。
	// 50ms ≪ 500ms，用例确定性成立。
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": make([]float64, 4)}}})
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": make([]float64, 4)}}})
	}))
	defer fast.Close()
	e, cleanup, err := New(&conf.Vector{
		Enabled:         true,
		DefaultEmbedder: "slow-model",
		Embedders: []*conf.Vector_Embedding{
			{Model: "slow-model", BaseUrl: slow.URL, Dimension: 4, Timeout: durationpb.New(50 * time.Millisecond)},
			{Model: "fast-model", BaseUrl: fast.URL, Dimension: 4, Timeout: durationpb.New(2 * time.Second)},
		},
	})
	if err != nil {
		t.Fatalf("New(multi-space with timeouts): %v", err)
	}
	defer cleanup()
	ctx := context.Background()
	if _, err := e.Embed(ctx, "slow-model", "text"); err == nil || !strings.Contains(err.Error(), "call embeddings endpoint") {
		t.Fatalf("Embed on slow endpoint with 50ms timeout = %v, want transport error", err)
	}
	got, err := e.Embed(ctx, "fast-model", "text")
	if err != nil || len(got) != 4 {
		t.Fatalf("Embed on fast endpoint = (%d dims, %v), want 4 dims untouched by the slow space", len(got), err)
	}
}

func TestEmbedder_when_endpoint_errors_mapped(t *testing.T) {
	srv, _ := stubEndpoint(t, nil, http.StatusUnauthorized)
	e, cleanup, err := New(embeddingConf(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := e.Embed(context.Background(), "test-embedding-model", "text"); err == nil || !strings.Contains(err.Error(), "401") {
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
	if _, err := e2.Embed(context.Background(), "test-embedding-model", "text"); err == nil || !strings.Contains(err.Error(), "no data") {
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
	if _, err := e.Embed(context.Background(), "test-embedding-model", "   "); err == nil {
		t.Fatal("Embed(blank) = nil error, want rejection")
	}
}

func TestEmbedder_when_timeout_unset_constructs_with_default(t *testing.T) {
	// 缺省超时落位由构造成功表达；具体传输行为由 http.Client 语义保证。
	c := embeddingConf("http://127.0.0.1:1")
	c.Embedders[0].Timeout = nil
	e, cleanup, err := New(c)
	if err != nil {
		t.Fatalf("New without timeout = %v, want default fallback", err)
	}
	defer cleanup()
	if _, err := e.Dimension("test-embedding-model"); err != nil {
		t.Fatalf("Dimension after construction = %v, want nil", err)
	}
}
