package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/agent"
	"grape/internal/biz"
	"grape/internal/conf"
	entrymw "grape/internal/middleware"
	"grape/internal/ops"
	"grape/internal/service"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"google.golang.org/protobuf/types/known/durationpb"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// agentTestTimeout 驱动整条用例的守门时限，防止 SSE 用例挂死。
const agentTestTimeout = 30 * time.Second

// agentFakeSessionRepo 为认证中间件提供恒命中的会话事实：任一令牌都
// 解析为租户 t-1 用户 7 的身份。
type agentFakeSessionRepo struct{}

func (*agentFakeSessionRepo) Issue(context.Context, biz.Identity, *biz.IssueSessionInput) (*biz.Session, error) {
	return nil, biz.ErrSessionInvalidArgument
}

func (*agentFakeSessionRepo) Validate(context.Context, string) (*biz.Session, error) {
	return &biz.Session{
		ID:        1,
		Status:    1,
		UserID:    7,
		TenantID:  "t-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}, nil
}

func (*agentFakeSessionRepo) Revoke(context.Context, biz.Identity, int64) error {
	return biz.ErrSessionInvalidArgument
}

func (*agentFakeSessionRepo) RevokeByAccessTokenHash(context.Context, string) error {
	return biz.ErrSessionInvalidArgument
}

func (*agentFakeSessionRepo) Refresh(context.Context, *biz.RefreshSessionInput) (*biz.Session, error) {
	return nil, biz.ErrSessionInvalid
}

// newAgentTestServer 用替身模型装配完整 HTTP 服务器：真实 trace / auth /
// 路由级超时中间件链与真实挂载，越界与超时治理在链上验证。返回服务器
// 与资源释放函数。
func newAgentTestServer(t *testing.T, confServer *conf.Server, llm model.Model) (*kratoshttp.Server, func()) {
	t.Helper()
	svc, closeSessions, err := agent.NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	stack, err := agent.NewStackWithOptions(agentConf(), nil, svc, agent.WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build agent stack: %v", err)
	}
	agentService := service.NewAgentService(stack)
	traceMiddleware, err := entrymw.NewTraceMiddleware(confServer)
	if err != nil {
		t.Fatalf("build trace middleware: %v", err)
	}
	authMiddleware := entrymw.NewAuthMiddleware(biz.NewSessionUsecase(&agentFakeSessionRepo{}))
	srv := NewHTTPServer(confServer, traceMiddleware,
		NewRouteTimeoutMiddleware(confServer, agentService),
		authMiddleware, ops.NewMetricsHandler(nil), nil, nil, agentService)
	closeAll := func() {
		stack.Close()
		closeSessions()
	}
	t.Cleanup(closeAll)
	return srv, closeAll
}

// agentConf 提供骨架装配的 agent 配置（model 仅满足装配校验，
// 替身模型经 WithModelOverride 注入时不会被调用）。
func agentConf() *conf.Agent {
	return &conf.Agent{
		AppName: "grape-agent",
		Llm:     &conf.Agent_LLM{Model: "stub"},
		Agui: &conf.Agent_AGUI{
			BasePath:          "/agui",
			Timeout:           durationpb.New(agentTestTimeout),
			HeartbeatInterval: durationpb.New(15 * time.Second),
		},
	}
}

// agentHTTPConf 提供与 configs/config.yaml 相同口径的服务器配置：
// 传输层超时归零，普通路由由 request_timeout 恢复限时。
func agentHTTPConf() *conf.Server {
	return &conf.Server{
		Http: &conf.Server_HTTP{
			Timeout:        durationpb.New(0),
			RequestTimeout: durationpb.New(time.Second),
		},
	}
}

// agentChatRequest 构造 AG-UI chat 请求体。
func agentChatRequest(threadID string) string {
	return fmt.Sprintf(`{"threadId":%q,"runId":"run-1","messages":[{"id":"m-1","role":"user","content":"say ping"}]}`, threadID)
}

// agentDo 执行一次带认证的请求并返回响应。
func agentDo(t *testing.T, srv *kratoshttp.Server, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// agentEventTypes 解析 SSE 响应体中的事件类型序列。
func agentEventTypes(t *testing.T, body string) []string {
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

// agentHasEvent 断言事件序列包含指定类型。
func agentHasEvent(t *testing.T, types []string, want string) {
	t.Helper()
	for _, typ := range types {
		if typ == want {
			return
		}
	}
	t.Fatalf("event types %v do not contain %q", types, want)
}

// TestAgentChatWithoutTokenReturns401 守卫 AG-UI 路由的认证链：无令牌
// 请求经完整挂载链必须被认证中间件拒绝。
func TestAgentChatWithoutTokenReturns401(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodPost, "/agui/", "", agentChatRequest("thread-1"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("chat without token: status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentAckConfirmationWithoutTokenReturns401 守卫 ack 决定路由的
// 认证链：ackhttp 自身不做鉴权，鉴权必须由外层链完成。
func TestAgentAckConfirmationWithoutTokenReturns401(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodPost, "/agui/ack/confirmation", "",
		`{"threadId":"thread-1","confirmationId":"c-1","approved":true}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ack confirmation without token: status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentAckConfirmationsWithoutTokenReturns401 守卫 ack 查询路由的
// 认证链。
func TestAgentAckConfirmationsWithoutTokenReturns401(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodGet, "/agui/ack/confirmations?threadId=thread-1", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ack confirmations without token: status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}

// TestAgentRouteWithoutTrailingSlashRedirectsToBase 验证无尾斜杠形式
// 进入内层 ServeMux 后以 307（保方法）规整到前缀根（TODO §1.5 挂载形态）。
func TestAgentRouteWithoutTrailingSlashRedirectsToBase(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodPost, "/agui", "token-1", agentChatRequest("thread-1"))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("no-slash /agui: status = %d, want 307\n%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/agui/" {
		t.Fatalf("redirect location = %q, want /agui/", loc)
	}
}

// TestAgentChatEchoToolRoundTripStreamsToolResult 验证 echo 工具端到端：
// 脚本化模型先发起 echo 工具调用再回终答，SSE 流应包含完整生命周期
// 事件且工具结果出现。
func TestAgentChatEchoToolRoundTripStreamsToolResult(t *testing.T) {
	llm := acktest.NewScriptedModel("stub",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{
			ID: "call-1", Name: "echo", Args: `{"text":"ping"}`,
		}}},
		acktest.Step{Text: "echo said ping"},
	)
	srv, _ := newAgentTestServer(t, agentHTTPConf(), llm)
	rec := agentDo(t, srv, http.MethodPost, "/agui/", "token-1", agentChatRequest("thread-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	types := agentEventTypes(t, rec.Body.String())
	agentHasEvent(t, types, "RUN_STARTED")
	agentHasEvent(t, types, "TOOL_CALL_START")
	agentHasEvent(t, types, "TOOL_CALL_RESULT")
	agentHasEvent(t, types, "RUN_FINISHED")
	if !strings.Contains(rec.Body.String(), "ping") {
		t.Fatalf("tool result content missing echo payload:\n%s", rec.Body.String())
	}
}

// delayingModel 是延迟分片发送的替身模型：模拟慢 LLM，分片间隔跨越
// 请求超时阈值，验证治理后 SSE 不被截断。
type delayingModel struct {
	delays []time.Duration // 第 i 片发出前的延迟（第 0 片立即）
	chunks []string
}

func (*delayingModel) Info() model.Info { return model.Info{Name: "delaying"} }

func (m *delayingModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	stop := "stop"
	full := strings.Join(m.chunks, "")
	ch := make(chan *model.Response, len(m.chunks)+1)
	go func() {
		defer close(ch)
		// 分片形态对齐框架 openai 适配器：chunk 增量 + 汇总终帧。
		for i, text := range m.chunks {
			if m.delays[i] > 0 {
				time.Sleep(m.delays[i])
			}
			ch <- &model.Response{
				ID: "delaying-1", Object: model.ObjectTypeChatCompletionChunk, IsPartial: true,
				Choices: []model.Choice{{Index: 0, Delta: model.Message{Role: model.RoleAssistant, Content: text}}},
			}
		}
		ch <- &model.Response{
			ID: "delaying-1", Object: model.ObjectTypeChatCompletion, Done: true,
			Choices: []model.Choice{{
				Index:        0,
				Message:      model.Message{Role: model.RoleAssistant, Content: full},
				FinishReason: &stop,
			}},
		}
	}()
	return ch, nil
}

// TestAgentChatStreamSurvivesBeyondRequestTimeout 验证 SSE 超时治理：
// 传输层超时归零后，分片间隔超过 1s（原全局超时值）的长流仍完整送达，
// 末片与 RUN_FINISHED 均在流内；若路由级超时错误作用于 agent 前缀，
// 本用例会在 1s 处截断而失败。
func TestAgentChatStreamSurvivesBeyondRequestTimeout(t *testing.T) {
	llm := &delayingModel{
		delays: []time.Duration{0, 1200 * time.Millisecond, 100 * time.Millisecond},
		chunks: []string{"early-", "late-", "tail"},
	}
	srv, _ := newAgentTestServer(t, agentHTTPConf(), llm)
	rec := agentDo(t, srv, http.MethodPost, "/agui/", "token-1", agentChatRequest("thread-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 超时点（1s）之后才发出的两个分片都必须出现在流内；事件按分片
	// 逐条下发，故逐片断言而非拼接子串。
	for _, late := range []string{"late-", "tail"} {
		if !strings.Contains(body, late) {
			t.Fatalf("stream truncated, chunk %q missing:\n%s", late, body)
		}
	}
	types := agentEventTypes(t, body)
	agentHasEvent(t, types, "TEXT_MESSAGE_CONTENT")
	agentHasEvent(t, types, "RUN_FINISHED")
}

// TestAgentChatResponseCarriesTraceID 验证 trace-id 响应头在 SSE 路由
// 上回传（链路中间件在进入 handler 前写头，SSE 客户端可见）。
func TestAgentChatResponseCarriesTraceID(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodPost, "/agui/", "token-1", agentChatRequest("thread-1"))
	if id := rec.Header().Get("x-trace-id"); id == "" {
		t.Fatalf("chat response missing x-trace-id header")
	}
}

// TestAgentAckResponseCarriesTraceID 验证 ack 路由（含 401 路径）同样
// 回传 trace-id：链路中间件先于认证执行，错误响应不丢链路事实。
func TestAgentAckResponseCarriesTraceID(t *testing.T) {
	srv, _ := newAgentTestServer(t, agentHTTPConf(), acktest.NewScriptedModel("stub"))
	rec := agentDo(t, srv, http.MethodGet, "/agui/ack/confirmations?threadId=thread-1", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ack confirmations without token: status = %d, want 401", rec.Code)
	}
	if id := rec.Header().Get("x-trace-id"); id == "" {
		t.Fatalf("ack response missing x-trace-id header")
	}
}
