package openaicompleter

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

// stubCatalog 是测试用文本模型目录：返回固定端点事实或注入解析错误
// （与生产解析器「租户未配置文本模型即 fail-closed」同语义）。
type stubCatalog struct {
	spec biz.TextModelSpec
	err  error
}

func (c *stubCatalog) ResolveTextModel(context.Context) (biz.TextModelSpec, error) {
	if c.err != nil {
		return biz.TextModelSpec{}, c.err
	}
	return c.spec, nil
}

// stubEndpoint 起一个记录请求的替身 chat completions 端点，按 content
// 返回固定应答；断言请求形态（路径/方法/model/messages/鉴权头）后由用例
// 消费。
func stubEndpoint(t *testing.T, content string, status int) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": content}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func newCompleterForTest(baseURL string) (biz.Completer, func(), error) {
	return New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", BaseURL: baseURL, Model: "glm-4", APIKey: "test-key", Timeout: time.Second}})
}

func TestCompleter_when_nil_catalog_rejected(t *testing.T) {
	if _, _, err := New(nil); err == nil {
		t.Fatal("New(nil catalog) = nil error, want rejection")
	}
}

func TestCompleter_when_complete_posts_openai_compatible_request(t *testing.T) {
	srv, captured := stubEndpoint(t, "这是上下文前缀。", http.StatusOK)
	c, cleanup, err := newCompleterForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()

	got, err := c.Complete(context.Background(), "请概括这篇文档")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "这是上下文前缀。" {
		t.Fatalf("completion = %q, want endpoint content passthrough", got)
	}
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(*captured, &req); err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	if req.Model != "glm-4" {
		t.Fatalf("model = %q, want resolved text model", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "请概括这篇文档" {
		t.Fatalf("messages = %+v, want single user prompt passthrough", req.Messages)
	}
}

func TestCompleter_when_request_carries_bearer_key_and_chat_path(t *testing.T) {
	var authHeader, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()
	c, cleanup, err := newCompleterForTest(srv.URL + "/")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if authHeader != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer key", authHeader)
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions (base_url trailing slash normalized)", path)
	}
}

func TestCompleter_when_catalog_fails_passthrough_error(t *testing.T) {
	// 模型解析失败（租户未配置文本模型等）按原错误透传，消费方据此降级。
	c, cleanup, err := New(&stubCatalog{err: biz.ErrTextModelUnavailable})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); !errors.Is(err, biz.ErrTextModelUnavailable) {
		t.Fatalf("Complete with unavailable model = %v, want ErrTextModelUnavailable", err)
	}
}

func TestCompleter_when_endpoint_errors_mapped(t *testing.T) {
	srv, _ := stubEndpoint(t, "", http.StatusUnauthorized)
	c, cleanup, err := newCompleterForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Complete on 401 = %v, want status error", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer empty.Close()
	c2, cleanup2, err := newCompleterForTest(empty.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup2()
	if _, err := c2.Complete(context.Background(), "prompt"); err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("Complete on empty choices = %v, want no-choices error", err)
	}
}

func TestCompleter_when_prompt_blank_rejected(t *testing.T) {
	srv, _ := stubEndpoint(t, "ignored", http.StatusOK)
	c, cleanup, err := newCompleterForTest(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "   "); err == nil {
		t.Fatal("Complete(blank) = nil error, want rejection")
	}
}

func TestCompleter_when_spec_missing_base_url_rejected(t *testing.T) {
	c, cleanup, err := New(&stubCatalog{spec: biz.TextModelSpec{Key: "glm-4", Model: "glm-4"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer cleanup()
	if _, err := c.Complete(context.Background(), "prompt"); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("Complete without base_url = %v, want base_url error", err)
	}
}
