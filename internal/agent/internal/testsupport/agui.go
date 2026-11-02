package testsupport

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// AG-UI 确认流的 HTTP 面测试助手：背景 chat、待确认轮询、决定提交与
// SSE 事件解析。路径硬编码 /agui 前缀（与 StackConf 的缺省 BasePath
// 一致），跨根包与子包/外部测试包共用。

// StartChat 在后台 goroutine 发起 chat：运行阻塞在确认等待时请求不返回，
// 由决定唤醒后收尾。返回响应记录通道。
func StartChat(h nethttp.Handler, threadID, runID, message string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := fmt.Sprintf(`{"threadId":%q,"runId":%q,"messages":[{"id":"m-%s","role":"user","content":%q}]}`,
			threadID, runID, runID, message)
		req := httptest.NewRequest(nethttp.MethodPost, "/agui/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		done <- rec
	}()
	return done
}

// PendingConfirmation 是 GET /agui/ack/confirmations 的列表项投影。
type PendingConfirmation struct {
	ConfirmationID string `json:"confirmationId"`
	ToolName       string `json:"toolName"`
	Status         string `json:"status"`
	Live           bool   `json:"live"`
}

// ListPending 查询当前身份下的待确认列表。归属守卫对调用者名下不存在的
// 会话返回 404，语义即「对你不可见」——视为空列表（覆盖运行尚未建档的
// 轮询竞态与跨用户不可见两个场景）。
func ListPending(t *testing.T, h nethttp.Handler, threadID string) []PendingConfirmation {
	t.Helper()
	rec := PostJSON(t, h, nethttp.MethodGet, "/agui/ack/confirmations?threadId="+threadID, "")
	if rec.Code == nethttp.StatusNotFound {
		return nil
	}
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list confirmations status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var items []PendingConfirmation
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("parse confirmations: %v\n%s", err, rec.Body.String())
	}
	return items
}

// WaitForPending 轮询直到待确认数量达到期望，返回当前列表。
func WaitForPending(t *testing.T, h nethttp.Handler, threadID string, want int) []PendingConfirmation {
	deadline := time.Now().Add(WaitLimit)
	for {
		items := ListPending(t, h, threadID)
		if len(items) == want {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending confirmations = %d, want %d (timeout)", len(items), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Decide 提交一次确认决定。
func Decide(t *testing.T, h nethttp.Handler, threadID, confirmationID string, approved bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":%t}`, threadID, confirmationID, approved)
	return PostJSON(t, h, nethttp.MethodPost, "/agui/ack/confirmation", body)
}

// ChatEvents 等待后台 chat 收尾并解析事件类型序列。
func ChatEvents(t *testing.T, ch <-chan *httptest.ResponseRecorder) (types []string, body string) {
	t.Helper()
	select {
	case rec := <-ch:
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
		}
		return EventTypesOf(t, rec.Body.String()), rec.Body.String()
	case <-time.After(RunTimeout):
		t.Fatal("chat did not finish within guard timeout")
		return nil, ""
	}
}

// EventTypesOf 解析 SSE 响应体中的事件类型序列。
func EventTypesOf(t *testing.T, body string) []string {
	t.Helper()
	var types []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			t.Fatalf("parse sse event %q: %v", line, err)
		}
		types = append(types, evt.Type)
	}
	return types
}

// CountTypes 统计事件序列中指定类型的出现次数。
func CountTypes(types []string, want string) int {
	n := 0
	for _, typ := range types {
		if typ == want {
			n++
		}
	}
	return n
}

// HasType 断言事件序列包含指定类型。
func HasType(types []string, want string) bool {
	return CountTypes(types, want) > 0
}
