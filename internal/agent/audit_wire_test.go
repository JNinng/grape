package agent

// 审计接线的装配级测试：进程内会话存储 + 脚本化模型 + 审计挂点替身，
// 覆盖 live/recovery 两路径的工具行与确认行、失败不中断运行与 echo 循环
// 的全量留痕；wrapper 与拦截器单测分别在 audit 包与 httpapi 包。

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"grape/internal/agent/audit"
	"grape/internal/agent/internal/testsupport"
	"grape/internal/agent/tools"
	"grape/internal/biz"

	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// newAuditTestStackOnService 在给定会话存储上装配带审计挂点的确认流测试
// 栈：恢复路径用例需要两个栈共享同一会话存储（「重启」语义），普通用例
// 走 newConfirmTestStack 的自建存储形态。
func newAuditTestStackOnService(t *testing.T, svc session.Service, llm *acktest.ScriptedModel, repo *testsupport.FakeUserRepo, hook audit.Hook) *confirmTestStack {
	t.Helper()
	stack, err := NewStackWithOptions(testsupport.StackConf(), nil, svc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &testsupport.SearchTestEmbedder{Dims: 4}, &testsupport.SearchTestResolver{Dims: 4},
		WithModelOverride(llm), WithAuditHook(hook))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	s := &confirmTestStack{repo: repo, raw: stack.Handler()}
	s.h = s.asUser(testsupport.OwnerID)
	return s
}

// TestAuditWire_when_live_approved_records_tool_call_and_confirmation 验证
// live 同意的两个写入点：批准后工具在原运行内执行，终态 wrapper 直写
// 工具调用行（调用 ID/会话/参数与结果摘要原文/耗时/成功态），决定路由
// 写授权确认行（approved/live/决定人为网关身份回填的 userKey）；工具
// 执行次数与工具审计行数一致。
func TestAuditWire_when_live_approved_records_tool_call_and_confirmation(t *testing.T) {
	const (
		threadID = "thread-audit-approve"
		runID    = "run-audit-approve"
		callID   = "call-audit-approve"
		nickname = "audit-approved-nick"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, runID, "rename me to "+nickname)
	items := testsupport.WaitForPending(t, s.h, threadID, 1)
	decideRec := testsupport.Decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	toolRows, confs, batches := rec.Snapshot()
	if len(batches) != 0 {
		t.Fatalf("live path must not use combined batch, got %d", len(batches))
	}
	updates := repo.Nicknames()
	if len(updates) != 1 {
		t.Fatalf("tool executions = %d, want 1", len(updates))
	}
	if len(toolRows) != len(updates) {
		t.Fatalf("tool audit rows = %d, want %d (matches executions)", len(toolRows), len(updates))
	}
	tc := toolRows[0]
	if tc.ToolCallID != callID || tc.ToolName != tools.UpdateMyNicknameToolName {
		t.Fatalf("tool row id/name = %s/%s, want %s/%s", tc.ToolCallID, tc.ToolName, callID, tools.UpdateMyNicknameToolName)
	}
	if tc.SessionID != threadID {
		t.Fatalf("tool row sessionId = %q, want %q", tc.SessionID, threadID)
	}
	if tc.Status != biz.ToolCallStatusSuccess || tc.RetryCount != 0 {
		t.Fatalf("tool row status/retry = %s/%d, want success/0", tc.Status, tc.RetryCount)
	}
	if want := fmt.Sprintf(`{"nickname":%q}`, nickname); tc.ArgumentsSummary != want {
		t.Fatalf("tool row args summary = %q, want %q", tc.ArgumentsSummary, want)
	}
	if !strings.Contains(tc.ResultSummary, nickname) {
		t.Fatalf("tool row result summary missing nickname: %q", tc.ResultSummary)
	}
	if tc.DurationMS < 0 {
		t.Fatalf("tool row duration = %d, want >= 0", tc.DurationMS)
	}
	if len(confs) != 1 {
		t.Fatalf("confirmation rows = %d, want 1", len(confs))
	}
	cf := confs[0]
	if cf.ConfirmationID != callID || cf.ToolCallID != callID || cf.ToolName != tools.UpdateMyNicknameToolName {
		t.Fatalf("confirmation ids/name = %s/%s/%s, want %s/%s/%s",
			cf.ConfirmationID, cf.ToolCallID, cf.ToolName, callID, callID, tools.UpdateMyNicknameToolName)
	}
	if cf.Decision != biz.ConfirmationDecisionApproved || cf.Path != biz.ConfirmationPathLive {
		t.Fatalf("confirmation decision/path = %s/%s, want approved/live", cf.Decision, cf.Path)
	}
	if cf.SessionID != threadID {
		t.Fatalf("confirmation sessionId = %q, want %q", cf.SessionID, threadID)
	}
	if cf.Remember != "" {
		t.Fatalf("confirmation remember = %q, want empty (not requested)", cf.Remember)
	}
	if want := testsupport.OwnerUserKey(t); cf.DecidedBy != want {
		t.Fatalf("confirmation decidedBy = %q, want gateway backfill %q", cf.DecidedBy, want)
	}
}

// TestAuditWire_when_live_denied_with_remember_records_normalized_confirmation
// 验证 live 拒绝的归一语义：请求显式携带 remember=session，ack 对拒绝
// 静默忽略，确认行必须归一 Remember=""（biz 校验拒绝 deny 携带 remember），
// 工具不执行、无工具审计行。
func TestAuditWire_when_live_denied_with_remember_records_normalized_confirmation(t *testing.T) {
	const (
		threadID = "thread-audit-deny"
		runID    = "run-audit-deny"
		callID   = "call-audit-deny"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "audit-denied-nick", "keeping the old one"), repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, runID, "rename me")
	items := testsupport.WaitForPending(t, s.h, threadID, 1)
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":false,"remember":"session"}`,
		threadID, items[0].ConfirmationID)
	decideRec := testsupport.PostJSON(t, s.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	toolRows, confs, batches := rec.Snapshot()
	if len(toolRows) != 0 || len(batches) != 0 {
		t.Fatalf("denied live must record confirmation only, got %d tool rows / %d batches", len(toolRows), len(batches))
	}
	if len(confs) != 1 {
		t.Fatalf("confirmation rows = %d, want 1", len(confs))
	}
	cf := confs[0]
	if cf.Decision != biz.ConfirmationDecisionDenied {
		t.Fatalf("confirmation decision = %s, want denied", cf.Decision)
	}
	if cf.Remember != "" {
		t.Fatalf("denied remember = %q, want normalized empty", cf.Remember)
	}
	if cf.Path != biz.ConfirmationPathLive {
		t.Fatalf("confirmation path = %s, want live", cf.Path)
	}
	if updates := repo.Nicknames(); len(updates) != 0 {
		t.Fatalf("denied tool must not execute, got %+v", updates)
	}
}

// TestAuditWire_when_approved_with_remember_records_requested_scope 验证
// 同意携带 remember 的作用域原样入参：确认行记录请求的作用域（许可是否
// 真正落库归 ack 许可源语义，审计如实记录用户决定）。
func TestAuditWire_when_approved_with_remember_records_requested_scope(t *testing.T) {
	const (
		threadID = "thread-audit-remember"
		runID    = "run-audit-remember"
		callID   = "call-audit-remember"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "audit-remember-nick", "done"), repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, runID, "rename me")
	items := testsupport.WaitForPending(t, s.h, threadID, 1)
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":true,"remember":"session"}`,
		threadID, items[0].ConfirmationID)
	decideRec := testsupport.PostJSON(t, s.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	testsupport.ChatEvents(t, chat)

	_, confs, _ := rec.Snapshot()
	if len(confs) != 1 {
		t.Fatalf("confirmation rows = %d, want 1", len(confs))
	}
	if confs[0].Remember != biz.ConfirmationRememberSession {
		t.Fatalf("confirmation remember = %q, want %q", confs[0].Remember, biz.ConfirmationRememberSession)
	}
	if confs[0].Decision != biz.ConfirmationDecisionApproved {
		t.Fatalf("confirmation decision = %s, want approved", confs[0].Decision)
	}
}

// TestAuditWire_when_recovery_approved_records_combined_batch 验证恢复
// 路径同意的组合批量：同一会话存储上以第二个栈模拟重启（原网关等待者
// 不可达），孤儿确认批准后网关代执行工具，代执行工具行与确认行同批
// 原子落库，不产生直写工具行。
func TestAuditWire_when_recovery_approved_records_combined_batch(t *testing.T) {
	const (
		threadID = "thread-audit-recovery-approve"
		runID    = "run-audit-recovery-approve"
		callID   = "call-audit-recovery-approve"
		nickname = "audit-recovery-nick"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	script := nicknameCallScript(callID, nickname, "resumed after approval")

	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	// 旧栈：运行阻塞在确认等待（不等待其收尾，随测试进程搁置）。
	oldStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	chat := testsupport.StartChat(oldStack.h, threadID, runID, "rename me")
	testsupport.WaitForPending(t, oldStack.h, threadID, 1)
	// 新栈：同库新网关——待确认指针在共享存储中，等待者不在其内存表。
	curStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	orphans := testsupport.WaitForPending(t, curStack.h, threadID, 1)
	if orphans[0].Live {
		t.Fatalf("orphan must not be live for the new gateway: %+v", orphans[0])
	}

	decideRec := testsupport.Decide(t, curStack.h, threadID, orphans[0].ConfirmationID, true)
	if decideRec.Code != 200 || !strings.Contains(decideRec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide = %d\n%s", decideRec.Code, decideRec.Body.String())
	}
	_ = chat

	toolRows, confs, batches := rec.Snapshot()
	if len(batches) != 1 {
		t.Fatalf("recovery approve must write exactly one combined batch, got %d", len(batches))
	}
	if len(toolRows) != 0 {
		t.Fatalf("recovery approve must not direct-write tool rows, got %d", len(toolRows))
	}
	if len(confs) != 0 {
		t.Fatalf("recovery approve must not direct-write confirmation rows, got %d", len(confs))
	}
	bt, bc := batches[0].Tool, batches[0].Conf
	if bt == nil || bc == nil {
		t.Fatalf("combined batch must carry both rows, got tool=%v conf=%v", bt, bc)
	}
	if updates := repo.Nicknames(); len(updates) != 1 {
		t.Fatalf("recovery must execute the tool once, got %+v", updates)
	}
	if bt.ToolCallID != callID || bt.ToolName != tools.UpdateMyNicknameToolName || bt.Status != biz.ToolCallStatusSuccess {
		t.Fatalf("batch tool row = %s/%s/%s, want %s/%s/success", bt.ToolCallID, bt.ToolName, bt.Status, callID, tools.UpdateMyNicknameToolName)
	}
	if bt.SessionID != threadID {
		t.Fatalf("batch tool row sessionId = %q, want %q", bt.SessionID, threadID)
	}
	if !strings.Contains(bt.ArgumentsSummary, nickname) {
		t.Fatalf("batch tool row args summary missing nickname: %q", bt.ArgumentsSummary)
	}
	if bc.Decision != biz.ConfirmationDecisionApproved || bc.Path != biz.ConfirmationPathRecovery {
		t.Fatalf("batch confirmation decision/path = %s/%s, want approved/recovery", bc.Decision, bc.Path)
	}
	if want := testsupport.OwnerUserKey(t); bc.DecidedBy != want {
		t.Fatalf("batch confirmation decidedBy = %q, want %q", bc.DecidedBy, want)
	}
}

// TestAuditWire_when_recovery_denied_records_confirmation_only_batch 验证
// 恢复路径拒绝的单边批量：孤儿确认被拒绝后工具不执行，批量只带确认行
// （工具行为 nil），拒绝的 remember 同样归一为空。
func TestAuditWire_when_recovery_denied_records_confirmation_only_batch(t *testing.T) {
	const (
		threadID = "thread-audit-recovery-deny"
		runID    = "run-audit-recovery-deny"
		callID   = "call-audit-recovery-deny"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	script := nicknameCallScript(callID, "audit-recovery-denied-nick", "kept the old nickname")

	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	oldStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	chat := testsupport.StartChat(oldStack.h, threadID, runID, "rename me")
	testsupport.WaitForPending(t, oldStack.h, threadID, 1)
	curStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	orphans := testsupport.WaitForPending(t, curStack.h, threadID, 1)

	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":false,"remember":"global"}`,
		threadID, orphans[0].ConfirmationID)
	decideRec := testsupport.PostJSON(t, curStack.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 || !strings.Contains(decideRec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide = %d\n%s", decideRec.Code, decideRec.Body.String())
	}
	_ = chat

	toolRows, confs, batches := rec.Snapshot()
	if len(batches) != 1 {
		t.Fatalf("recovery deny must write exactly one batch, got %d", len(batches))
	}
	if len(toolRows) != 0 || len(confs) != 0 {
		t.Fatalf("recovery deny must not direct-write, got %d tool / %d confirmation rows", len(toolRows), len(confs))
	}
	if batches[0].Tool != nil {
		t.Fatalf("recovery deny batch must not carry a tool row: %+v", batches[0].Tool)
	}
	bc := batches[0].Conf
	if bc == nil || bc.Decision != biz.ConfirmationDecisionDenied || bc.Path != biz.ConfirmationPathRecovery {
		t.Fatalf("recovery deny batch confirmation = %+v, want denied/recovery", bc)
	}
	if bc.Remember != "" {
		t.Fatalf("recovery deny remember = %q, want normalized empty", bc.Remember)
	}
	if updates := repo.Nicknames(); len(updates) != 0 {
		t.Fatalf("recovery denial must not execute the tool, got %+v", updates)
	}
}

// TestAuditWire_when_hook_fails_run_and_decide_still_succeed 验证失败
// 注入纪律：挂点所有写入返回错误且确认写入 panic，live 同意链路仍完整
// 收尾——决定 200、运行以终答结束、工具真实执行一次。
func TestAuditWire_when_hook_fails_run_and_decide_still_succeed(t *testing.T) {
	const (
		threadID = "thread-audit-hook-fail"
		runID    = "run-audit-hook-fail"
		callID   = "call-audit-hook-fail"
		nickname = "audit-hook-fail-nick"
	)
	rec := &testsupport.AuditRecorder{Err: errors.New("audit storage down"), PanicOn: "confirmation"}
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, runID, "rename me")
	items := testsupport.WaitForPending(t, s.h, threadID, 1)
	decideRec := testsupport.Decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200 (audit failure must not break decide)\n%s", decideRec.Code, decideRec.Body.String())
	}
	types, _ := testsupport.ChatEvents(t, chat)
	if !testsupport.HasType(types, "RUN_FINISHED") {
		t.Fatalf("run must finish despite audit failures, types: %v", types)
	}
	if updates := repo.Nicknames(); len(updates) != 1 {
		t.Fatalf("tool must execute despite audit failures, got %+v", updates)
	}
}

// TestAuditWire_when_echo_loop_executions_match_audit_rows 验证全量留痕与
// 计数一致：allow 层级的 echo 每次执行一条成功工具行，且每次评估各落一
// 条 auto 授权行（confirmation_id 即 tool_call_id、path 恒 auto、属主
// userKey 为 decided_by）。
func TestAuditWire_when_echo_loop_executions_match_audit_rows(t *testing.T) {
	const threadID = "thread-audit-echo-loop"
	loop := acktest.NewScriptedModel("audit-loop-model",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: "call-audit-loop", Name: tools.EchoToolName, Args: `{"text":"hi"}`}}},
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, loop, repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, "run-audit-echo-loop", "loop")
	types, _ := testsupport.ChatEvents(t, chat)
	if n := testsupport.CountTypes(types, "TOOL_CALL_RESULT"); n != defaultMaxToolIter {
		t.Fatalf("tool executions = %d, want %d", n, defaultMaxToolIter)
	}
	toolRows, confs, batches := rec.Snapshot()
	if len(batches) != 0 {
		t.Fatalf("allow-tier loop must not use combined batches, got %d", len(batches))
	}
	if len(toolRows) != defaultMaxToolIter {
		t.Fatalf("tool audit rows = %d, want %d (matches executions)", len(toolRows), defaultMaxToolIter)
	}
	// 逐次评估各一条 auto 行：allow 层级的每次工具调用都经授权终态观察
	// 转写为授权确认审计行，无人工决定行。
	if len(confs) != defaultMaxToolIter {
		t.Fatalf("permission audit rows = %d, want %d (one per evaluation)", len(confs), defaultMaxToolIter)
	}
	wantUserKey := testsupport.OwnerUserKey(t)
	for _, tc := range toolRows {
		if tc.ToolCallID != "call-audit-loop" || tc.ToolName != tools.EchoToolName {
			t.Fatalf("unexpected tool row %s/%s", tc.ToolCallID, tc.ToolName)
		}
		if tc.Status != biz.ToolCallStatusSuccess || tc.SessionID != threadID {
			t.Fatalf("unexpected tool row status/session = %s/%q", tc.Status, tc.SessionID)
		}
		if tc.ArgumentsSummary != `{"text":"hi"}` {
			t.Fatalf("unexpected echo args summary: %q", tc.ArgumentsSummary)
		}
	}
	for _, cf := range confs {
		if cf.ConfirmationID != "call-audit-loop" || cf.ToolCallID != "call-audit-loop" || cf.ToolName != tools.EchoToolName {
			t.Fatalf("unexpected permission row %s/%s/%s", cf.ConfirmationID, cf.ToolCallID, cf.ToolName)
		}
		if cf.Decision != biz.ConfirmationDecisionAuto || cf.Path != biz.ConfirmationPathAuto {
			t.Fatalf("permission decision/path = %s/%s, want auto/auto", cf.Decision, cf.Path)
		}
		if cf.SessionID != threadID || cf.Remember != "" {
			t.Fatalf("unexpected permission row session/remember = %q/%q", cf.SessionID, cf.Remember)
		}
		if cf.DecidedBy != wantUserKey {
			t.Fatalf("permission decidedBy = %q, want owner userKey %q", cf.DecidedBy, wantUserKey)
		}
	}
}

// TestAuditWire_when_decide_not_settled_records_no_audit 验证「无结算不
// 审计」：对不存在的确认 ID 提交决定返回 404，三个写入面（工具行/确认行/
// 组合批量）都无行；随后同一会话以真实 ID 结算成功并出审计行——证明
// 空快照源于无结算而非接线断裂。
func TestAuditWire_when_decide_not_settled_records_no_audit(t *testing.T) {
	const (
		threadID = "thread-audit-nosettle"
		runID    = "run-audit-nosettle"
		callID   = "call-audit-nosettle"
		nickname = "audit-nosettle-nick"
	)
	rec := testsupport.NewAuditRecorder()
	repo := testsupport.NewFakeUserRepo(&biz.User{ID: testsupport.OwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := testsupport.StartChat(s.h, threadID, runID, "rename me to "+nickname)
	items := testsupport.WaitForPending(t, s.h, threadID, 1)
	bogus := testsupport.Decide(t, s.h, threadID, "confirmation-missing", true)
	if bogus.Code != 404 {
		t.Fatalf("decide on missing confirmation status = %d, want 404\n%s", bogus.Code, bogus.Body.String())
	}
	toolRows, confs, batches := rec.Snapshot()
	if len(toolRows)+len(confs)+len(batches) != 0 {
		t.Fatalf("no-settle decide must not record, got %d/%d/%d", len(toolRows), len(confs), len(batches))
	}

	decideRec := testsupport.Decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != 200 {
		t.Fatalf("real decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	testsupport.ChatEvents(t, chat)
	toolRows, confs, batches = rec.Snapshot()
	if len(toolRows) != 1 || len(confs) != 1 || len(batches) != 0 {
		t.Fatalf("settled decide rows = %d/%d/%d, want 1/1/0", len(toolRows), len(confs), len(batches))
	}
}
