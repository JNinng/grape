package agent

// 确认恢复路径集成测试：以真实 PostgreSQL（本套件专属库 grape_agent_confirm_test）
// 验证「重启后孤儿确认」的完整恢复语义——原进程的运行与网关等待者不复
// 存在，持久化在 agent_session_states 的待确认指针仍在；新栈经 ackhttp
// 决定路由走恢复路径：同意则服务端代执行工具并拉起续跑运行（/history
// 可回放续跑产出），拒绝则合成 denied 结果续跑、工具不执行。
//
// 环境变量门控（未设置时跳过）：
//
//	GRAPE_CONFIRM_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent -run TestPGConfirm -count=1 -v
//
// 装载取舍与会话集成测试（session_pg_test.go）一致：DeployFiles 全清单
// 部署 + 跨包部署锁互斥，绝不触碰其他套件的测试库。
//
// 「重启」的模拟口径：不关闭旧栈（其阻塞运行随测试进程存活，模拟进程
// 被替换前失联的运行现场），在其上以全新连接池 + 全新网关构建新栈——
// 等待者表在进程内存中不可达、待确认指针与会话事件在库中完好，与真实
// 重启后新进程看到的存储状态一致。不能走 stack.Close：core runner 的
// Close 会取消在途运行，ack 按「运行取消」清理待确认指针，孤儿场景即
// 被破坏（这是生产语义：优雅取消不是孤儿）。

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

// confirmSetupDatabase 重建专属测试库并按 DeployFiles 全清单部署，返回
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

// confirmWaitHistoryPoll 轮询 /history 直到快照包含期望文本。请求在后台
// goroutine 发起，宽限期内检查已写入的部分响应体：follow 开启时，快照
// 响应在尾随终止前不闭合，而上游缺陷（track 事件按时间戳严格大于
// 游标过滤，同毫秒事件被跳过）会让尾随一直等到下一个终态事件（如搁置
// 旧运行的 30s 超时 RUN_ERROR）才闭合——这正是前端以「快照轮询 + 部分读取」
// 兜底的场景，此处按同一模式取样。未闭合的
// 请求随之搁置（其尾随 goroutine 随旧运行终态或进程退出消亡）。
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
