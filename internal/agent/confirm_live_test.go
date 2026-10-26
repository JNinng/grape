package agent

// C1 live 确认流集成测试：进程内会话存储 + 脚本化模型 + 内存用户仓储 +
// 完整 Stack handler，覆盖 ack 直通路径（live）的完整 HTTP 契约——写工具
// 触发 tool_confirmation、同意后运行继续且工具真实执行、拒绝后以 denied
// 结算且模型不重试、跨用户决定被归属守卫拒绝、工具迭代上限终止死循环。

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/jninng/trpc_agent_go_ack/acktest"
	"google.golang.org/protobuf/types/known/durationpb"
)

// confirmStackConf 组装确认流测试的骨架配置：短运行超时只影响挂死兜底，
// 用例本身靠显式时限推进。
func confirmStackConf() *conf.Agent {
	return &conf.Agent{
		AppName: confirmTestAppName,
		Llm:     &conf.Agent_LLM{Model: "stub"},
		Agui: &conf.Agent_AGUI{
			BasePath:          "/agui",
			Timeout:           durationpb.New(confirmTestRunTimeout),
			HeartbeatInterval: durationpb.New(100 * time.Millisecond),
		},
	}
}

const (
	confirmTestAppName    = "grape-agent"
	confirmTestTenant     = "t-1"
	confirmTestOwnerID    = int64(7)
	confirmTestStrangerID = int64(8)
	// confirmTestRunTimeout 是单次运行的守门超时：确认流用例在决定到达前
	// 会阻塞运行，该值只保证阻塞不至挂死，用例节奏由测试自身控制。
	confirmTestRunTimeout = 30 * time.Second
	// confirmTestWaitLimit 是轮询等待确认登记/清理的总时限。
	confirmTestWaitLimit = 10 * time.Second
)

// confirmTestStack 聚合一次确认流用例所需的全部组件。
type confirmTestStack struct {
	repo *fakeUserRepo
	// raw 是未包身份的 Stack handler；身份经 asUser 按视角注入。
	raw nethttp.Handler
	// h 是归属者（confirmTestOwnerID）视角的 handler，多数用例直接用。
	h nethttp.Handler
}

// newConfirmTestStack 组装进程内确认流测试栈：内存会话存储 + 注入模型
// + 内存用户仓储，handler 外包身份注入（等价认证中间件命中后的形态，
// 挂载链治理由 internal/server 套件守卫）。
func newConfirmTestStack(t *testing.T, llm *acktest.ScriptedModel, repo *fakeUserRepo) *confirmTestStack {
	t.Helper()
	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	stack, err := NewStackWithOptions(confirmStackConf(), nil, svc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	s := &confirmTestStack{repo: repo, raw: stack.Handler()}
	s.h = s.asUser(confirmTestOwnerID)
	return s
}

// identityHandler 以固定身份包装 handler（等价认证中间件命中后的 ctx）。
func identityHandler(next nethttp.Handler, tenant string, userID int64) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		ctx := biz.WithIdentity(r.Context(), biz.Identity{TenantID: tenant, UserID: userID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// asUser 以指定身份视角复用同一底层 handler：跨用户用例需要两个视角，
// 各自独立包装裸 handler，避免身份覆盖顺序引入假象。
func (s *confirmTestStack) asUser(userID int64) nethttp.Handler {
	return identityHandler(s.raw, confirmTestTenant, userID)
}

// postJSON 执行一次 JSON 请求并返回响应记录。
func postJSON(t *testing.T, h nethttp.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// startChat 在后台 goroutine 发起 chat：运行阻塞在确认等待时请求不返回，
// 由决定唤醒后收尾。返回响应记录通道。
func startChat(h nethttp.Handler, threadID, runID, message string) <-chan *httptest.ResponseRecorder {
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

// pendingConfirmation 是 GET /agui/ack/confirmations 的列表项投影。
type pendingConfirmation struct {
	ConfirmationID string `json:"confirmationId"`
	ToolName       string `json:"toolName"`
	Status         string `json:"status"`
	Live           bool   `json:"live"`
}

// listPending 查询当前身份下的待确认列表。归属守卫对调用者名下不存在的
// 会话返回 404，语义即「对你不可见」——视为空列表（覆盖运行尚未建档的
// 轮询竞态与跨用户不可见两个场景）。
func listPending(t *testing.T, h nethttp.Handler, threadID string) []pendingConfirmation {
	t.Helper()
	rec := postJSON(t, h, nethttp.MethodGet, "/agui/ack/confirmations?threadId="+threadID, "")
	if rec.Code == nethttp.StatusNotFound {
		return nil
	}
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list confirmations status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var items []pendingConfirmation
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("parse confirmations: %v\n%s", err, rec.Body.String())
	}
	return items
}

// waitForPending 轮询直到待确认数量达到期望，返回当前列表。
func waitForPending(t *testing.T, h nethttp.Handler, threadID string, want int) []pendingConfirmation {
	t.Helper()
	deadline := time.Now().Add(confirmTestWaitLimit)
	for {
		items := listPending(t, h, threadID)
		if len(items) == want {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending confirmations = %d, want %d (timeout)", len(items), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// decide 提交一次确认决定。
func decide(t *testing.T, h nethttp.Handler, threadID, confirmationID string, approved bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":%t}`, threadID, confirmationID, approved)
	return postJSON(t, h, nethttp.MethodPost, "/agui/ack/confirmation", body)
}

// chatEvents 等待后台 chat 收尾并解析事件类型序列。
func chatEvents(t *testing.T, ch <-chan *httptest.ResponseRecorder) (types []string, body string) {
	t.Helper()
	select {
	case rec := <-ch:
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("chat status = %d, want 200\n%s", rec.Code, rec.Body.String())
		}
		return eventTypesOf(t, rec.Body.String()), rec.Body.String()
	case <-time.After(confirmTestRunTimeout):
		t.Fatal("chat did not finish within guard timeout")
		return nil, ""
	}
}

// eventTypesOf 解析 SSE 响应体中的事件类型序列。
func eventTypesOf(t *testing.T, body string) []string {
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

// countTypes 统计事件序列中指定类型的出现次数。
func countTypes(types []string, want string) int {
	n := 0
	for _, typ := range types {
		if typ == want {
			n++
		}
	}
	return n
}

func hasType(types []string, want string) bool {
	return countTypes(types, want) > 0
}

// nicknameCallScript 构造两步脚本：先发起改昵称工具调用（ID 固定供决定
// 引用），再回终答。拒绝路径依赖 ScriptedModel 的轮次推进：拒绝的工具
// 结果也是 RoleTool 消息，模型下一轮消费第二步文本，不再发起工具调用。
func nicknameCallScript(callID, nickname, finalText string) *acktest.ScriptedModel {
	return acktest.NewScriptedModel("confirm-script",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{
			ID: callID, Name: updateMyNicknameToolName,
			Args: fmt.Sprintf(`{"nickname":%q}`, nickname),
		}}},
		acktest.Step{Text: finalText},
	)
}

// TestConfirmLive_when_approved_run_continues_and_tool_executes 验证
// live 同意全链路：写工具触发 tool_confirmation 待确认项，同意决定经
// ackhttp 唤醒阻塞运行（live 路径），工具真实执行到 biz 用例，运行以
// 终答收尾且待确认清理。
func TestConfirmLive_when_approved_run_continues_and_tool_executes(t *testing.T) {
	const (
		threadID = "thread-approve"
		runID    = "run-approve"
		callID   = "call-update-1"
		nickname = "approved-nick"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo)

	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	items := waitForPending(t, s.h, threadID, 1)
	if items[0].ToolName != updateMyNicknameToolName || items[0].Status != "pending" || !items[0].Live {
		t.Fatalf("unexpected pending item: %+v", items[0])
	}

	rec := decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"path":"live"`) {
		t.Fatalf("decide path mismatch, want live:\n%s", rec.Body.String())
	}

	types, body := chatEvents(t, chat)
	if !hasType(types, "TOOL_CALL_RESULT") || !hasType(types, "RUN_FINISHED") {
		t.Fatalf("run did not finish with tool result, types: %v", types)
	}
	if !strings.Contains(body, "nickname updated") {
		t.Fatalf("final assistant text missing from stream:\n%s", body)
	}
	updates := repo.updates()
	if len(updates) != 1 || updates[0].Nickname != nickname {
		t.Fatalf("tool must execute exactly once with new nickname, got %+v", updates)
	}
	if left := waitForPending(t, s.h, threadID, 0); len(left) != 0 {
		t.Fatalf("pending not cleared: %+v", left)
	}
}

// TestConfirmLive_when_denied_tool_skips_and_model_does_not_retry 验证
// live 拒绝结算：工具不执行，模型收到 status=denied 的工具结果，不重复
// 发起同一工具调用（事件流中工具调用只出现一次），运行正常收尾。
func TestConfirmLive_when_denied_tool_skips_and_model_does_not_retry(t *testing.T) {
	const (
		threadID = "thread-deny"
		runID    = "run-deny"
		callID   = "call-update-2"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "denied-nick", "ok, keeping the old one"), repo)

	chat := startChat(s.h, threadID, runID, "rename me")
	items := waitForPending(t, s.h, threadID, 1)

	rec := decide(t, s.h, threadID, items[0].ConfirmationID, false)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	types, body := chatEvents(t, chat)
	if !hasType(types, "RUN_FINISHED") {
		t.Fatalf("denied run must still finish normally, types: %v", types)
	}
	if n := countTypes(types, "TOOL_CALL_START"); n != 1 {
		t.Fatalf("model retried the denied tool: %d tool call starts, want 1\ntypes: %v", n, types)
	}
	if !strings.Contains(body, `"status":"denied"`) {
		t.Fatalf("denied tool result not delivered to stream:\n%s", body)
	}
	if updates := repo.updates(); len(updates) != 0 {
		t.Fatalf("tool must not execute after denial, got %+v", updates)
	}
	waitForPending(t, s.h, threadID, 0)
}

// TestConfirmLive_when_other_user_decides_confirmation_rejected 验证跨
// 用户决定被归属守卫拒绝：他人名下不存在该会话（404），原会话的待确认
// 不受影响，归属者仍可正常决定。
func TestConfirmLive_when_other_user_decides_confirmation_rejected(t *testing.T) {
	const (
		threadID = "thread-cross-user"
		runID    = "run-cross-user"
		callID   = "call-update-3"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "cross-nick", "nickname updated"), repo)

	chat := startChat(s.h, threadID, runID, "rename me")
	items := waitForPending(t, s.h, threadID, 1)

	// 他人视角（同租户其他用户）：列表不可见、决定 404。
	stranger := s.asUser(confirmTestStrangerID)
	seen := listPending(t, stranger, threadID)
	if len(seen) != 0 {
		t.Fatalf("stranger must not list owner's confirmations, got %+v", seen)
	}
	rec := decide(t, stranger, threadID, items[0].ConfirmationID, true)
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("stranger decide status = %d, want 404\n%s", rec.Code, rec.Body.String())
	}
	if left := listPending(t, s.h, threadID); len(left) != 1 {
		t.Fatalf("owner's pending confirmation was disturbed: %+v", left)
	}

	// 归属者决定不受影响，链路照常走完。
	rec = decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if rec.Code != nethttp.StatusOK || !strings.Contains(rec.Body.String(), `"path":"live"`) {
		t.Fatalf("owner decide broken: status %d\n%s", rec.Code, rec.Body.String())
	}
	chatEvents(t, chat)
	if updates := repo.updates(); len(updates) != 1 {
		t.Fatalf("tool executed %d times after owner approval, want 1", len(updates))
	}
}

// TestConfirmLive_when_tool_iterations_exhausted_run_terminates 验证
// MaxToolIterations 防风暴：脚本化模型对 allow 层级的 echo 工具死循环
// 调用（脚本耗尽重复末步），运行按上限终止（RUN_ERROR），实际执行的
// 工具轮数恰为上限值。
func TestConfirmLive_when_tool_iterations_exhausted_run_terminates(t *testing.T) {
	const threadID = "thread-loop"
	loop := acktest.NewScriptedModel("loop-model",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: "call-loop", Name: echoToolName, Args: `{"text":"hi"}`}}},
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, loop, repo)

	chat := startChat(s.h, threadID, "run-loop", "loop")
	types, _ := chatEvents(t, chat)

	if n := countTypes(types, "TOOL_CALL_RESULT"); n != defaultMaxToolIter {
		t.Fatalf("tool executions = %d, want exactly %d (MaxToolIterations)", n, defaultMaxToolIter)
	}
	if !hasType(types, "RUN_ERROR") {
		t.Fatalf("exhausted run must terminate with RUN_ERROR, types: %v", types)
	}
}
