package agent

// C1 恢复路径集成测试：以真实 PostgreSQL（本套件专属库 grape_agent_c1_test）
// 验证「重启后孤儿确认」的完整恢复语义——原进程的运行与网关等待者不复
// 存在，持久化在 agent_session_states 的待确认指针仍在；新栈经 ackhttp
// 决定路由走恢复路径：同意则服务端代执行工具并拉起续跑运行（/history
// 可回放续跑产出），拒绝则合成 denied 结果续跑、工具不执行。
//
// 环境变量门控（未设置时跳过）：
//
//	GRAPE_C1_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
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
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// c1TestDatabase 是本套件专属测试库；其余测试库绝不触碰。
const c1TestDatabase = "grape_agent_c1_test"

// c1GrapeUPassword 与 role.sql 占位符替换值：grape_u 的既定测试口令。
const c1GrapeUPassword = "grape_u_pwd"

// c1SetupDatabase 重建专属测试库并按 DeployFiles 全清单部署，返回
// grape_u DSN。形态与 B2 会话集成测试一致（单一事实源的部署清单 +
// 跨包互斥锁）。
func c1SetupDatabase(t *testing.T) string {
	t.Helper()
	adminDSN := os.Getenv("GRAPE_C1_TEST_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("GRAPE_C1_TEST_ADMIN_DSN not set, skip confirm recovery integration tests")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+c1TestDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop test database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+c1TestDatabase); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	deployDir, err := pg_sqlc.DeployDir()
	if err != nil {
		t.Fatalf("locate deploy dir: %v", err)
	}
	if err := pg_sqlc.ValidateDeployFiles(deployDir); err != nil {
		t.Fatalf("validate deploy files: %v", err)
	}
	deployCfg := *cfg
	deployCfg.Database = c1TestDatabase
	deployCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	deployConn, err := pgx.ConnectConfig(ctx, &deployCfg)
	if err != nil {
		t.Fatalf("connect deploy: %v", err)
	}
	defer deployConn.Close(ctx)
	// role.sql 的角色目录操作是集群级的，跨测试包并发部署必须互斥。
	release, err := pg_sqlc.AcquireDeployLock(ctx)
	if err != nil {
		t.Fatalf("acquire deploy lock: %v", err)
	}
	defer release()
	for _, name := range pg_sqlc.DeployFiles {
		sql, err := os.ReadFile(deployDir + "/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		deployed := pg_sqlc.ResolveDeployPasswords(string(sql), c1GrapeUPassword, "test_u_pwd")
		if _, err := deployConn.Exec(ctx, deployed); err != nil {
			t.Fatalf("deploy %s: %v", name, err)
		}
	}
	if _, err := deployConn.Exec(ctx,
		"GRANT CONNECT ON DATABASE "+c1TestDatabase+" TO grape_u, test_u"); err != nil {
		t.Fatalf("grant connect: %v", err)
	}

	dsn, err := c1GrapeUDSN(adminDSN)
	if err != nil {
		t.Fatalf("build grape_u dsn: %v", err)
	}
	return dsn
}

// c1GrapeUDSN 把管理员 DSN 改写为 grape_u 连接专属测试库的 DSN。
func c1GrapeUDSN(adminDSN string) (string, error) {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword("grape_u", c1GrapeUPassword)
	u.Path = "/" + c1TestDatabase
	return u.String(), nil
}

// c1SessionService 经生产装配函数构造会话服务（postgres 后端）。
func c1SessionService(t *testing.T, grapeUDSN string) (session.Service, func()) {
	t.Helper()
	return c1SessionServiceTTL(t, grapeUDSN, 0)
}

// c1SessionServiceTTL 同 c1SessionService，可指定 session TTL。
func c1SessionServiceTTL(t *testing.T, grapeUDSN string, ttl time.Duration) (session.Service, func()) {
	t.Helper()
	c := &conf.Agent{AppName: confirmTestAppName}
	d := &conf.Data{Database: &conf.Data_Database{Master: &conf.Data_Database_Slot{Source: grapeUDSN}}}
	sessionSvc, cleanup, err := NewSessionService(c, d)
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	return sessionSvc, cleanup
}

// c1NewStack 以注入模型与用户仓储构建完整栈（生产装配路径）。
func c1NewStack(t *testing.T, sessionSvc session.Service, llm *acktest.ScriptedModel, repo *fakeUserRepo) *Stack {
	t.Helper()
	stack, err := NewStackWithOptions(confirmStackConf(), nil, sessionSvc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, WithModelOverride(llm))
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

// c1WaitHistoryPoll 轮询 /history 直到快照包含期望文本。请求在后台
// goroutine 发起，宽限期内检查已写入的部分响应体：follow 开启时，快照
// 响应在尾随终止前不闭合，而上游 D4 缺陷（track 事件按时间戳严格大于
// 游标过滤，同毫秒事件被跳过）会让尾随一直等到下一个终态事件（如搁置
// 旧运行的 30s 超时 RUN_ERROR）才闭合——这正是 TODO §1.2/A3 要求前端
// 以「快照轮询 + 部分读取」兜底的场景，此处按同一模式取样。未闭合的
// 请求随之搁置（其尾随 goroutine 随旧运行终态或进程退出消亡）。
func c1WaitHistoryPoll(t *testing.T, h http.Handler, threadID, wantContains string) string {
	t.Helper()
	deadline := time.Now().Add(confirmTestWaitLimit)
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
//（biz 用例真实收到写入），续跑运行在会话中产出终答并可经 /history 回放。
func TestPGConfirm_when_restarted_orphan_approved_executes_and_resumes(t *testing.T) {
	dsn := c1SetupDatabase(t)
	const (
		threadID = "thread-orphan-approve"
		runID    = "run-orphan-approve"
		callID   = "call-orphan-approve"
		nickname = "orphan-approved-nick"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	// 旧进程：运行阻塞在确认等待（搁置，不关闭——Close 会取消在途运行
	// 并按取消语义清理指针，破坏孤儿场景）。
	svc1, cleanupSvc1 := c1SessionService(t, dsn)
	defer cleanupSvc1()
	old := c1NewStack(t, svc1, nicknameCallScript(callID, nickname, "resumed after approval"), repo)
	h1 := identityHandler(old.Handler(), confirmTestTenant, confirmTestOwnerID)
	_ = startChat(h1, threadID, runID, "rename me")
	items := waitForPending(t, h1, threadID, 1)

	// 新进程：全新连接池 + 全新栈（同库）。待确认指针持久化在会话状态，
	// 旧网关的等待者不可达——Pending 应报孤儿（live=false）。
	svc2, cleanupSvc2 := c1SessionService(t, dsn)
	defer cleanupSvc2()
	current := c1NewStack(t, svc2, nicknameCallScript(callID, nickname, "resumed after approval"), repo)
	h2 := identityHandler(current.Handler(), confirmTestTenant, confirmTestOwnerID)

	orphans := waitForPending(t, h2, threadID, 1)
	if orphans[0].ConfirmationID != items[0].ConfirmationID || orphans[0].Live {
		t.Fatalf("orphan pending mismatch: want %s live=false, got %+v",
			items[0].ConfirmationID, orphans[0])
	}

	rec := decide(t, h2, threadID, orphans[0].ConfirmationID, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("orphan decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide path mismatch, want recovery:\n%s", rec.Body.String())
	}

	// 工具被网关代执行，恰好一次、参数正确。
	updates := repo.updates()
	if len(updates) != 1 || updates[0].Nickname != nickname {
		t.Fatalf("recovery must execute the tool once with new nickname, got %+v", updates)
	}
	// 续跑运行收尾：终答随 track 持久化，/history 快照可回放。
	c1WaitHistoryPoll(t, h2, threadID, "resumed after approval")
	waitForPending(t, h2, threadID, 0)
}

// TestPGConfirm_when_restarted_orphan_denied_synthesizes_denial 验证拒绝
// 侧恢复：孤儿确认被拒绝后工具不执行，网关合成 denied 工具结果拉起续跑，
// 模型在续跑中收尾。
func TestPGConfirm_when_restarted_orphan_denied_synthesizes_denial(t *testing.T) {
	dsn := c1SetupDatabase(t)
	const (
		threadID = "thread-orphan-deny"
		runID    = "run-orphan-deny"
		callID   = "call-orphan-deny"
	)
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})

	svc1, cleanupSvc1 := c1SessionService(t, dsn)
	defer cleanupSvc1()
	old := c1NewStack(t, svc1, nicknameCallScript(callID, "orphan-denied-nick", "kept the old nickname"), repo)
	h1 := identityHandler(old.Handler(), confirmTestTenant, confirmTestOwnerID)
	_ = startChat(h1, threadID, runID, "rename me")
	waitForPending(t, h1, threadID, 1)

	svc2, cleanupSvc2 := c1SessionService(t, dsn)
	defer cleanupSvc2()
	current := c1NewStack(t, svc2, nicknameCallScript(callID, "orphan-denied-nick", "kept the old nickname"), repo)
	h2 := identityHandler(current.Handler(), confirmTestTenant, confirmTestOwnerID)
	orphans := waitForPending(t, h2, threadID, 1)

	rec := decide(t, h2, threadID, orphans[0].ConfirmationID, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("orphan decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide path mismatch, want recovery:\n%s", rec.Body.String())
	}

	if updates := repo.updates(); len(updates) != 0 {
		t.Fatalf("orphan denial must not execute the tool, got %+v", updates)
	}
	c1WaitHistoryPoll(t, h2, threadID, "kept the old nickname")
	waitForPending(t, h2, threadID, 0)
}
