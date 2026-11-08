package agent

// 确认恢复路径 PG 集成测试：重启后孤儿确认的同意/拒绝恢复语义（同意则
// 代执行并续跑，拒绝则合成 denied 结果）。套件、环境变量与装载口径见
// README「测试」；「重启」以不 Close 旧栈的新栈模拟，理由见 README
// 「迁移暂存」。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/agent/internal/testsupport"
	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// confirmSuite 是本套件专属测试库定位；其余测试库绝不触碰。
var confirmSuite = testsupport.SuiteDatabase{
	EnvVar:         "GRAPE_CONFIRM_TEST_ADMIN_DSN",
	Database:       "grape_agent_confirm_test",
	GrapeUPassword: "grape_u_pwd",
	SkipMessage:    "GRAPE_CONFIRM_TEST_ADMIN_DSN not set, skip confirm recovery integration tests",
}

// confirmSetupDatabase 重建专属测试库并按 TestDeployFiles 全清单部署，返回
// grape_u DSN。
func confirmSetupDatabase(t *testing.T) string {
	t.Helper()
	return testsupport.SetupSuiteDatabase(t, confirmSuite)
}

// confirmSessionService 经生产装配函数构造会话服务（postgres 后端）。
func confirmSessionService(t *testing.T, grapeUDSN string) (session.Service, func()) {
	t.Helper()
	return confirmSessionServiceTTL(t, grapeUDSN, 0)
}

// confirmSessionServiceTTL 同 confirmSessionService，可指定 session TTL。
func confirmSessionServiceTTL(t *testing.T, grapeUDSN string, ttl time.Duration) (session.Service, func()) {
	t.Helper()
	c := &conf.Agent{AppName: testsupport.AppName}
	d := &conf.Data{Database: &conf.Data_Database{Master: &conf.Data_Database_Slot{Source: grapeUDSN}}}
	sessionSvc, cleanup, err := NewSessionService(c, d)
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	return sessionSvc, cleanup
}

// confirmNewStack 以注入模型与用户仓储构建完整栈（生产装配路径）。
func confirmNewStack(t *testing.T, sessionSvc session.Service, llm *acktest.ScriptedModel, repo *testsupport.FakeUserRepo) *Stack {
	t.Helper()
	stack, err := NewStackWithOptions(testsupport.StackConf(), nil, sessionSvc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &testsupport.SearchTestEmbedder{Dims: 4}, &testsupport.SearchTestResolver{Dims: 4}, WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	return stack
}

// historyPollGrace 是单次 /history 采样的宽限期：follow 尾随未在宽限期
// 内闭合时按已写入的部分响应体判定（快照头先于尾随写出）。
const historyPollGrace = 300 * time.Millisecond

// lockedRecorder 是带互斥的 ResponseWriter：部分读取场景下后台 follow
// goroutine 仍在向 recorder 写事件，httptest.ResponseRecorder 自身无
// 同步，读写必须经同一把锁。
type lockedRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func (l *lockedRecorder) Header() http.Header {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.Header()
}

func (l *lockedRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.Write(p)
}

func (l *lockedRecorder) WriteHeader(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.WriteHeader(code)
}

// snapshot 返回状态码与已写入响应体的快照（同一临界区内读取）。
func (l *lockedRecorder) snapshot() (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.Code, l.rec.Body.String()
}

// confirmWaitHistoryPoll 轮询 /history 直到快照包含期望文本：请求在后台
// 发起，宽限期内检查已写入的部分响应体（follow 未闭合时上游同毫秒事件
// 过滤缺陷会让尾随久等），未闭合请求随之搁置。
func confirmWaitHistoryPoll(t *testing.T, h http.Handler, threadID, wantContains string) string {
	t.Helper()
	deadline := time.Now().Add(testsupport.WaitLimit)
	for {
		rec := &lockedRecorder{rec: httptest.NewRecorder()}
		served := make(chan struct{})
		go func() {
			defer close(served)
			body := `{"threadId":"` + threadID + `","runId":"snap-recovery","messages":[]}`
			req := httptest.NewRequest(http.MethodPost, "/agui/history", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(rec, req)
		}()
		select {
		case <-served:
		case <-time.After(historyPollGrace):
		}
		code, body := rec.snapshot()
		if strings.Contains(body, wantContains) {
			// 命中即要求快照请求本身成功：非 200 的响应体不该被当作
			// 有效回放证据。
			if code != http.StatusOK {
				t.Fatalf("history status = %d, want 200\n%s", code, body)
			}
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("history snapshot missing %q after polling (last status %d):\n%s", wantContains, code, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPGConfirm_when_restarted_orphan_approved_executes_and_resumes 验证
// 同意侧恢复：重启后的孤儿确认经决定路由走恢复路径，工具被网关代执行
// （biz 用例真实收到写入），续跑运行在会话中产出终答并可经 /history 回放。
func TestPGConfirm_when_restarted_orphan_approved_executes_and_resumes(t *testing.T) {
	dsn := confirmSetupDatabase(t)
	const (
		threadID = "thread-orphan-approve"
		runID    = "run-orphan-approve"
		callID   = "call-orphan-approve"
		nickname = "orphan-approved-nick"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	// 旧进程：运行阻塞在确认等待（搁置，不关闭——Close 会取消在途运行
	// 并按取消语义清理指针，破坏孤儿场景）。
	svc1, cleanupSvc1 := confirmSessionService(t, dsn)
	defer cleanupSvc1()
	old := confirmNewStack(t, svc1, nicknameCallScript(callID, nickname, "resumed after approval"), repo)
	h1 := testsupport.IdentityHandler(old.Handler(), testsupport.Tenant, testsupport.OwnerID)
	_ = testsupport.StartChat(h1, threadID, runID, "rename me")
	items := testsupport.WaitForPending(t, h1, threadID, 1)

	// 新进程：全新连接池 + 全新栈（同库）。待确认指针持久化在会话状态，
	// 旧网关的等待者不可达——Pending 应报孤儿（live=false）。
	svc2, cleanupSvc2 := confirmSessionService(t, dsn)
	defer cleanupSvc2()
	current := confirmNewStack(t, svc2, nicknameCallScript(callID, nickname, "resumed after approval"), repo)
	h2 := testsupport.IdentityHandler(current.Handler(), testsupport.Tenant, testsupport.OwnerID)

	orphans := testsupport.WaitForPending(t, h2, threadID, 1)
	if orphans[0].ConfirmationID != items[0].ConfirmationID || orphans[0].Live {
		t.Fatalf("orphan pending mismatch: want %s live=false, got %+v",
			items[0].ConfirmationID, orphans[0])
	}

	rec := testsupport.Decide(t, h2, threadID, orphans[0].ConfirmationID, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("orphan decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide path mismatch, want recovery:\n%s", rec.Body.String())
	}

	// 工具被网关代执行，恰好一次、参数正确。
	updates := repo.Nicknames()
	if len(updates) != 1 || updates[0].Nickname != nickname {
		t.Fatalf("recovery must execute the tool once with new nickname, got %+v", updates)
	}
	// 续跑运行收尾：终答随 track 持久化，/history 快照可回放。
	confirmWaitHistoryPoll(t, h2, threadID, "resumed after approval")
	testsupport.WaitForPending(t, h2, threadID, 0)
}

// TestPGConfirm_when_restarted_orphan_denied_synthesizes_denial 验证拒绝
// 侧恢复：孤儿确认被拒绝后工具不执行，网关合成 denied 工具结果拉起续跑，
// 模型在续跑中收尾。
func TestPGConfirm_when_restarted_orphan_denied_synthesizes_denial(t *testing.T) {
	dsn := confirmSetupDatabase(t)
	const (
		threadID = "thread-orphan-deny"
		runID    = "run-orphan-deny"
		callID   = "call-orphan-deny"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	svc1, cleanupSvc1 := confirmSessionService(t, dsn)
	defer cleanupSvc1()
	old := confirmNewStack(t, svc1, nicknameCallScript(callID, "orphan-denied-nick", "kept the old nickname"), repo)
	h1 := testsupport.IdentityHandler(old.Handler(), testsupport.Tenant, testsupport.OwnerID)
	_ = testsupport.StartChat(h1, threadID, runID, "rename me")
	testsupport.WaitForPending(t, h1, threadID, 1)

	svc2, cleanupSvc2 := confirmSessionService(t, dsn)
	defer cleanupSvc2()
	current := confirmNewStack(t, svc2, nicknameCallScript(callID, "orphan-denied-nick", "kept the old nickname"), repo)
	h2 := testsupport.IdentityHandler(current.Handler(), testsupport.Tenant, testsupport.OwnerID)
	orphans := testsupport.WaitForPending(t, h2, threadID, 1)

	rec := testsupport.Decide(t, h2, threadID, orphans[0].ConfirmationID, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("orphan decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide path mismatch, want recovery:\n%s", rec.Body.String())
	}

	if updates := repo.Nicknames(); len(updates) != 0 {
		t.Fatalf("orphan denial must not execute the tool, got %+v", updates)
	}
	confirmWaitHistoryPoll(t, h2, threadID, "kept the old nickname")
	testsupport.WaitForPending(t, h2, threadID, 0)
}
