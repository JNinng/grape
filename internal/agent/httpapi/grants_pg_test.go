package httpapi_test

// 工具授权集成测试：以真实 PostgreSQL（本套件专属库
// grape_grant_test）验证许可存储的全链路语义——remember 落库
// agent_tool_grant、同会话二次调用免确认、吊销后重新确认、会话级/
// 全局级作用域差异、重启（重建连接池）后许可仍生效、同键重复授权
// 幂等、RLS 关闭场景跨租户不可见（应用层兜底，与向量层隔离口径一致）、
// 管理端点的权限码边界（种子角色经 fn_has_permission 库内判定）。
//
// 环境变量门控（未设置时跳过）：
//
//	GRAPE_GRANT_TEST_ADMIN_DSN=postgres://postgresql:root@127.0.0.1:5432/postgres \
//	go test ./internal/agent/httpapi -run TestPGGrant -count=1 -v
//
// 装载取舍与会话/确认恢复集成测试一致：TestDeployFiles 全清单部署（含
// 96_agent_grant.sql、权限码种子与测试种子 test_seed.sql）+ 跨包部署锁
// 互斥，绝不触碰其他套件的测试库。「重启」以全新连接池 + 全新栈模拟
//（不 Close 旧栈），与确认恢复路径（根包 confirm_pg_test.go）的口径一致。
//
// 外部测试包（httpapi_test）形态：用例经完整 Stack（根包生产装配路径）
// 与本包导出构造函数驱动，不触达包内私有符号。

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"grape/internal/agent"
	"grape/internal/agent/httpapi"
	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/data/pg_sqlc"

	"github.com/jackc/pgx/v5"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// grantSuite 是本套件专属测试库定位；其余测试库绝不触碰。
var grantSuite = testsupport.SuiteDatabase{
	EnvVar:         "GRAPE_GRANT_TEST_ADMIN_DSN",
	Database:       "grape_grant_test",
	GrapeUPassword: "grape_u_pwd",
	SkipMessage:    "GRAPE_GRANT_TEST_ADMIN_DSN not set, skip grant integration tests",
}

// grantTestTenantAdmin / grantTestTenantPlain 是权限码边界用例的种子身份：
// 用户 1（admin，t-super-01，角色 101 → 菜单含 agent:grant:query/revoke）
// 与用户 12（superplain，t-super-01，无角色 → 无 agent 授权码）。两者须
// 同租户：吊销他人授权的 403 分支要求目标行对操作者可见（跨租户行在
// 可见性判定即 404，走不到权限码判定）。
var (
	grantTestTenantAdmin = biz.Identity{TenantID: "t-super-01", UserID: 1}
	grantTestTenantPlain = biz.Identity{TenantID: "t-super-01", UserID: 12}
)

// grantSetupDatabase 重建专属测试库并按 TestDeployFiles 全清单部署，返回
// grape_u DSN。形态与既有套件一致（单一事实源的部署清单 +
// 跨包互斥锁）。
func grantSetupDatabase(t *testing.T) string {
	t.Helper()
	return testsupport.SetupSuiteDatabase(t, grantSuite)
}

// grantSessionService 经生产装配函数构造会话服务（postgres 后端）。
func grantSessionService(t *testing.T, grapeUDSN string) (session.Service, func()) {
	t.Helper()
	c := &conf.Agent{AppName: testsupport.AppName}
	d := &conf.Data{Database: &conf.Data_Database{Master: &conf.Data_Database_Slot{Source: grapeUDSN}}}
	sessionSvc, cleanup, err := agent.NewSessionService(c, d)
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	return sessionSvc, cleanup
}

// grantUsecase 构造接真实库的授权用例（grape_u 连接专属测试库）。
func grantUsecase(t *testing.T, grapeUDSN string) (*biz.GrantUsecase, func()) {
	t.Helper()
	data, cleanup, err := pg_sqlc.NewData(&conf.Data{Database: &conf.Data_Database{
		// 两槽校验都要求 source 必填：测试只消费 master，system 槽以
		// 同一 DSN 占位过校验（不建池）。
		Master: &conf.Data_Database_Slot{Source: grapeUDSN},
		System: &conf.Data_Database_Slot{Source: grapeUDSN},
	}})
	if err != nil {
		t.Fatalf("build data: %v", err)
	}
	return biz.NewGrantUsecase(pg_sqlc.NewGrantRepo(data)), cleanup
}

// grantNewStack 以注入模型、用户仓储与授权用例构建完整栈（生产装配路径，
// grants 恒非 nil——许可源接真实授权表）。
func grantNewStack(t *testing.T, svc session.Service, llm *acktest.ScriptedModel, repo *testsupport.FakeUserRepo, grants *biz.GrantUsecase) *agent.Stack {
	t.Helper()
	stack, err := agent.NewStackWithOptions(testsupport.StackConf(), nil, svc, agent.NewVectorStore(nil),
		biz.NewUserUsecase(repo), grants, &testsupport.SearchTestEmbedder{Dims: 4}, &testsupport.SearchTestResolver{Dims: 4}, agent.WithModelOverride(llm))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	return stack
}

// grantScript 构造「第 N 次运行」的脚本：会话历史中已有 priorTools 条
// 工具消息（每次工具执行累加一条），脚本化模型按 RoleTool 消息数推进
// 轮次，故先前缀对应数量的占位步，再发一次工具调用、回终答。
func grantScript(priorTools int, callID, nickname, finalText string) *acktest.ScriptedModel {
	steps := make([]acktest.Step, 0, priorTools+2)
	for i := 0; i < priorTools; i++ {
		steps = append(steps, acktest.Step{Text: "waiting for the real step"})
	}
	steps = append(steps,
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{
			ID: callID, Name: tools.UpdateMyNicknameToolName,
			Args: `{"nickname":"` + nickname + `"}`,
		}}},
		acktest.Step{Text: finalText},
	)
	return acktest.NewScriptedModel("grant-script", steps...)
}

// grantDecide 提交一次带 remember 的确认决定（ackhttp 决定体的 remember
// 字段：session / global / 空）。
func grantDecide(t *testing.T, h nethttp.Handler, threadID, confirmationID string, approved bool, remember string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"threadId":"` + threadID + `","confirmationId":"` + confirmationID + `","approved":` + boolJS(approved) +
		`,"remember":"` + remember + `"}`
	return testsupport.PostJSON(t, h, nethttp.MethodPost, "/agui/ack/confirmation", body)
}

func boolJS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// grantListOwnGrants 经管理端点列出本人活跃授权，返回授权 id 列表。
func grantListOwnGrants(t *testing.T, h nethttp.Handler) []httpapi.GrantItem {
	t.Helper()
	rec := testsupport.PostJSON(t, h, nethttp.MethodGet, "/agui/grants", "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("list grants status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	var reply httpapi.GrantListReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatalf("parse grant list: %v\n%s", err, rec.Body.String())
	}
	return reply.Grants
}

// grantRunOnce 在指定线程上发起一次运行并等待收尾，返回事件流文本。
func grantRunOnce(t *testing.T, h nethttp.Handler, threadID, runID, message string) string {
	t.Helper()
	chat := testsupport.StartChat(h, threadID, runID, message)
	_, body := testsupport.ChatEvents(t, chat)
	return body
}

// grantRequireNoConfirmation 断言事件流没有确认卡（免确认直通的信号）。
func grantRequireNoConfirmation(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "tool_confirmation") {
		t.Fatalf("run must skip confirmation, got confirmation event:\n%s", body)
	}
}

// TestPGGrant_when_remember_session_second_call_skips_confirmation 验证
// live 全链路（脚本模型 + 真实 grant 表）：首次确认同意 remember=session
// 落库；同会话二次调用免确认直接执行（Lookup 命中）。
func TestPGGrant_when_remember_session_second_call_skips_confirmation(t *testing.T) {
	dsn := grantSetupDatabase(t)
	const (
		thread = "thread-grant-session"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	svc, cleanupSvc := grantSessionService(t, dsn)
	defer cleanupSvc()
	grants, cleanupGrants := grantUsecase(t, dsn)
	defer cleanupGrants()

	// 第一次运行：确认 → 同意并 remember=session。
	s1 := grantNewStack(t, svc, grantScript(0, "call-g1", "nick-first", "first done"), repo, grants)
	h1 := testsupport.IdentityHandler(s1.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h1, thread, "run-g1", "rename me")
	items := testsupport.WaitForPending(t, h1, thread, 1)
	rec := grantDecide(t, h1, thread, items[0].ConfirmationID, true, "session")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat)
	if updates := repo.Nicknames(); len(updates) != 1 || updates[0].Nickname != "nick-first" {
		t.Fatalf("first run must execute the tool once, got %+v", updates)
	}

	// 许可已落库：会话级一行，工具与作用域正确。
	own := grantListOwnGrants(t, h1)
	if len(own) != 1 || own[0].ToolName != tools.UpdateMyNicknameToolName || own[0].Scope != "session" {
		t.Fatalf("session grant must be persisted, got %+v", own)
	}

	// 第二次运行（同会话）：免确认直接执行。
	s2 := grantNewStack(t, svc, grantScript(1, "call-g2", "nick-second", "second done"), repo, grants)
	h2 := testsupport.IdentityHandler(s2.Handler(), testsupport.Tenant, testsupport.OwnerID)
	body := grantRunOnce(t, h2, thread, "run-g2", "rename me again")
	grantRequireNoConfirmation(t, body)
	if !strings.Contains(body, "second done") {
		t.Fatalf("second run must finish with its final text:\n%s", body)
	}
	updates := repo.Nicknames()
	if len(updates) != 2 || updates[1].Nickname != "nick-second" {
		t.Fatalf("second call must execute without confirmation, got %+v", updates)
	}
	if left := testsupport.WaitForPending(t, h2, thread, 0); len(left) != 0 {
		t.Fatalf("no confirmation may be pending, got %+v", left)
	}
}

// TestPGGrant_when_revoked_grant_reconfirms_on_next_call 验证吊销生效：
// 吊销端点置 revoked_at 后，同会话下次调用重新触发确认（Lookup 不命中
// 天然达成），工具在决定前不执行。
func TestPGGrant_when_revoked_grant_reconfirms_on_next_call(t *testing.T) {
	dsn := grantSetupDatabase(t)
	const thread = "thread-grant-revoke"
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	svc, cleanupSvc := grantSessionService(t, dsn)
	defer cleanupSvc()
	grants, cleanupGrants := grantUsecase(t, dsn)
	defer cleanupGrants()

	s1 := grantNewStack(t, svc, grantScript(0, "call-r1", "nick-r1", "r1 done"), repo, grants)
	h1 := testsupport.IdentityHandler(s1.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h1, thread, "run-r1", "rename me")
	items := testsupport.WaitForPending(t, h1, thread, 1)
	if rec := grantDecide(t, h1, thread, items[0].ConfirmationID, true, "session"); rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	// 第二次运行免确认（许可生效基线）。
	s2 := grantNewStack(t, svc, grantScript(1, "call-r2", "nick-r2", "r2 done"), repo, grants)
	h2 := testsupport.IdentityHandler(s2.Handler(), testsupport.Tenant, testsupport.OwnerID)
	grantRequireNoConfirmation(t, grantRunOnce(t, h2, thread, "run-r2", "rename me again"))

	// 吊销：属主经管理端点吊销自己的授权。
	own := grantListOwnGrants(t, h2)
	if len(own) != 1 {
		t.Fatalf("want exactly one active grant, got %+v", own)
	}
	rec := testsupport.PostJSON(t, h2, nethttp.MethodDelete, "/agui/grants/"+strconv.FormatInt(own[0].ID, 10), "")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("revoke status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if left := grantListOwnGrants(t, h2); len(left) != 0 {
		t.Fatalf("revoked grant must leave the active list, got %+v", left)
	}

	// 第三次运行：重新触发确认，决定前工具不执行。
	s3 := grantNewStack(t, svc, grantScript(2, "call-r3", "nick-r3", "r3 done"), repo, grants)
	h3 := testsupport.IdentityHandler(s3.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat3 := testsupport.StartChat(h3, thread, "run-r3", "rename me once more")
	items3 := testsupport.WaitForPending(t, h3, thread, 1)
	if items3[0].ToolName != tools.UpdateMyNicknameToolName {
		t.Fatalf("reconfirm must target the same tool, got %+v", items3[0])
	}
	if updates := repo.Nicknames(); len(updates) != 2 {
		t.Fatalf("tool must not execute before the new decision, got %+v", updates)
	}
	if rec := grantDecide(t, h3, thread, items3[0].ConfirmationID, false, ""); rec.Code != nethttp.StatusOK {
		t.Fatalf("deny status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat3)
	if updates := repo.Nicknames(); len(updates) != 2 {
		t.Fatalf("denied reconfirm must not execute the tool, got %+v", updates)
	}
}

// TestPGGrant_when_session_grant_does_not_cover_other_thread 验证会话级
// 作用域边界：thread-A 的会话级许可不放行 thread-B 的调用。
func TestPGGrant_when_session_grant_does_not_cover_other_thread(t *testing.T) {
	dsn := grantSetupDatabase(t)
	const (
		threadA = "thread-scope-a"
		threadB = "thread-scope-b"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	svc, cleanupSvc := grantSessionService(t, dsn)
	defer cleanupSvc()
	grants, cleanupGrants := grantUsecase(t, dsn)
	defer cleanupGrants()

	s1 := grantNewStack(t, svc, grantScript(0, "call-sa", "nick-sa", "sa done"), repo, grants)
	h1 := testsupport.IdentityHandler(s1.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h1, threadA, "run-sa", "rename me")
	items := testsupport.WaitForPending(t, h1, threadA, 1)
	if rec := grantDecide(t, h1, threadA, items[0].ConfirmationID, true, "session"); rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	// thread-B（同用户）：会话级许可不覆盖，重新触发确认。
	s2 := grantNewStack(t, svc, grantScript(0, "call-sb", "nick-sb", "sb done"), repo, grants)
	h2 := testsupport.IdentityHandler(s2.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chatB := testsupport.StartChat(h2, threadB, "run-sb", "rename me in another thread")
	itemsB := testsupport.WaitForPending(t, h2, threadB, 1)
	if itemsB[0].ConfirmationID == items[0].ConfirmationID {
		t.Fatalf("thread-B must raise its own confirmation, got %+v", itemsB)
	}
	if updates := repo.Nicknames(); len(updates) != 1 {
		t.Fatalf("tool must not execute in thread-B before decision, got %+v", updates)
	}
	if rec := grantDecide(t, h2, threadB, itemsB[0].ConfirmationID, false, ""); rec.Code != nethttp.StatusOK {
		t.Fatalf("deny status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chatB)
}

// TestPGGrant_when_remember_global_covers_other_thread 验证全局级作用域：
// remember=global 的许可对同用户的其他会话免确认，落库行 session_id 为空。
func TestPGGrant_when_remember_global_covers_other_thread(t *testing.T) {
	dsn := grantSetupDatabase(t)
	const (
		threadA = "thread-global-a"
		threadB = "thread-global-b"
	)
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	svc, cleanupSvc := grantSessionService(t, dsn)
	defer cleanupSvc()
	grants, cleanupGrants := grantUsecase(t, dsn)
	defer cleanupGrants()

	s1 := grantNewStack(t, svc, grantScript(0, "call-ga", "nick-ga", "ga done"), repo, grants)
	h1 := testsupport.IdentityHandler(s1.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h1, threadA, "run-ga", "rename me")
	items := testsupport.WaitForPending(t, h1, threadA, 1)
	if rec := grantDecide(t, h1, threadA, items[0].ConfirmationID, true, "global"); rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	own := grantListOwnGrants(t, h1)
	if len(own) != 1 || own[0].Scope != "global" {
		t.Fatalf("global grant must be persisted with empty session, got %+v", own)
	}

	// thread-B：全局级许可覆盖，免确认直接执行。
	s2 := grantNewStack(t, svc, grantScript(0, "call-gb", "nick-gb", "gb done"), repo, grants)
	h2 := testsupport.IdentityHandler(s2.Handler(), testsupport.Tenant, testsupport.OwnerID)
	body := grantRunOnce(t, h2, threadB, "run-gb", "rename me in another thread")
	grantRequireNoConfirmation(t, body)
	if updates := repo.Nicknames(); len(updates) != 2 || updates[1].Nickname != "nick-gb" {
		t.Fatalf("global grant must cover other threads, got %+v", updates)
	}
}

// TestPGGrant_when_pool_rebuilt_grant_still_effective 验证持久化维度
// 的重启语义：许可在库中跨连接池存活，重建池 + 重建栈后同会话调用
// 仍免确认（与孤儿确认的恢复语义互补：grants 在库、pending 指针
// 在会话状态，重启后都还在）。
func TestPGGrant_when_pool_rebuilt_grant_still_effective(t *testing.T) {
	dsn := grantSetupDatabase(t)
	const thread = "thread-grant-restart"
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	svc1, cleanupSvc1 := grantSessionService(t, dsn)
	defer cleanupSvc1()
	grants, cleanupGrants := grantUsecase(t, dsn)
	defer cleanupGrants()

	s1 := grantNewStack(t, svc1, grantScript(0, "call-p1", "nick-p1", "p1 done"), repo, grants)
	h1 := testsupport.IdentityHandler(s1.Handler(), testsupport.Tenant, testsupport.OwnerID)
	chat := testsupport.StartChat(h1, thread, "run-p1", "rename me")
	items := testsupport.WaitForPending(t, h1, thread, 1)
	if rec := grantDecide(t, h1, thread, items[0].ConfirmationID, true, "session"); rec.Code != nethttp.StatusOK {
		t.Fatalf("decide status = %d\n%s", rec.Code, rec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	// 「重启」：全新连接池 + 全新栈（旧池不关闭，模拟进程被替换）。
	svc2, cleanupSvc2 := grantSessionService(t, dsn)
	defer cleanupSvc2()
	s2 := grantNewStack(t, svc2, grantScript(1, "call-p2", "nick-p2", "p2 done"), repo, grants)
	h2 := testsupport.IdentityHandler(s2.Handler(), testsupport.Tenant, testsupport.OwnerID)
	body := grantRunOnce(t, h2, thread, "run-p2", "rename me after restart")
	grantRequireNoConfirmation(t, body)
	if updates := repo.Nicknames(); len(updates) != 2 || updates[1].Nickname != "nick-p2" {
		t.Fatalf("grant must survive the restart, got %+v", updates)
	}
}

// TestPGGrant_when_duplicate_active_key_save_is_idempotent 验证部分唯一
// 约束语义（决策：同键重复授权幂等跳过，不覆盖不报错——许可是布尔存在
// 而非计数，先到决定生效）：双写后活跃行一条；吊销后可重新授权，旧行
// 保留为审计现场（活跃行仍是全新一条）。
func TestPGGrant_when_duplicate_active_key_save_is_idempotent(t *testing.T) {
	dsn := grantSetupDatabase(t)
	grants, cleanup := grantUsecase(t, dsn)
	defer cleanup()
	ctx := context.Background()
	auth := biz.Identity{TenantID: testsupport.Tenant, UserID: testsupport.OwnerID}

	if err := grants.Remember(ctx, auth, "thread-dup", "update_my_nickname"); err != nil {
		t.Fatalf("first remember: %v", err)
	}
	if err := grants.Remember(ctx, auth, "thread-dup", "update_my_nickname"); err != nil {
		t.Fatalf("duplicate remember must be idempotent, got %v", err)
	}
	own, err := grants.ListOwn(ctx, auth)
	if err != nil || len(own) != 1 {
		t.Fatalf("active grants = %d, want 1 (err %v)", len(own), err)
	}
	firstID := own[0].ID

	// 吊销后同键重新授权：新活跃行成立，被吊销行不复活也不冲突。
	if err := grants.Revoke(ctx, auth, firstID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := grants.Remember(ctx, auth, "thread-dup", "update_my_nickname"); err != nil {
		t.Fatalf("re-grant after revoke: %v", err)
	}
	own, err = grants.ListOwn(ctx, auth)
	if err != nil || len(own) != 1 {
		t.Fatalf("active grants after re-grant = %d, want 1 (err %v)", len(own), err)
	}
	if own[0].ID == firstID {
		t.Fatalf("re-grant must create a new row, got the revoked one %d", firstID)
	}
}

// TestPGGrant_when_rls_disabled_cross_tenant_invisible 验证 RLS 关闭
// 场景的应用层兜底（与向量层隔离口径一致）：以超级用户关闭 agent_tool_grant
// 的 RLS 后，认证租户参数仍把跨租户读写挡住——列表只见本租户、许可
// 查询不命中他租户、吊销他租户行等同不存在。
func TestPGGrant_when_rls_disabled_cross_tenant_invisible(t *testing.T) {
	adminDSN := testsupport.AdminDSN(t, grantSuite)
	dsn := grantSetupDatabase(t)
	ctx := context.Background()

	// 关闭 RLS：模拟「数据库策略缺失/被绕过」的运行形态。
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	cfg.Database = grantSuite.Database
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx,
		"ALTER TABLE public.agent_tool_grant DISABLE ROW LEVEL SECURITY; "+
			"ALTER TABLE public.agent_tool_grant NO FORCE ROW LEVEL SECURITY"); err != nil {
		t.Fatalf("disable rls: %v", err)
	}

	grants, cleanup := grantUsecase(t, dsn)
	defer cleanup()
	tenantA := biz.Identity{TenantID: "t-rls-a", UserID: 7}
	tenantB := biz.Identity{TenantID: "t-rls-b", UserID: 8}
	if err := grants.Remember(ctx, tenantA, "thread-rls-a", "update_my_nickname"); err != nil {
		t.Fatalf("remember tenant A: %v", err)
	}
	if err := grants.Remember(ctx, tenantB, "thread-rls-b", "update_my_nickname"); err != nil {
		t.Fatalf("remember tenant B: %v", err)
	}

	// 租户 A 的列表只见本租户行。
	ownA, err := grants.ListOwn(ctx, tenantA)
	if err != nil || len(ownA) != 1 || ownA[0].TenantID != "t-rls-a" {
		t.Fatalf("tenant A must see only its own grant, got %+v (err %v)", ownA, err)
	}
	// 租户 A 查询租户 B 用户的许可：不命中。
	g, err := grants.Resolve(ctx, tenantA, 8, "thread-rls-b", "update_my_nickname")
	if err != nil || g != nil {
		t.Fatalf("cross-tenant lookup must miss, got %+v (err %v)", g, err)
	}
	// 租户 A 吊销租户 B 的授权：目标不可见等同不存在。
	ownB, err := grants.ListOwn(ctx, tenantB)
	if err != nil || len(ownB) != 1 {
		t.Fatalf("tenant B must still see its own grant, got %+v (err %v)", ownB, err)
	}
	if err := grants.Revoke(ctx, tenantA, ownB[0].ID); !errors.Is(err, biz.ErrGrantNotFound) {
		t.Fatalf("cross-tenant revoke must return ErrGrantNotFound, got %v", err)
	}
	// 租户 B 的授权不受影响。
	if ownB, err = grants.ListOwn(ctx, tenantB); err != nil || len(ownB) != 1 {
		t.Fatalf("tenant B grant must survive, got %+v (err %v)", ownB, err)
	}
}

// TestPGGrant_when_management_permission_boundary_over_seed_roles 验证
// 管理端点的权限码边界（种子角色经 fn_has_permission 库内判定）：
// 持 agent:grant:query/revoke 的管理员（种子用户 1）可全租户列表与
// 吊销他人授权；普通用户（种子用户 12 superplain，无 agent 授权码）403、本人
// 视角不受影响。
func TestPGGrant_when_management_permission_boundary_over_seed_roles(t *testing.T) {
	dsn := grantSetupDatabase(t)
	grants, cleanup := grantUsecase(t, dsn)
	defer cleanup()
	ctx := context.Background()

	// 管理员建一条本人会话级授权。
	if err := grants.Remember(ctx, grantTestTenantAdmin, "thread-boundary", "update_my_nickname"); err != nil {
		t.Fatalf("remember as admin: %v", err)
	}

	// 全租户列表：管理员放行、普通用户 403。
	tenantWide, err := grants.ListTenant(ctx, grantTestTenantAdmin)
	if err != nil || len(tenantWide) != 1 {
		t.Fatalf("admin tenant list = %+v (err %v), want exactly one row", tenantWide, err)
	}
	if _, err := grants.ListTenant(ctx, grantTestTenantPlain); !errors.Is(err, biz.ErrGrantAccessDenied) {
		t.Fatalf("plain user tenant list must be denied, got %v", err)
	}
	// 吊销他人授权：普通用户 403（授权行存在但不放行）。
	if err := grants.Revoke(ctx, grantTestTenantPlain, tenantWide[0].ID); !errors.Is(err, biz.ErrGrantAccessDenied) {
		t.Fatalf("plain user revoke must be denied, got %v", err)
	}
	// 管理员吊销本人授权：不设码，直接生效。
	if err := grants.Revoke(ctx, grantTestTenantAdmin, tenantWide[0].ID); err != nil {
		t.Fatalf("admin self revoke: %v", err)
	}
	// 吊销后普通用户可见性仍为空（未产生跨租户泄漏）。
	plainOwn, err := grants.ListOwn(ctx, grantTestTenantPlain)
	if err != nil || len(plainOwn) != 0 {
		t.Fatalf("plain user must own nothing, got %+v (err %v)", plainOwn, err)
	}
}
