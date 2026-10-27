package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"grape/internal/agent"
	"grape/internal/biz"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"
	"grape/web"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// newAgentEventWindowTestServer 装配含事件窗口端点的完整 HTTP 服务器：
// 与 agent 路由共用同一挂载链（真实 trace / auth / 路由级超时中间件），
// 以 agent 路由真实在册的形态守卫注册顺序语义。返回服务器、内存会话
// 存储与资源释放函数。
func newAgentEventWindowTestServer(t *testing.T) (*kratoshttp.Server, *inmemory.SessionService, func()) {
	t.Helper()
	svc, closeSessions, err := agent.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	memory, ok := svc.(*inmemory.SessionService)
	if !ok {
		t.Fatalf("session service type = %T, want *inmemory.SessionService", svc)
	}
	confServer := agentHTTPConf()
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc,
		agent.NewVectorStore(nil), biz.NewUserUsecase(&agentStubUserRepo{}), nil,
		&agentStubEmbedder{}, agent.WithModelOverride(acktest.NewScriptedModel("stub")))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	agentService := service.NewAgentService(stack)
	sessionsService := service.NewAgentSessionsService(stack)
	eventWindowService := service.NewAgentEventWindowService(stack)
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	// telemetry 位传 nil：本测试不涉 OTel（nil 接收者按未启用处理）。
	srv, err := NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, agentService),
		authMiddleware, ops.NewMetricsHandler(nil, nil), nil, nil, agentService,
		sessionsService,
		eventWindowService,
		// 审计与检索留痕查询端点本文件用例不触达，传 nil。
		nil,
		nil,
		WebHandler(newWebFileHandler(web.DistFS(), ReservedAPIPrefixes(agentService))))
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}
	closeAll := func() {
		stack.Close()
		closeSessions()
	}
	t.Cleanup(closeAll)
	return srv, memory, closeAll
}

// seedEventWindowSession 以指定用户身份播种一个会话并追加一条工具结果
// 事件（占位符原文对照的锚点事件形态），返回其事件 ID。
func seedEventWindowSession(t *testing.T, memory *inmemory.SessionService, userKey, sessionID, content string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := memory.CreateSession(ctx, session.Key{
		AppName:   agentConf().GetAppName(),
		UserID:    userKey,
		SessionID: sessionID,
	}, nil)
	if err != nil {
		t.Fatalf("seed session %s for %s: %v", sessionID, userKey, err)
	}
	evt := event.NewResponseEvent("inv-1", agentConf().GetAppName(), &model.Response{
		ID:      "resp-1",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "call-1", ToolName: "echo", Content: content}}},
	})
	if err := memory.AppendEvent(ctx, sess, evt); err != nil {
		t.Fatalf("append event: %v", err)
	}
	return evt.ID
}

// TestAgentEventWindowWithoutTokenReturns401 守卫事件窗口路由的认证链：
// 无令牌请求经完整挂载链必须被认证中间件拒绝。
func TestAgentEventWindowWithoutTokenReturns401(t *testing.T) {
	srv, _, _ := newAgentEventWindowTestServer(t)
	rec := agentDo(t, srv, http.MethodGet, "/agui/event-window?threadId=thread-1&eventId=e-1", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("event window without token: status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentEventWindowRouteRegisteredBeforeAgentPrefix 守卫注册顺序语义：
// 事件窗口精确路径先于 agent 前缀通配命中本端点（200 + JSON，而非被通
// 配吞成 SSE 或 307）。
func TestAgentEventWindowRouteRegisteredBeforeAgentPrefix(t *testing.T) {
	srv, memory, _ := newAgentEventWindowTestServer(t)
	eventID := seedEventWindowSession(t, memory, "t-1:7", "thread-1", "raw-marker")
	rec := agentDo(t, srv, http.MethodGet, "/agui/event-window?threadId=thread-1&eventId="+eventID, "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agui/event-window status = %d, want 200 (swallowed by agent prefix wildcard?)\n%s",
			rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("GET /agui/event-window content-type = %q, want application/json", ct)
	}
}

// TestAgentEventWindowReturnsOnlyCallerOwnedRawContent 守卫跨用户不可见
// 与原文投影：同一令牌身份（租户 t-1 用户 7，见 agentFakeSessionRepo）
// 取自己名下会话的锚点窗口得原文；他方 userKey 名下会话的锚点不可见
// （404），响应不泄露他方事件内容。
func TestAgentEventWindowReturnsOnlyCallerOwnedRawContent(t *testing.T) {
	srv, memory, _ := newAgentEventWindowTestServer(t)
	const (
		threadID = "thread-own"
		content  = "RAW-TOOL-RESULT-original-text"
	)
	mine := seedEventWindowSession(t, memory, "t-1:7", threadID, content)
	theirs := seedEventWindowSession(t, memory, "t-1:8", "thread-foreign", "FOREIGN-OWNER-TEXT")

	rec := agentDo(t, srv, http.MethodGet, "/agui/event-window?threadId="+threadID+"&eventId="+mine, "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("event window status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var reply struct {
		AnchorEventID string `json:"anchorEventId"`
		Events        []struct {
			EventID string `json:"eventId"`
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("parse event window reply: %v\n%s", err, rec.Body.String())
	}
	if reply.AnchorEventID != mine || len(reply.Events) != 1 {
		t.Fatalf("event window reply = %+v, want single anchor window %q", reply, mine)
	}
	if reply.Events[0].Content != content || reply.Events[0].Role != "tool" {
		t.Fatalf("anchor event = %+v, want raw content %q with tool role", reply.Events[0], content)
	}

	rec = agentDo(t, srv, http.MethodGet, "/agui/event-window?threadId=thread-foreign&eventId="+theirs, "token-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign owner event window status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "FOREIGN-OWNER-TEXT") {
		t.Fatalf("foreign owner body leaks events: %s", rec.Body.String())
	}
}

// TestAgentEventWindowInvalidParamsReturn400 守卫参数校验在挂载链上不变：
// 缺 threadId / eventId 与非法 before / after 一律 400（端点自身的
// fail-closed 语义不因入链放宽）。
func TestAgentEventWindowInvalidParamsReturn400(t *testing.T) {
	srv, _, _ := newAgentEventWindowTestServer(t)
	for _, query := range []string{
		"",
		"?threadId=thread-1",
		"?eventId=e-1",
		"?threadId=thread-1&eventId=e-1&before=-1",
		"?threadId=thread-1&eventId=e-1&before=51",
	} {
		rec := agentDo(t, srv, http.MethodGet, "/agui/event-window"+query, "token-1", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("event window%s status = %d, want 400\n%s", query, rec.Code, rec.Body.String())
		}
	}
}
