package httpapi

// SessionUsage 直测：参数边界、身份 fail-closed、userKey 归属、按响应 ID
// 去重与 token/耗时投影；注册顺序与认证链语义由 internal/server 的守卫
// 测试覆盖。

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/biz"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// newSessionUsageFixture 组装直测件：真实 inmemory 会话存储 + 用量装配物。
func newSessionUsageFixture(t *testing.T) (*SessionUsage, *inmemory.SessionService) {
	t.Helper()
	svc, closeSvc, err := testsupport.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSvc)
	memory, ok := svc.(*inmemory.SessionService)
	if !ok {
		t.Fatalf("session service type = %T, want *inmemory.SessionService", svc)
	}
	return NewSessionUsage(memory, eventWindowTestAppName), memory
}

// seedSessionUsageEvents 以指定 userKey 播种会话并追加两次模型调用的
// 事件：第一次只带模型与用量，随后一条同响应 ID 的工具结果事件（用量
// 重复）验证去重；第二次带思考与首字耗时。
func seedSessionUsageEvents(t *testing.T, memory *inmemory.SessionService, userKey, sessionID string) {
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
	firstUsage := &model.Usage{
		PromptTokens:            1200,
		CompletionTokens:        80,
		TotalTokens:             1280,
		PromptTokensDetails:     model.PromptTokensDetails{CachedTokens: 1100},
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 60},
		TimingInfo: &model.TimingInfo{
			FirstTokenDuration: 150 * time.Millisecond,
			ReasoningDuration:  2600 * time.Millisecond,
		},
	}
	events := []*event.Event{
		event.NewResponseEvent("inv-1", "user", &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "有哪些库"}}},
		}),
		event.NewResponseEvent("inv-1", eventWindowTestAppName, &model.Response{
			ID: "chatcmpl-1", Model: "qwen3:8b", Usage: firstUsage,
			Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call-1",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name: "list_knowledge_libraries", Arguments: []byte(`{}`),
					},
				}},
			}}},
		}),
		event.NewResponseEvent("inv-1", eventWindowTestAppName, &model.Response{
			ID: "chatcmpl-1", Model: "qwen3:8b", Usage: firstUsage,
			Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleTool, Content: "tool-result", ToolID: "call-1", ToolName: "echo",
			}}},
		}),
		event.NewResponseEvent("inv-1", eventWindowTestAppName, &model.Response{
			ID: "chatcmpl-2", Model: "qwen3:8b",
			Usage: &model.Usage{PromptTokens: 1300, CompletionTokens: 60, TotalTokens: 1360},
			Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant, Content: "final answer",
			}}},
		}),
	}
	for _, evt := range events {
		if err := memory.AppendEvent(ctx, sess, evt); err != nil {
			t.Fatalf("append event %s: %v", evt.ID, err)
		}
	}
}

// sessionUsageDo 以给定身份（nil = 不注入）执行一次用量请求。
func sessionUsageDo(t *testing.T, h nethttp.Handler, identity *biz.Identity, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(nethttp.MethodGet, "/agui/session-usage"+query, nil)
	if identity != nil {
		req = req.WithContext(biz.WithIdentity(req.Context(), *identity))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sessionUsageFactsOf 解析响应体并守卫时间戳的 UTC RFC3339 渲染契约。
func sessionUsageFactsOf(t *testing.T, body string) []sessionUsageFact {
	t.Helper()
	var reply sessionUsageReply
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("parse session usage reply %q: %v", body, err)
	}
	for _, fact := range reply.Facts {
		if _, err := time.Parse(time.RFC3339, fact.CreatedAt); err != nil {
			t.Fatalf("createdAt %q is not RFC3339: %v", fact.CreatedAt, err)
		}
	}
	return reply.Facts
}

// TestSessionUsage_when_thread_id_missing_returns_400 验证 threadId 必填。
func TestSessionUsage_when_thread_id_missing_returns_400(t *testing.T) {
	h, _ := newSessionUsageFixture(t)
	rec := sessionUsageDo(t, h, &eventWindowTestIdentity, "")
	if rec.Code != nethttp.StatusBadRequest {
		t.Fatalf("missing threadId status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
}

// TestSessionUsage_when_identity_missing_fails_closed_401 验证身份缺失
// fail-closed：401 且不触会话存储。
func TestSessionUsage_when_identity_missing_fails_closed_401(t *testing.T) {
	h, _ := newSessionUsageFixture(t)
	rec := sessionUsageDo(t, h, nil, "?threadId=s-1")
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("missing identity status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestSessionUsage_when_session_absent_or_foreign_returns_404 验证会话不
// 存在与跨用户两种情况统一 404，不泄露存在性。
func TestSessionUsage_when_session_absent_or_foreign_returns_404(t *testing.T) {
	h, memory := newSessionUsageFixture(t)
	seedSessionUsageEvents(t, memory, "t-1:7", "s-1")

	if rec := sessionUsageDo(t, h, &eventWindowTestIdentity, "?threadId=s-missing"); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	foreign := biz.Identity{TenantID: "t-1", UserID: 8}
	if rec := sessionUsageDo(t, h, &foreign, "?threadId=s-1"); rec.Code != nethttp.StatusNotFound {
		t.Fatalf("foreign session status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
}

// TestSessionUsage_projects_model_tokens_and_timing_per_call 验证投影：
// 按响应 ID 去重（工具结果事件与模型事件共享同一份用量），token 细分与
// 思考/首字耗时按毫秒呈现，事实按事件顺序升序。
func TestSessionUsage_projects_model_tokens_and_timing_per_call(t *testing.T) {
	h, memory := newSessionUsageFixture(t)
	seedSessionUsageEvents(t, memory, "t-1:7", "s-1")

	rec := sessionUsageDo(t, h, &eventWindowTestIdentity, "?threadId=s-1")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	facts := sessionUsageFactsOf(t, rec.Body.String())
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want 2 (deduped by response id): %+v", len(facts), facts)
	}
	first := facts[0]
	if first.MessageID != "chatcmpl-1" || first.Model != "qwen3:8b" {
		t.Fatalf("first fact = %+v, want chatcmpl-1/qwen3:8b", first)
	}
	if first.InvocationID != "inv-1" || facts[1].InvocationID != "inv-1" {
		t.Fatalf("invocation ids = %q/%q, want inv-1 (flow-level timing key)", first.InvocationID, facts[1].InvocationID)
	}
	if first.Usage == nil ||
		first.Usage.Prompt != 1200 || first.Usage.Completion != 80 || first.Usage.Total != 1280 ||
		first.Usage.Cached != 1100 || first.Usage.Reasoning != 60 {
		t.Fatalf("first usage = %+v, want prompt/completion/total/cached/reasoning 1200/80/1280/1100/60", first.Usage)
	}
	if first.ReasoningMs != 2600 || first.FirstTokenMs != 150 {
		t.Fatalf("first timing = reasoning %dms first %dms, want 2600/150", first.ReasoningMs, first.FirstTokenMs)
	}
	if facts[1].MessageID != "chatcmpl-2" || facts[1].ReasoningMs != 0 {
		t.Fatalf("second fact = %+v, want chatcmpl-2 with zero timing", facts[1])
	}
	if body := rec.Body.String(); !strings.Contains(body, `"sessionId":"s-1"`) {
		t.Fatalf("reply lost session id: %s", body)
	}
}
