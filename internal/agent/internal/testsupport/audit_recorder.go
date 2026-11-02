package testsupport

import (
	"context"
	"sync"

	"grape/internal/biz"
)

// AuditBatchRow 是一次组合批量写入的两参快照。
type AuditBatchRow struct {
	Tool *biz.ToolCallRecordInput
	Conf *biz.ConfirmationRecordInput
}

// AuditRecorder 是审计挂点接口的内存替身：捕获三类写入的入参快照，可
// 注入写入错误与 panic（失败注入用例；构造时直接设 Err / PanicOn，用例
// 中途改注入经 SetFailure 加锁）。快照做浅拷贝，断言不受调用侧后续变更
// 影响。Attempts 记写入尝试次数（含失败与 panic 的调用），供「写入确实
// 被尝试」的正向对照。方法签名与 agent 树 audit.Hook 的形状一致（结构化
// 满足，无需 import 审计包——本包禁止 import agent 生产包）。
type AuditRecorder struct {
	mu            sync.Mutex
	toolCalls     []*biz.ToolCallRecordInput
	confirmations []*biz.ConfirmationRecordInput
	batches       []AuditBatchRow
	attempts      int
	Err           error  // 非 nil 时所有写入返回该错误
	PanicOn       string // "tool" / "confirmation" / "batch" 时对应方法 panic
}

// NewAuditRecorder 构造空替身。
func NewAuditRecorder() *AuditRecorder { return &AuditRecorder{} }

// SetFailure 在用例中途改写失败注入（加锁，与并发写入互斥）。
func (r *AuditRecorder) SetFailure(err error, panicOn string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Err = err
	r.PanicOn = panicOn
}

// Attempts 返回写入尝试次数（含失败与 panic 的调用）。
func (r *AuditRecorder) Attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

func (r *AuditRecorder) RecordToolCall(_ context.Context, in *biz.ToolCallRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts += 1
	if r.PanicOn == "tool" {
		panic("audit hook panic: tool call")
	}
	if r.Err != nil {
		return r.Err
	}
	c := *in
	r.toolCalls = append(r.toolCalls, &c)
	return nil
}

func (r *AuditRecorder) RecordConfirmation(_ context.Context, in *biz.ConfirmationRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts += 1
	if r.PanicOn == "confirmation" {
		panic("audit hook panic: confirmation")
	}
	if r.Err != nil {
		return r.Err
	}
	c := *in
	r.confirmations = append(r.confirmations, &c)
	return nil
}

func (r *AuditRecorder) RecordToolCallWithConfirmation(_ context.Context, t *biz.ToolCallRecordInput, c *biz.ConfirmationRecordInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts += 1
	if r.PanicOn == "batch" {
		panic("audit hook panic: batch")
	}
	if r.Err != nil {
		return r.Err
	}
	row := AuditBatchRow{}
	if t != nil {
		tc := *t
		row.Tool = &tc
	}
	if c != nil {
		cc := *c
		row.Conf = &cc
	}
	r.batches = append(r.batches, row)
	return nil
}

// Snapshot 在同一临界区内取全部行（多路并发的审计写入无顺序保证）。
func (r *AuditRecorder) Snapshot() (tools []*biz.ToolCallRecordInput, confs []*biz.ConfirmationRecordInput, batches []AuditBatchRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toolCalls, r.confirmations, r.batches
}
