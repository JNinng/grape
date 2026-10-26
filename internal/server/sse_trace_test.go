package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// gatingModel 是放行门控的替身模型：首个分片等到 release 信号才发，
// 供测试在分片阶段（RUN_STARTED 已写、模型分片未发）观察响应头。
type gatingModel struct {
	release chan struct{}
	once    sync.Once
}

func (*gatingModel) Info() model.Info { return model.Info{Name: "gating"} }

func (m *gatingModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 2)
	go func() {
		<-m.release
		stop := "stop"
		ch <- &model.Response{
			ID: "gating-1", Object: model.ObjectTypeChatCompletionChunk, IsPartial: true,
			Choices: []model.Choice{{Index: 0, Delta: model.Message{Role: model.RoleAssistant, Content: "late"}}},
		}
		ch <- &model.Response{
			ID: "gating-1", Object: model.ObjectTypeChatCompletion, Done: true,
			Choices: []model.Choice{{
				Index:        0,
				Message:      model.Message{Role: model.RoleAssistant, Content: "late"},
				FinishReason: &stop,
			}},
		}
		close(ch)
	}()
	return ch, nil
}

// Release 放行模型分片（幂等，可安全多次调用）。
func (m *gatingModel) Release() { m.once.Do(func() { close(m.release) }) }

// firstWriteRecorder 包装 httptest.ResponseRecorder：首次 Write 置原子
// 标志。中间件链先写响应头再产出首个 SSE 事件，标志置位后读取头映射
// 经该原子量建立 happens-before，测试在流中途读头无数据竞争。
type firstWriteRecorder struct {
	*httptest.ResponseRecorder
	firstWrite atomic.Bool
}

func (r *firstWriteRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(b)
	r.firstWrite.Store(true)
	return n, err
}

// TestAgentChatTraceIDVisibleDuringChunkPhase 验证 x-trace-id 在 SSE
// 分片阶段（流未结束）已可见：RUN_STARTED 已写、首个模型分片被门控
// 未发时，响应头已带 trace-id——SSE 客户端在长流早期即可拿到链路 ID。
func TestAgentChatTraceIDVisibleDuringChunkPhase(t *testing.T) {
	llm := &gatingModel{release: make(chan struct{})}
	defer llm.Release() // 失败路径也放行，防 goroutine 悬挂
	srv, _ := newAgentTestServer(t, agentHTTPConf(), llm)

	rec := &firstWriteRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, "/agui/", strings.NewReader(agentChatRequest("thread-1")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token-1")

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.ServeHTTP(rec, req)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !rec.firstWrite.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !rec.firstWrite.Load() {
		t.Fatal("first SSE write never happened within deadline")
	}
	traceID := rec.Header().Get("x-trace-id")
	if traceID == "" {
		t.Fatal("x-trace-id must be visible during chunk phase (before stream ends)")
	}

	llm.Release()
	select {
	case <-done:
	case <-time.After(agentTestTimeout):
		t.Fatal("SSE request did not finish after release")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "late") {
		t.Fatalf("released chunk missing from stream:\n%s", rec.Body.String())
	}
}
