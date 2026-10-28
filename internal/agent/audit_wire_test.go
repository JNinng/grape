package agent

// 审计接线（AuditHook）测试：进程内会话存储 + 脚本化模型 + 内存
// 用户仓储 + 内存审计挂点替身，覆盖工具执行终态与 ack 决定两个写入点——
// live 同意（工具行直写 + 确认行）、live 拒绝（deny+remember 归一为空、
// 无工具行）、同意携带 remember（作用域原样入参）、恢复路径同意（代执行
// 工具行与确认行组合批量）、恢复路径拒绝（单边批量）、挂点失败不中断
// 运行、echo 循环的执行次数与审计行数一致；wrapper 单元面覆盖无调用 ID
// 跳过、失败终态状态、panic 不传播与捕获器归集。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"grape/internal/biz"

	"github.com/jninng/trpc_agent_go_ack"
	"github.com/jninng/trpc_agent_go_ack/acktest"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// auditBatchRow 是一次组合批量写入的两参快照。
type auditBatchRow struct {
	tool *biz.ToolCallRecordInput
	conf *biz.ConfirmationRecordInput
}

// auditRecorder 是审计挂点的内存替身：捕获三类写入的入参快照，可注入
// 写入错误与 panic（失败注入用例）。快照做浅拷贝，断言不受调用侧后续
// 变更影响。
type auditRecorder struct {
	mu            sync.Mutex
	toolCalls     []*biz.ToolCallRecordInput
	confirmations []*biz.ConfirmationRecordInput
	batches       []auditBatchRow
	err           error  // 非 nil 时所有写入返回该错误
	panicOn       string // "tool" / "confirmation" / "batch" 时对应方法 panic
}

func newAuditRecorder() *auditRecorder { return &auditRecorder{} }

func (r *auditRecorder) RecordToolCall(_ context.Context, in *biz.ToolCallRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.panicOn == "tool" {
		panic("audit hook panic: tool call")
	}
	if r.err != nil {
		return r.err
	}
	c := *in
	r.toolCalls = append(r.toolCalls, &c)
	return nil
}

func (r *auditRecorder) RecordConfirmation(_ context.Context, in *biz.ConfirmationRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.panicOn == "confirmation" {
		panic("audit hook panic: confirmation")
	}
	if r.err != nil {
		return r.err
	}
	c := *in
	r.confirmations = append(r.confirmations, &c)
	return nil
}

func (r *auditRecorder) RecordToolCallWithConfirmation(_ context.Context, t *biz.ToolCallRecordInput, c *biz.ConfirmationRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.panicOn == "batch" {
		panic("audit hook panic: batch")
	}
	if r.err != nil {
		return r.err
	}
	row := auditBatchRow{}
	if t != nil {
		tc := *t
		row.tool = &tc
	}
	if c != nil {
		cc := *c
		row.conf = &cc
	}
	r.batches = append(r.batches, row)
	return nil
}

// snapshot 在同一临界区内取全部行（多路并发的审计写入无顺序保证）。
func (r *auditRecorder) snapshot() (tools []*biz.ToolCallRecordInput, confs []*biz.ConfirmationRecordInput, batches []auditBatchRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toolCalls, r.confirmations, r.batches
}

// newAuditTestStackOnService 在给定会话存储上装配带审计挂点的确认流测试
// 栈：恢复路径用例需要两个栈共享同一会话存储（「重启」语义），普通用例
// 走 newConfirmTestStack 的自建存储形态。
func newAuditTestStackOnService(t *testing.T, svc session.Service, llm *acktest.ScriptedModel, repo *fakeUserRepo, hook AuditHook) *confirmTestStack {
	t.Helper()
	stack, err := NewStackWithOptions(confirmStackConf(), nil, svc, NewVectorStore(nil),
		biz.NewUserUsecase(repo), nil, &searchTestEmbedder{dims: 4},
		WithModelOverride(llm), WithAuditHook(hook))
	if err != nil {
		t.Fatalf("build stack: %v", err)
	}
	t.Cleanup(stack.Close)
	s := &confirmTestStack{repo: repo, raw: stack.Handler()}
	s.h = s.asUser(confirmTestOwnerID)
	return s
}

// ownerUserKey 是归属者身份的 userKey（决定人断言基准）。
func ownerUserKey(t *testing.T) string {
	t.Helper()
	key, err := EncodeUserKey(confirmTestTenant, confirmTestOwnerID)
	if err != nil {
		t.Fatalf("encode owner user key: %v", err)
	}
	return key
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	items := waitForPending(t, s.h, threadID, 1)
	decideRec := decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)

	tools, confs, batches := rec.snapshot()
	if len(batches) != 0 {
		t.Fatalf("live path must not use combined batch, got %d", len(batches))
	}
	updates := repo.nicknames()
	if len(updates) != 1 {
		t.Fatalf("tool executions = %d, want 1", len(updates))
	}
	if len(tools) != len(updates) {
		t.Fatalf("tool audit rows = %d, want %d (matches executions)", len(tools), len(updates))
	}
	tc := tools[0]
	if tc.ToolCallID != callID || tc.ToolName != updateMyNicknameToolName {
		t.Fatalf("tool row id/name = %s/%s, want %s/%s", tc.ToolCallID, tc.ToolName, callID, updateMyNicknameToolName)
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
	if cf.ConfirmationID != callID || cf.ToolCallID != callID || cf.ToolName != updateMyNicknameToolName {
		t.Fatalf("confirmation ids/name = %s/%s/%s, want %s/%s/%s",
			cf.ConfirmationID, cf.ToolCallID, cf.ToolName, callID, callID, updateMyNicknameToolName)
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
	if want := ownerUserKey(t); cf.DecidedBy != want {
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "audit-denied-nick", "keeping the old one"), repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, runID, "rename me")
	items := waitForPending(t, s.h, threadID, 1)
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":false,"remember":"session"}`,
		threadID, items[0].ConfirmationID)
	decideRec := postJSON(t, s.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)

	tools, confs, batches := rec.snapshot()
	if len(tools) != 0 || len(batches) != 0 {
		t.Fatalf("denied live must record confirmation only, got %d tool rows / %d batches", len(tools), len(batches))
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
	if updates := repo.nicknames(); len(updates) != 0 {
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, "audit-remember-nick", "done"), repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, runID, "rename me")
	items := waitForPending(t, s.h, threadID, 1)
	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":true,"remember":"session"}`,
		threadID, items[0].ConfirmationID)
	decideRec := postJSON(t, s.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)

	_, confs, _ := rec.snapshot()
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	script := nicknameCallScript(callID, nickname, "resumed after approval")

	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	// 旧栈：运行阻塞在确认等待（不等待其收尾，随测试进程搁置）。
	oldStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	chat := startChat(oldStack.h, threadID, runID, "rename me")
	waitForPending(t, oldStack.h, threadID, 1)
	// 新栈：同库新网关——待确认指针在共享存储中，等待者不在其内存表。
	curStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	orphans := waitForPending(t, curStack.h, threadID, 1)
	if orphans[0].Live {
		t.Fatalf("orphan must not be live for the new gateway: %+v", orphans[0])
	}

	decideRec := decide(t, curStack.h, threadID, orphans[0].ConfirmationID, true)
	if decideRec.Code != 200 || !strings.Contains(decideRec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide = %d\n%s", decideRec.Code, decideRec.Body.String())
	}
	_ = chat

	tools, confs, batches := rec.snapshot()
	if len(batches) != 1 {
		t.Fatalf("recovery approve must write exactly one combined batch, got %d", len(batches))
	}
	if len(tools) != 0 {
		t.Fatalf("recovery approve must not direct-write tool rows, got %d", len(tools))
	}
	if len(confs) != 0 {
		t.Fatalf("recovery approve must not direct-write confirmation rows, got %d", len(confs))
	}
	bt, bc := batches[0].tool, batches[0].conf
	if bt == nil || bc == nil {
		t.Fatalf("combined batch must carry both rows, got tool=%v conf=%v", bt, bc)
	}
	if updates := repo.nicknames(); len(updates) != 1 {
		t.Fatalf("recovery must execute the tool once, got %+v", updates)
	}
	if bt.ToolCallID != callID || bt.ToolName != updateMyNicknameToolName || bt.Status != biz.ToolCallStatusSuccess {
		t.Fatalf("batch tool row = %s/%s/%s, want %s/%s/success", bt.ToolCallID, bt.ToolName, bt.Status, callID, updateMyNicknameToolName)
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
	if want := ownerUserKey(t); bc.DecidedBy != want {
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	script := nicknameCallScript(callID, "audit-recovery-denied-nick", "kept the old nickname")

	svc, closeSessions, err := NewMemorySessionService()
	if err != nil {
		t.Fatalf("build session service: %v", err)
	}
	t.Cleanup(closeSessions)
	oldStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	chat := startChat(oldStack.h, threadID, runID, "rename me")
	waitForPending(t, oldStack.h, threadID, 1)
	curStack := newAuditTestStackOnService(t, svc, script, repo, rec)
	orphans := waitForPending(t, curStack.h, threadID, 1)

	body := fmt.Sprintf(`{"threadId":%q,"confirmationId":%q,"approved":false,"remember":"global"}`,
		threadID, orphans[0].ConfirmationID)
	decideRec := postJSON(t, curStack.h, "POST", "/agui/ack/confirmation", body)
	if decideRec.Code != 200 || !strings.Contains(decideRec.Body.String(), `"path":"recovery"`) {
		t.Fatalf("decide = %d\n%s", decideRec.Code, decideRec.Body.String())
	}
	_ = chat

	tools, confs, batches := rec.snapshot()
	if len(batches) != 1 {
		t.Fatalf("recovery deny must write exactly one batch, got %d", len(batches))
	}
	if len(tools) != 0 || len(confs) != 0 {
		t.Fatalf("recovery deny must not direct-write, got %d tool / %d confirmation rows", len(tools), len(confs))
	}
	if batches[0].tool != nil {
		t.Fatalf("recovery deny batch must not carry a tool row: %+v", batches[0].tool)
	}
	bc := batches[0].conf
	if bc == nil || bc.Decision != biz.ConfirmationDecisionDenied || bc.Path != biz.ConfirmationPathRecovery {
		t.Fatalf("recovery deny batch confirmation = %+v, want denied/recovery", bc)
	}
	if bc.Remember != "" {
		t.Fatalf("recovery deny remember = %q, want normalized empty", bc.Remember)
	}
	if updates := repo.nicknames(); len(updates) != 0 {
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
	rec := &auditRecorder{err: errors.New("audit storage down"), panicOn: "confirmation"}
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, runID, "rename me")
	items := waitForPending(t, s.h, threadID, 1)
	decideRec := decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != 200 {
		t.Fatalf("decide status = %d, want 200 (audit failure must not break decide)\n%s", decideRec.Code, decideRec.Body.String())
	}
	types, _ := chatEvents(t, chat)
	if !hasType(types, "RUN_FINISHED") {
		t.Fatalf("run must finish despite audit failures, types: %v", types)
	}
	if updates := repo.nicknames(); len(updates) != 1 {
		t.Fatalf("tool must execute despite audit failures, got %+v", updates)
	}
}

// TestAuditWire_when_echo_loop_executions_match_audit_rows 验证全量留痕
// 与计数一致：allow 层级的 echo 工具被循环调用至上限，每次执行都有
// 一条成功工具审计行（调用 ID 来自框架 ctx、会话 ID 来自 Invocation）。
func TestAuditWire_when_echo_loop_executions_match_audit_rows(t *testing.T) {
	const threadID = "thread-audit-echo-loop"
	loop := acktest.NewScriptedModel("audit-loop-model",
		acktest.Step{ToolCalls: []acktest.ToolCallSpec{{ID: "call-audit-loop", Name: echoToolName, Args: `{"text":"hi"}`}}},
	)
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, loop, repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, "run-audit-echo-loop", "loop")
	types, _ := chatEvents(t, chat)
	if n := countTypes(types, "TOOL_CALL_RESULT"); n != defaultMaxToolIter {
		t.Fatalf("tool executions = %d, want %d", n, defaultMaxToolIter)
	}
	tools, confs, batches := rec.snapshot()
	if len(confs) != 0 || len(batches) != 0 {
		t.Fatalf("allow-tier loop must not record confirmations, got %d/%d", len(confs), len(batches))
	}
	if len(tools) != defaultMaxToolIter {
		t.Fatalf("tool audit rows = %d, want %d (matches executions)", len(tools), defaultMaxToolIter)
	}
	for _, tc := range tools {
		if tc.ToolCallID != "call-audit-loop" || tc.ToolName != echoToolName {
			t.Fatalf("unexpected tool row %s/%s", tc.ToolCallID, tc.ToolName)
		}
		if tc.Status != biz.ToolCallStatusSuccess || tc.SessionID != threadID {
			t.Fatalf("unexpected tool row status/session = %s/%q", tc.Status, tc.SessionID)
		}
		if tc.ArgumentsSummary != `{"text":"hi"}` {
			t.Fatalf("unexpected echo args summary: %q", tc.ArgumentsSummary)
		}
	}
}

// newAuditEchoTool 构造直调 wrapper 单测用的 echo 工具清单。
func newAuditEchoTool() []tool.Tool {
	return []tool.Tool{newEchoTool()}
}

// TestAuditTool_when_context_has_no_call_id_skips_record 验证无调用 ID
// 的执行不落审计行（biz 契约 ToolCallID 必填，缺 ID 只记日志跳过），
// 工具结果本身不受影响。
func TestAuditTool_when_context_has_no_call_id_skips_record(t *testing.T) {
	rec := newAuditRecorder()
	logger := slog.New(slog.DiscardHandler)
	tools := auditedTools(newAuditEchoTool(), rec, nil, logger)
	ct, ok := tools[0].(tool.CallableTool)
	if !ok {
		t.Fatalf("wrapped tool must stay callable")
	}
	if got := ct.Declaration().Name; got != echoToolName {
		t.Fatalf("declaration passthrough = %s, want %s", got, echoToolName)
	}
	res, err := ct.Call(context.Background(), []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res == nil {
		t.Fatalf("call result must pass through")
	}
	tools2, confs, batches := rec.snapshot()
	if len(tools2) != 0 || len(confs) != 0 || len(batches) != 0 {
		t.Fatalf("call without tool call id must not record, got %d/%d/%d", len(tools2), len(confs), len(batches))
	}
}

// TestAuditTool_when_inner_tool_fails_records_error_status 验证失败终态：
// 内部工具报错时审计行记 error 状态，结果摘要为错误文本，错误本身原样
// 透传给调用方。
func TestAuditTool_when_inner_tool_fails_records_error_status(t *testing.T) {
	inner := function.NewFunctionTool(
		func(_ context.Context, in echoInput) (echoOutput, error) {
			if strings.TrimSpace(in.Text) == "" {
				return echoOutput{}, fmt.Errorf("text is required")
			}
			return echoOutput{Echo: in.Text}, nil
		},
		function.WithName(echoToolName),
		function.WithDescription("echo"),
	)
	rec := newAuditRecorder()
	wrapped := auditedTools([]tool.Tool{inner}, rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-err-1")
	if _, err := ct.Call(ctx, []byte(`{"text":""}`)); err == nil {
		t.Fatalf("inner error must pass through")
	}
	tools, _, _ := rec.snapshot()
	if len(tools) != 1 {
		t.Fatalf("tool audit rows = %d, want 1", len(tools))
	}
	if tools[0].Status != biz.ToolCallStatusError {
		t.Fatalf("tool row status = %s, want error", tools[0].Status)
	}
	if tools[0].ResultSummary != "text is required" {
		t.Fatalf("tool row result summary = %q, want error text", tools[0].ResultSummary)
	}
	if tools[0].ToolCallID != "call-err-1" {
		t.Fatalf("tool row id = %q, want ctx-injected call id", tools[0].ToolCallID)
	}
}

// TestAuditTool_when_hook_panics_error_does_not_propagate 验证挂点 panic
// 不向工具调用方传播：wrapper 直写路径的 hook panic 被吸收，工具照常
// 返回结果。
func TestAuditTool_when_hook_panics_error_does_not_propagate(t *testing.T) {
	rec := &auditRecorder{panicOn: "tool"}
	tools := auditedTools(newAuditEchoTool(), rec, nil, slog.New(slog.DiscardHandler))
	ct := tools[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-panic-1")
	res, err := ct.Call(ctx, []byte(`{"text":"hi"}`))
	if err != nil || res == nil {
		t.Fatalf("tool call must succeed despite hook panic, got res=%v err=%v", res, err)
	}
}

// TestAuditTool_when_capture_target_matches_records_into_capture 验证
// 捕获器归集：ctx 携带目标一致的捕获器时代执行行只归集不直写，会话 ID
// 由捕获器补齐；目标不一致（续跑运行的新调用）仍走直写。
func TestAuditTool_when_capture_target_matches_records_into_capture(t *testing.T) {
	rec := newAuditRecorder()
	tools := auditedTools(newAuditEchoTool(), rec, nil, slog.New(slog.DiscardHandler))
	ct := tools[0].(tool.CallableTool)

	capture := &recoveryCapture{toolCallID: "call-recovery-1", sessionID: "thread-recovery"}
	ctx := context.WithValue(context.Background(), captureKeyType{}, capture)
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, "call-recovery-1")
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolsRows, _, _ := rec.snapshot()
	if len(toolsRows) != 0 {
		t.Fatalf("captured execution must not direct-write, got %d rows", len(toolsRows))
	}
	taken := capture.take()
	if taken == nil {
		t.Fatalf("capture must hold the proxy execution row")
	}
	if taken.SessionID != "thread-recovery" || taken.ToolCallID != "call-recovery-1" {
		t.Fatalf("captured row = %s/%s, want thread-recovery/call-recovery-1", taken.SessionID, taken.ToolCallID)
	}

	// 目标不一致：ctx 仍是捕获器所在链，但框架注入了新调用 ID。
	ctx2 := context.WithValue(context.Background(), captureKeyType{}, capture)
	ctx2 = context.WithValue(ctx2, tool.ContextKeyToolCallID{}, "call-resumed-1")
	if _, err := ct.Call(ctx2, []byte(`{"text":"yo"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolsRows, _, _ = rec.snapshot()
	if len(toolsRows) != 1 || toolsRows[0].ToolCallID != "call-resumed-1" {
		t.Fatalf("mismatched capture target must direct-write, got %+v", toolsRows)
	}
}

// TestAuditTool_when_inner_tool_panics_records_error_row_and_repanics 验证
// 工具自身 panic 的审计面：defer recover 先落一条 status=error 审计行
// （结果摘要含 panic 值），再原样 re-panic——框架对 panic 的处理语义
// 不变，工具行不再因 panic 被执行边界吞掉而缺失。
func TestAuditTool_when_inner_tool_panics_records_error_row_and_repanics(t *testing.T) {
	inner := function.NewFunctionTool(
		func(context.Context, echoInput) (echoOutput, error) {
			panic("boom in tool")
		},
		function.WithName(echoToolName),
		function.WithDescription("echo"),
	)
	rec := newAuditRecorder()
	wrapped := auditedTools([]tool.Tool{inner}, rec, nil, slog.New(slog.DiscardHandler))
	ct := wrapped[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-panic-tool-1")

	panicked := func() (r any) {
		defer func() { r = recover() }()
		_, _ = ct.Call(ctx, []byte(`{"text":"hi"}`))
		return
	}()
	if panicked == nil {
		t.Fatal("tool panic must propagate unchanged")
	}
	tools, confs, batches := rec.snapshot()
	if len(tools) != 1 || len(confs) != 0 || len(batches) != 0 {
		t.Fatalf("panic audit rows = %d/%d/%d, want 1/0/0", len(tools), len(confs), len(batches))
	}
	row := tools[0]
	if row.ToolCallID != "call-panic-tool-1" || row.ToolName != echoToolName {
		t.Fatalf("panic row id/name = %s/%s, want call-panic-tool-1/%s", row.ToolCallID, row.ToolName, echoToolName)
	}
	if row.Status != biz.ToolCallStatusError {
		t.Fatalf("panic row status = %s, want error", row.Status)
	}
	if !strings.Contains(row.ResultSummary, "tool panicked") || !strings.Contains(row.ResultSummary, "boom in tool") {
		t.Fatalf("panic row result summary = %q, want panic value", row.ResultSummary)
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
	rec := newAuditRecorder()
	repo := newFakeUserRepo(&biz.User{ID: confirmTestOwnerID, Status: biz.UserStatusEnabled,
		Name: "alice", Nickname: "Alice", Locale: "zh-CN"})
	s := newConfirmTestStack(t, nicknameCallScript(callID, nickname, "nickname updated"), repo, WithAuditHook(rec))

	chat := startChat(s.h, threadID, runID, "rename me to "+nickname)
	items := waitForPending(t, s.h, threadID, 1)
	bogus := decide(t, s.h, threadID, "confirmation-missing", true)
	if bogus.Code != nethttp.StatusNotFound {
		t.Fatalf("decide on missing confirmation status = %d, want 404\n%s", bogus.Code, bogus.Body.String())
	}
	tools, confs, batches := rec.snapshot()
	if len(tools)+len(confs)+len(batches) != 0 {
		t.Fatalf("no-settle decide must not record, got %d/%d/%d", len(tools), len(confs), len(batches))
	}

	decideRec := decide(t, s.h, threadID, items[0].ConfirmationID, true)
	if decideRec.Code != nethttp.StatusOK {
		t.Fatalf("real decide status = %d, want 200\n%s", decideRec.Code, decideRec.Body.String())
	}
	chatEvents(t, chat)
	tools, confs, batches = rec.snapshot()
	if len(tools) != 1 || len(confs) != 1 || len(batches) != 0 {
		t.Fatalf("settled decide rows = %d/%d/%d, want 1/1/0", len(tools), len(confs), len(batches))
	}
}

// TestAuditGuard_when_pending_preread_fails_captured_tool_row_written_alone
// 验证预读失败的回退单边写：Pending 预读不可得（payload nil）但捕获器
// 已归集恢复路径代执行行时，工具行以单边批量落库（不因预读失败连坐
// 丢失），确认行不伪造。
func TestAuditGuard_when_pending_preread_fails_captured_tool_row_written_alone(t *testing.T) {
	rec := newAuditRecorder()
	g := &ackAuditGuard{hook: rec, logger: slog.New(slog.DiscardHandler)}
	capture := &recoveryCapture{toolCallID: "call-recover-orphan", sessionID: "thread-recover-orphan"}
	capture.add(&biz.ToolCallRecordInput{
		SessionID:  "thread-recover-orphan",
		ToolCallID: "call-recover-orphan",
		ToolName:   updateMyNicknameToolName,
		Status:     biz.ToolCallStatusSuccess,
	})
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: confirmTestTenant, UserID: confirmTestOwnerID})
	decided := &ackDecisionBody{ThreadID: "thread-recover-orphan", ConfirmationID: "call-recover-orphan", Approved: true}

	g.record(ctx, decided, nil, ack.PathRecovery, capture)

	_, confs, batches := rec.snapshot()
	if len(confs) != 0 {
		t.Fatalf("confirmation rows = %d, want 0 (facts unavailable)", len(confs))
	}
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1 (single-side tool row)", len(batches))
	}
	if batches[0].tool == nil || batches[0].conf != nil {
		t.Fatalf("batch = %+v, want tool row only", batches[0])
	}
	if batches[0].tool.ToolCallID != "call-recover-orphan" || batches[0].tool.SessionID != "thread-recover-orphan" {
		t.Fatalf("batch tool row = %+v, want orphan proxy execution", batches[0].tool)
	}
}

// TestAuditGuard_when_pending_unavailable_without_capture_skips_audit 验证
// 回退的边界：预读不可得且捕获器无代执行行（live 路径或纯降级拒绝）时，
// 无任何写入——既不丢行也不伪造行。
func TestAuditGuard_when_pending_unavailable_without_capture_skips_audit(t *testing.T) {
	rec := newAuditRecorder()
	g := &ackAuditGuard{hook: rec, logger: slog.New(slog.DiscardHandler)}
	ctx := biz.WithIdentity(context.Background(), biz.Identity{TenantID: confirmTestTenant, UserID: confirmTestOwnerID})
	decided := &ackDecisionBody{ThreadID: "thread-live", ConfirmationID: "conf-live", Approved: false}

	g.record(ctx, decided, nil, ack.PathLive, nil)

	tools, confs, batches := rec.snapshot()
	if len(tools)+len(confs)+len(batches) != 0 {
		t.Fatalf("unavailable payload without capture must not record, got %d/%d/%d", len(tools), len(confs), len(batches))
	}
}

// TestWriteAudit_when_hook_blocks_write_abandoned_after_budget 验证审计
// 写入的短超时预算：挂点悬挂时写入在预算内放弃并返回，调用方不阻塞
// （审计旁路面不得反噬业务主流程）。
func TestWriteAudit_when_hook_blocks_write_abandoned_after_budget(t *testing.T) {
	prev := auditWriteTimeout
	auditWriteTimeout = 50 * time.Millisecond
	t.Cleanup(func() { auditWriteTimeout = prev })

	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		writeAudit(context.Background(), slog.New(slog.DiscardHandler), "blocked write",
			func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("write never started")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked write must be abandoned after the budget")
	}
}

// TestAuditTool_when_observer_present_counts_terminal_states 验证仅观测
// 形态（hook 缺省、observer 非 nil）：成功/失败/panic 三种终态都推送
// 工具计数（panic 记 error），无审计行写入；观测先于捕获器归集分支，
// 恢复路径代执行同样被计数。
func TestAuditTool_when_observer_present_counts_terminal_states(t *testing.T) {
	obs := newFakeObserver()
	tools := auditedTools(newAuditEchoTool(), nil, obs, slog.New(slog.DiscardHandler))
	ct := tools[0].(tool.CallableTool)

	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-obs-1")
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := obs.toolCount(echoToolName, biz.ToolCallStatusSuccess); got != 1 {
		t.Fatalf("echo success count = %d, want 1", got)
	}

	// panic 终态同样计数（error），panic 本身照常传播。
	boom := function.NewFunctionTool(
		func(context.Context, echoInput) (echoOutput, error) {
			panic("boom")
		},
		function.WithName(echoToolName),
		function.WithDescription("echo"),
	)
	wrapped := auditedTools([]tool.Tool{boom}, nil, obs, slog.New(slog.DiscardHandler))[0].(tool.CallableTool)
	func() {
		defer func() { _ = recover() }()
		_, _ = wrapped.Call(ctx, []byte(`{"text":"hi"}`))
	}()
	if got := obs.toolCount(echoToolName, biz.ToolCallStatusError); got != 1 {
		t.Fatalf("echo error count (panic terminal) = %d, want 1", got)
	}
}

// TestAuditTool_when_hook_and_observer_both_present_both_fired 验证组合
// 形态：审计挂点与观测契约同时注入时，单一包装点两路都生效——审计行
// 照写、观测计数照推。
func TestAuditTool_when_hook_and_observer_both_present_both_fired(t *testing.T) {
	rec := newAuditRecorder()
	obs := newFakeObserver()
	tools := auditedTools(newAuditEchoTool(), rec, obs, slog.New(slog.DiscardHandler))
	ct := tools[0].(tool.CallableTool)
	ctx := context.WithValue(context.Background(), tool.ContextKeyToolCallID{}, "call-both-1")
	if _, err := ct.Call(ctx, []byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	toolsRows, _, _ := rec.snapshot()
	if len(toolsRows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(toolsRows))
	}
	if got := obs.toolCount(echoToolName, biz.ToolCallStatusSuccess); got != 1 {
		t.Fatalf("observed count = %d, want 1", got)
	}
}
