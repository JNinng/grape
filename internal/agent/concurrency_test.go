package agent

// E3 运行并发门控测试：原语面（占槽/释放/按键隔离/关闭零包装）+ 装配面
// （完整 Stack handler 上在途运行占位、超限 429、他键不受限、关闭时并发
// 不受限、身份缺失 401）。在途运行以「阻塞在确认等待」的 live 流承载
// （与 confirm_live_test 同一推进手法），拒绝路径同步断言（429 即返）。

import (
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/jninng/trpc_agent_go_ack/acktest"
)

// concurrencyStackConf 组装限流测试的骨架配置：maxConcurrentRuns 逐用例
// 注入（0 = 门控关闭形态）。
func concurrencyStackConf(maxRuns int32) *conf.Agent {
	c := confirmStackConf()
	c.Agui.MaxConcurrentRuns = maxRuns
	return c
}

// newConcurrencyTestStack 按给定并发上限装配确认流测试栈（内存会话存储
// + 注入模型 + 内存用户仓储，handler 外包归属者身份注入）。
func newConcurrencyTestStack(t *testing.T, maxRuns int32, llm *acktest.ScriptedModel, repo *fakeUserRepo) *confirmTestStack {
	t.Helper()
	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	stack, err := NewStackWithOptions(concurrencyStackConf(maxRuns), nil, svc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &searchTestEmbedder{dims: 4}, WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	s := &confirmTestStack{repo: repo, raw: stack.Handler()}
	s.h = s.asUser(confirmTestOwnerID)
	return s
}

// TestRunGate_when_slot_released_next_acquire_succeeds 验证原语面：占槽
// 到上限即拒、释放后可再占、不同身份的配额相互独立。
func TestRunGate_when_slot_released_next_acquire_succeeds(t *testing.T) {
	g := newRunGate(2)
	if g == nil {
		t.Fatal("gate must be constructed for positive limit")
	}
	if !g.acquire("t-1:1") || !g.acquire("t-1:1") {
		t.Fatal("first two acquires for same key must succeed")
	}
	if g.acquire("t-1:1") {
		t.Fatal("third acquire at limit must be rejected")
	}
	if !g.acquire("t-1:2") {
		t.Fatal("other key must have its own quota")
	}
	g.release("t-1:1")
	if !g.acquire("t-1:1") {
		t.Fatal("acquire after release must succeed")
	}
}

// TestRunGate_when_release_drops_last_slot_key_removed 验证计数器不随
// 用户数无界增长：末槽释放后键被删除。
func TestRunGate_when_release_drops_last_slot_key_removed(t *testing.T) {
	g := newRunGate(1)
	g.acquire("t-1:9")
	g.release("t-1:9")
	g.mu.Lock()
	remaining := len(g.inflight)
	g.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("inflight entries = %d, want 0 (key must be dropped)", remaining)
	}
}

// TestRunGate_when_disabled_returns_nil_gate 验证关闭形态：非正上限不
// 构造门控（wrap 直接透传原 handler，零包装）。
func TestRunGate_when_disabled_returns_nil_gate(t *testing.T) {
	for _, v := range []int32{0, -1} {
		if g := newRunGate(v); g != nil {
			t.Fatalf("newRunGate(%d) = %v, want nil (disabled)", v, g)
		}
	}
}

// TestRunGate_when_in_flight_run_holds_slot_same_user_next_chat_rejected
// 验证装配面超限拒绝：上限 1 时在途运行占住槽位，同身份第二个 chat 运行
// 请求同步得到 429 与固定文案；决定结算、运行收尾释放槽位后，后续运行
// 不再被拒（脚本耗尽走 RUN_ERROR 也是 200 SSE——只断言非 429）。
func TestRunGate_when_in_flight_run_holds_slot_same_user_next_chat_rejected(t *testing.T) {
	const (
		threadID = "thread-gate-reject"
		runID    = "run-gate-reject"
		callID   = "call-gate-reject"
		nickname = "gate-nick"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConcurrencyTestStack(t, 1, nicknameCallScript(callID, nickname, "nickname updated"), repo)

	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	waitForPending(t, s.h, threadID, 1)

	body := fmt.Sprintf(`{"threadId":%q,"runId":%q,"messages":[{"id":"m-2","role":"user","content":"again"}]}`, threadID, runID+"-2")
	rejected := postJSON(t, s.h, nethttp.MethodPost, "/agui/", body)
	if rejected.Code != nethttp.StatusTooManyRequests {
		t.Fatalf("second run status = %d, want 429\n%s", rejected.Code, rejected.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rejected.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse rejection body: %v\n%s", err, rejected.Body.String())
	}
	if payload["message"] != runLimitExceededMsg {
		t.Fatalf("rejection message = %q, want %q", payload["message"], runLimitExceededMsg)
	}

	decideRec := decide(t, s.h, threadID, callID, true)
	if decideRec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)

	after := postJSON(t, s.h, nethttp.MethodPost, "/agui/",
		fmt.Sprintf(`{"threadId":%q,"runId":%q,"messages":[{"id":"m-3","role":"user","content":"third"}]}`, threadID, runID+"-3"))
	if after.Code == nethttp.StatusTooManyRequests {
		t.Fatalf("run after release must not be rejected\n%s", after.Body.String())
	}
}

// TestRunGate_when_limit_held_other_user_run_accepted 验证按键隔离的装配
// 面：同一上限被他身份占住时，另一身份的运行正常受理。受理的证明形态：
// 新会话的运行进入框架并注册自己的待确认项（脚本模型按对话内 tool 消息
// 数选步，新会话首步重放工具调用）——被 429 拦下的运行到不了框架，永不
// 注册待确认；两路运行随后各自确定性结算收尾。
func TestRunGate_when_limit_held_other_user_run_accepted(t *testing.T) {
	const (
		threadID      = "thread-gate-other"
		runID         = "run-gate-other"
		callID        = "call-gate-other"
		otherThreadID = "thread-gate-other-b"
		otherRunID    = "run-gate-other-b"
		nickname      = "gate-other-nick"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConcurrencyTestStack(t, 1, nicknameCallScript(callID, nickname, "nickname updated"), repo)

	// 归属者发起运行并阻塞在确认等待（占住其配额）。
	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	waitForPending(t, s.h, threadID, 1)

	// 陌生身份的运行不受他键配额影响：受理并注册自己的待确认项。
	stranger := identityHandler(s.raw, confirmTestTenant, confirmTestStrangerID)
	otherChat := startChat(stranger, otherThreadID, otherRunID, "rename me too")
	waitForPending(t, stranger, otherThreadID, 1)

	otherDecide := decide(t, stranger, otherThreadID, callID, true)
	if otherDecide.Code != nethttp.StatusOK {
		t.Fatalf("stranger decide status = %d, want 200\n%s", otherDecide.Code, otherDecide.Body.String())
	}
	decideRec := decide(t, s.h, threadID, callID, true)
	if decideRec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)
	chatEvents(t, otherChat)
}

// TestRunGate_when_disabled_concurrent_runs_not_limited 验证关闭形态的
// 装配面：上限未配置（默认 0）时同身份并发运行不受限——第二个 chat 正常
// 受理（证明形态同上：待确认注册即受理）。同 thread 的并发运行被框架以
// 409 拒绝，与本门控无关，故第二运行用独立 thread。
func TestRunGate_when_disabled_concurrent_runs_not_limited(t *testing.T) {
	const (
		threadID      = "thread-gate-off"
		runID         = "run-gate-off"
		callID        = "call-gate-off"
		otherThreadID = "thread-gate-off-b"
		otherRunID    = "run-gate-off-b"
		nickname      = "gate-off-nick"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConcurrencyTestStack(t, 0, nicknameCallScript(callID, nickname, "nickname updated"), repo)

	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	waitForPending(t, s.h, threadID, 1)

	secondChat := startChat(s.h, otherThreadID, otherRunID, "second run")
	waitForPending(t, s.h, otherThreadID, 1)

	secondDecide := decide(t, s.h, otherThreadID, callID, true)
	if secondDecide.Code != nethttp.StatusOK {
		t.Fatalf("second run decide status = %d, want 200\n%s", secondDecide.Code, secondDecide.Body.String())
	}
	decideRec := decide(t, s.h, threadID, callID, true)
	if decideRec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)
	chatEvents(t, secondChat)
}

// TestRunGate_when_identity_missing_chat_rejected_unauthorized 验证
// fail-closed：门控开启时身份缺失（绕过认证中间件的路径）的运行请求
// 401，不占槽、不触达框架。
func TestRunGate_when_identity_missing_chat_rejected_unauthorized(t *testing.T) {
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConcurrencyTestStack(t, 1, nicknameCallScript("call-gate-401", "nick", "done"), repo)

	body := `{"threadId":"thread-gate-401","runId":"run-gate-401","messages":[{"id":"m-1","role":"user","content":"hi"}]}`
	req := httptest.NewRequest(nethttp.MethodPost, "/agui/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.raw.ServeHTTP(rec, req)
	if rec.Code != nethttp.StatusUnauthorized {
		t.Fatalf("identity-free run status = %d, want 401\n%s", rec.Code, rec.Body.String())
	}
}
