package agent

// eventWindow 直测：事件窗口端点是自建端点面，参数边界、身份 fail-closed、
// userKey 归属与原文投影必须显式覆盖，不依赖挂载链上的间接断言（注册顺
// 序与认证链语义由 internal/server 的守卫测试锁定）。

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// eventWindowTestAppName 是直测装配的 app 名：只需与播种用的框架 Key 一致。
const eventWindowTestAppName = "grape-agent"

// eventWindowTestIdentity 是归属者身份（编码为 userKey t-1:7）。
var eventWindowTestIdentity = biz.Identity{TenantID: "t-1", UserID: 7}

// eventWindowErrorSessions 是可注入故障的会话存储替身：实现 WindowService
// 的存储按 errWindowOverride 返回，未实现的变体经构造差异覆盖。
type eventWindowErrorSessions struct {
	session.Service
	err error
}

func (s eventWindowErrorSessions) GetEventWindow(context.Context, session.EventWindowRequest) (*session.EventWindow, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &session.EventWindow{}, nil
}

// eventWindowPlainSessions 是未实现 WindowService 的会话存储替身（装配
// 缺陷分支用）：只内嵌接口，其余方法经 nil 兜底（直测路径不可达）。
type eventWindowPlainSessions struct {
	session.Service
}

// newEventWindowFixture 组装直测件：真实 inmemory 会话存储（实现
// WindowService）+ 窗口装配物。
func newEventWindowFixture(t *testing.T) (*eventWindow, *inmemory.SessionService) {
	t.Helper()
	svc, closeSvc, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSvc)
	memory, ok := svc.(*inmemory.SessionService)
	if !ok {
		t.Fatalf("session service type = %T, want *inmemory.SessionService", svc)
	}
	return newEventWindow(memory, eventWindowTestAppName), memory
}

// seedEventWindowEvents 以指定 userKey 播种一个会话并追加三条事件（用户
// 消息 → 助手消息含工具调用 → 工具结果），返回事件 ID（追加序）。
func seedEventWindowEvents(t *testing.T, memory *inmemory.SessionService, userKey, sessionID string) []string {
	t.Helper()
	ctx := context.Background()
	sess, err := memory.CreateSession(ctx, session.Key{
		AppName:   eventWindowTestAppName,
		UserID:    userKey,
		SessionID: sessionID,
	}, nil)
	if err != nil {
		t.Fatalf("seed session %s for %s: %v", sessionID, userKey, err)
	}
	userEvent := event.NewResponseEvent("inv-1", "user", &model.Response{
		ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "seed-user-hello"}}},
	})
	assistantEvent := event.NewResponseEvent("inv-1", eventWindowTestAppName, &model.Response{
		ID: "resp-2",
		Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleAssistant,
			Content: "seed-assistant-calling",
			ToolCalls: []model.ToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"text":"seed-tool-args"}`)},
			}},
		}}},
	})
	toolEvent := event.NewResponseEvent("inv-1", eventWindowTestAppName, &model.Response{
		ID: "resp-3",
		Choices: []model.Choice{{Message: model.Message{
			Role:     model.RoleTool,
			Content:  "seed-tool-raw-result-marker",
			ToolID:   "call-1",
			ToolName: "echo",
		}}},
	})
	ids := make([]string, 0, 3)
	for _, evt := range []*event.Event{userEvent, assistantEvent, toolEvent} {
		if err := memory.AppendEvent(ctx, sess, evt); err != nil {
			t.Fatalf("append event %s: %v", evt.ID, err)
		}
		ids = append(ids, evt.ID)
	}
	return ids
}

// eventWindowDo 以给定身份（nil = 不注入，模拟绕过认证中间件）执行一次
// 窗口请求。
func eventWindowDo(t *testing.T, h nethttp.Handler, identity *biz.Identity, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodGet, "/agui/event-window"+query, nil)
	if identity != nil {
		req = req.WithContext(biz.WithIdentity(req.Context(), *identity))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// eventWindowEventsOf 解析窗口响应体并守卫时间戳的 UTC RFC3339 渲染契约。
func eventWindowEventsOf(t *testing.T, body string) []eventWindowEventItem {
	t.Helper()
	var reply struct {
		AnchorEventID string                 `json:"anchorEventId"`
		Events        []eventWindowEventItem `json:"events"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("parse event window reply %q: %v", body, err)
	}
	for _, item := range reply.Events {
		if _, err := time.Parse(time.RFC3339, item.CreatedAt); err != nil {
			t.Fatalf("createdAt %q is not RFC3339: %v", item.CreatedAt, err)
		}
	}
	return reply.Events
}

// TestEventWindow_when_params_missing_or_invalid_returns_400 验证参数校验：
// threadId / eventId 必填，before / after 负值、非数字或超上限一律 400
// （fail-closed，不静默截断）。
func TestEventWindow_when_params_missing_or_invalid_returns_400(t *testing.T) {
	h, _ := newEventWindowFixture(t)
	for _, query := range []string{
		"",
		"?threadId=s-1",
		"?eventId=e-1",
		"?threadId=s-1&eventId=e-1&before=-1",
		"?threadId=s-1&eventId=e-1&after=abc",
		"?threadId=s-1&eventId=e-1&before=51",
		"?threadId=s-1&eventId=e-1&after=51",
	} {
		rec := eventWindowDo(t, h, &eventWindowTestIdentity, query)
		if rec.Code != nethttp.StatusBadRequest {
			t.Fatalf("event-window%s status = %d, want 400\n%s", query, rec.Code, rec.Body.String())
		}
	}
}

// TestEventWindow_when_identity_missing_fails_closed_401 验证身份缺失
// （绕过认证中间件的调用）fail-closed：401 且不触会话存储。
func TestEventWindow_when_identity_missing_fails_closed_401(t *testing.T) {
	h, _ := newEventWindowFixture(t)
	rec := eventWindowDo(t, h, nil, "?threadId=s-1&eventId=e-1")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("missing identity status = %d, want 401", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, errMissingIdentityMsg) {
		t.Fatalf("missing identity body = %q, want %q", body, errMissingIdentityMsg)
	}
}

// TestEventWindow_when_identity_unencodable_returns_401 验证编码失败分支：
// 身份存在但 EncodeUserKey 拒绝（非正数用户 ID）时按未认证处理。
func TestEventWindow_when_identity_unencodable_returns_401(t *testing.T) {
	h, _ := newEventWindowFixture(t)
	rec := eventWindowDo(t, h, &biz.Identity{TenantID: "t-1", UserID: 0}, "?threadId=s-1&eventId=e-1")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("unencodable identity status = %d, want 401", rec.Code)
	}
}

// TestEventWindow_when_anchor_foreign_returns_404 验证跨用户不可见：锚点
// 属其他 userKey 名下的会话时存储层找不到该锚点，统一 404（不区分不存在
// 与非你所有），且不泄露他方事件内容。
func TestEventWindow_when_anchor_foreign_returns_404(t *testing.T) {
	h, memory := newEventWindowFixture(t)
	userKey, err := EncodeUserKey("t-1", 8)
	if err != nil {
		t.Fatalf("encode user key: %v", err)
	}
	theirs := seedEventWindowEvents(t, memory, userKey, "foreign-sess")

	rec := eventWindowDo(t, h, &eventWindowTestIdentity, "?threadId=foreign-sess&eventId="+theirs[0])
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("foreign anchor status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "seed-") {
		t.Fatalf("foreign anchor body leaks events: %s", body)
	}
}

// TestEventWindow_when_window_requested_returns_raw_projection 验证窗口
// 语义与原文投影：缺省 span 只回锚点一条；before=2 回窗口（追加序）；
// 事件原文完整（压缩只作用于请求投影副本，事件流保留原文），工具结果
// 事件带 role/toolCallId/toolName。注意框架窗口语义：事件窗口只收载
// 「有意义文本」事件（用户/助手文本、工具结果），助手工具调用载体事件
// 不可作锚点也不入窗（框架 ExtractEventText 排除），投影的工具调用展开
// 对 eventWindowEventOf 直测覆盖。
func TestEventWindow_when_window_requested_returns_raw_projection(t *testing.T) {
	h, memory := newEventWindowFixture(t)
	userKey, err := EncodeUserKey("t-1", 7)
	if err != nil {
		t.Fatalf("encode user key: %v", err)
	}
	ids := seedEventWindowEvents(t, memory, userKey, "own-sess")

	// 缺省 span：只回锚点（工具结果事件）一条。
	rec := eventWindowDo(t, h, &eventWindowTestIdentity, "?threadId=own-sess&eventId="+ids[2])
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("anchor-only status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	events := eventWindowEventsOf(t, rec.Body.String())
	if len(events) != 1 {
		t.Fatalf("anchor-only events = %d, want 1\n%s", len(events), rec.Body.String())
	}
	anchor := events[0]
	if anchor.EventID != ids[2] {
		t.Fatalf("anchor eventId = %q, want %q", anchor.EventID, ids[2])
	}
	if anchor.Content != "seed-tool-raw-result-marker" {
		t.Fatalf("anchor content = %q, want raw tool result preserved", anchor.Content)
	}
	if anchor.Role != "tool" || anchor.ToolCallID != "call-1" || anchor.ToolName != "echo" {
		t.Fatalf("anchor role/tool fields = %q/%q/%q, want tool/call-1/echo", anchor.Role, anchor.ToolCallID, anchor.ToolName)
	}

	// before=2：窗口回用户消息 + 锚点两条（助手工具调用载体事件不入窗），
	// 按追加序排列。
	rec = eventWindowDo(t, h, &eventWindowTestIdentity, "?threadId=own-sess&eventId="+ids[2]+"&before=2&after=0")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("window status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	events = eventWindowEventsOf(t, rec.Body.String())
	if len(events) != 2 {
		t.Fatalf("window events = %d, want 2\n%s", len(events), rec.Body.String())
	}
	wantIDs := []string{ids[0], ids[2]}
	for i, want := range wantIDs {
		if events[i].EventID != want {
			t.Fatalf("window event[%d] = %q, want %q (append order)", i, events[i].EventID, want)
		}
	}
	if events[0].Role != "user" || events[0].Content != "seed-user-hello" {
		t.Fatalf("window event[0] = %+v, want raw user message", events[0])
	}
}

// TestEventWindowEventOf_when_assistant_tool_calls_project_expanded 直测
// 事件投影的助手工具调用展开（框架窗口不收载工具调用载体事件，HTTP 面
// 无法自然覆盖该分支）：ID/名/参数一字投影，多 choice 正文换行拼接。
func TestEventWindowEventOf_when_assistant_tool_calls_project_expanded(t *testing.T) {
	entry := session.EventWindowEntry{
		Event: event.Event{
			ID:     "evt-1",
			Author: eventWindowTestAppName,
			Response: &model.Response{Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "calling",
					ToolCalls: []model.ToolCall{{
						ID:       "call-9",
						Type:     "function",
						Function: model.FunctionDefinitionParam{Name: "echo", Arguments: []byte(`{"text":"args-9"}`)},
					}},
				}},
				{Message: model.Message{Role: model.RoleAssistant, Content: "second-choice"}},
			}},
		},
		CreatedAt: time.Unix(0, 0).UTC(),
	}
	item := eventWindowEventOf(entry)
	if item.EventID != "evt-1" || item.Role != "assistant" {
		t.Fatalf("projected event = %+v, want evt-1/assistant", item)
	}
	if item.Content != "calling\nsecond-choice" {
		t.Fatalf("projected content = %q, want multi-choice join", item.Content)
	}
	if len(item.ToolCalls) != 1 || item.ToolCalls[0].ID != "call-9" || item.ToolCalls[0].Name != "echo" || item.ToolCalls[0].Arguments != `{"text":"args-9"}` {
		t.Fatalf("projected tool calls = %+v, want call-9/echo/args-9", item.ToolCalls)
	}
}

// TestEventWindow_when_storage_errors_returns_internal_error 验证存储故障
// 分支：非锚点错误只进日志，响应为 500 固定文案。
func TestEventWindow_when_storage_errors_returns_internal_error(t *testing.T) {
	h := newEventWindow(eventWindowErrorSessions{err: errors.New("storage unavailable")}, eventWindowTestAppName)
	rec := eventWindowDo(t, h, &eventWindowTestIdentity, "?threadId=s-1&eventId=e-1")
	if rec.Code != nethttp.StatusInternalServerError {
		t.Fatalf("storage error status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "internal error") {
		t.Fatalf("storage error body = %q, want fixed internal error message", body)
	}
}

// TestEventWindow_when_store_lacks_window_service_returns_internal_error
// 验证装配缺陷分支：会话存储未实现 WindowService 时以 500 固定文案拒绝，
// 不 panic、不静默降级。
func TestEventWindow_when_store_lacks_window_service_returns_internal_error(t *testing.T) {
	h := newEventWindow(eventWindowPlainSessions{}, eventWindowTestAppName)
	rec := eventWindowDo(t, h, &eventWindowTestIdentity, "?threadId=s-1&eventId=e-1")
	if rec.Code != nethttp.StatusInternalServerError {
		t.Fatalf("no window service status = %d, want 500", rec.Code)
	}
}
