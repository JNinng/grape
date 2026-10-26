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
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// agentSessionItemReply 是会话列表响应项的测试侧镜像（与 service 层的
// JSON 形状对齐）。
type agentSessionItemReply struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// newAgentSessionsTestServer 装配含会话列表端点的完整 HTTP 服务器：
// 与 agent 路由共用同一挂载链（真实 trace / auth / 路由级超时中间件），
// 以 agent 路由真实在册的形态守卫注册顺序语义。返回服务器、内存会话
// 存储（供用例播种会话）与资源释放函数。
func newAgentSessionsTestServer(t *testing.T) (*kratoshttp.Server, *inmemory.SessionService, func()) {
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
		agent.NewVectorStore(nil), agent.WithModelOverride(acktest.NewScriptedModel("stub")))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	agentService := service.NewAgentService(stack)
	sessionsService := service.NewAgentSessionsService(svc, agentConf(), agentService)
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	// telemetry 位传 nil：本测试不涉 OTel（nil 接收者按未启用处理）。
	srv, err := NewHTTPServer(confServer, nil, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, agentService),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, agentService,
		sessionsService,
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

// seedAgentSession 以指定用户身份在内存会话存储中播种一个会话；命名
// 空间取测试装配的 agent 应用名（与 AgentSessionsService 构造一致）。
func seedAgentSession(t *testing.T, memory *inmemory.SessionService, userKey, sessionID string) {
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
	if sess == nil {
		t.Fatalf("seed session %s for %s: nil session", sessionID, userKey)
	}
}

// agentSessionsOf 解析会话列表响应体。
func agentSessionsOf(t *testing.T, body string) []agentSessionItemReply {
	t.Helper()
	var reply struct {
		Sessions []agentSessionItemReply `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatalf("parse sessions reply %q: %v", body, err)
	}
	return reply.Sessions
}

// TestAgentSessionsWithoutTokenReturns401 守卫会话列表路由的认证链：
// 无令牌请求经完整挂载链必须被认证中间件拒绝。
func TestAgentSessionsWithoutTokenReturns401(t *testing.T) {
	srv, _, _ := newAgentSessionsTestServer(t)
	rec := agentDo(t, srv, http.MethodGet, "/agui/sessions", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessions without token: status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentSessionsReturnsOnlyCallerSessions 守卫跨用户不可见：同一
// 令牌身份（租户 t-1 用户 7，见 agentFakeSessionRepo）发起的列表请求
// 只返回该 userKey 名下的会话，其他用户的会话绝不出现。归属过滤以
// 服务端认证身份编码的 userKey 为唯一来源。
func TestAgentSessionsReturnsOnlyCallerSessions(t *testing.T) {
	srv, memory, _ := newAgentSessionsTestServer(t)
	seedAgentSession(t, memory, "t-1:7", "mine-1")
	seedAgentSession(t, memory, "t-1:7", "mine-2")
	seedAgentSession(t, memory, "t-1:8", "theirs-1")

	rec := agentDo(t, srv, http.MethodGet, "/agui/sessions", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	sessions := agentSessionsOf(t, rec.Body.String())
	ids := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
	}
	if len(ids) != 2 {
		t.Fatalf("session count = %d (%v), want 2 caller-owned sessions", len(ids), ids)
	}
	for _, want := range []string{"mine-1", "mine-2"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("sessions %v do not contain caller session %q", ids, want)
		}
	}
	for _, id := range ids {
		if strings.Contains(id, "theirs") {
			t.Fatalf("sessions %v leak another user's session %q", ids, id)
		}
	}
}

// TestAgentSessionsPaginationBoundaries 验证分页边界：offset 跳过与
// limit 截断按框架 offset 分页语义生效；offset 越界返回空数组；非法
// 参数（负 offset、非正 limit、超上限、非数字）一律 400 而非静默截断。
func TestAgentSessionsPaginationBoundaries(t *testing.T) {
	srv, memory, _ := newAgentSessionsTestServer(t)
	for i := 0; i < 3; i++ {
		seedAgentSession(t, memory, "t-1:7", "page-"+string(rune('a'+i)))
	}

	// limit 截断：limit=2 只返回 2 条。
	rec := agentDo(t, srv, http.MethodGet, "/agui/sessions?limit=2", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions?limit=2 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if got := len(agentSessionsOf(t, rec.Body.String())); got != 2 {
		t.Fatalf("sessions?limit=2 count = %d, want 2\n%s", got, rec.Body.String())
	}

	// offset 跳过：offset=1 且 limit 上限内返回其余 2 条。
	rec = agentDo(t, srv, http.MethodGet, "/agui/sessions?offset=1&limit=10", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions?offset=1 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if got := len(agentSessionsOf(t, rec.Body.String())); got != 2 {
		t.Fatalf("sessions?offset=1 count = %d, want 2\n%s", got, rec.Body.String())
	}

	// 空页：offset 越界返回空数组而非错误。
	rec = agentDo(t, srv, http.MethodGet, "/agui/sessions?offset=99", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions?offset=99 status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if got := agentSessionsOf(t, rec.Body.String()); len(got) != 0 {
		t.Fatalf("sessions?offset=99 count = %d, want 0\n%s", len(got), rec.Body.String())
	}

	// 非法参数一律 400。
	for _, query := range []string{"?offset=-1", "?limit=0", "?limit=-2", "?limit=101", "?offset=abc"} {
		rec = agentDo(t, srv, http.MethodGet, "/agui/sessions"+query, "token-1", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("sessions%s status = %d, want 400\n%s", query, rec.Code, rec.Body.String())
		}
	}
}

// TestAgentSessionsRouteRegisteredBeforeAgentPrefix 守卫注册顺序语义：
// 会话列表精确路径先于 agent 前缀通配命中本端点（200 + JSON，而非被
// 通配吞成 SSE 或 307），同时 agent 自身路由（/agui 无尾斜杠 307 规整）
// 不受影响。
func TestAgentSessionsRouteRegisteredBeforeAgentPrefix(t *testing.T) {
	srv, _, _ := newAgentSessionsTestServer(t)
	rec := agentDo(t, srv, http.MethodGet, "/agui/sessions", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agui/sessions status = %d, want 200 (swallowed by agent prefix wildcard?)\n%s",
			rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("GET /agui/sessions content-type = %q, want application/json", ct)
	}
	rec = agentDo(t, srv, http.MethodPost, "/agui", "token-1", agentChatRequest("thread-1"))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("no-slash /agui: status = %d, want 307\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentSessionsReplyIsEmptyArrayWithoutSessions 守卫空会话语义：
// 无会话时 sessions 为空数组而非 null。
func TestAgentSessionsReplyIsEmptyArrayWithoutSessions(t *testing.T) {
	srv, _, _ := newAgentSessionsTestServer(t)
	rec := agentDo(t, srv, http.MethodGet, "/agui/sessions", "token-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"sessions":[]`) {
		t.Fatalf("empty sessions body = %q, want empty array", body)
	}
}
