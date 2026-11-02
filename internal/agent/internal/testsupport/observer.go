package testsupport

import (
	"sync"
	"time"
)

// FakeObserver 是运行观测契约（biz.AgentRunObserver）的内存替身：记录
// 活跃数增量序列、拒绝计数与工具终态计数（键 tool|status）。
type FakeObserver struct {
	mu        sync.Mutex
	deltas    []int
	rejected  int
	toolCalls map[string]int
}

// NewFakeObserver 构造空替身。
func NewFakeObserver() *FakeObserver {
	return &FakeObserver{toolCalls: make(map[string]int)}
}

func (f *FakeObserver) RunActive(delta int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deltas = append(f.deltas, delta)
}

func (f *FakeObserver) RunRejected() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejected++
}

func (f *FakeObserver) ToolCallObserved(name, status string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.toolCalls[name+"|"+status]++
}

// ActiveNow 汇总增量序列得当前活跃数（配对增减时应为 0 或在途数）。
func (f *FakeObserver) ActiveNow() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, d := range f.deltas {
		n += d
	}
	return n
}

// RejectedCount 返回拒绝计数。
func (f *FakeObserver) RejectedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rejected
}

// ToolCount 返回指定工具与终态的观测计数。
func (f *FakeObserver) ToolCount(name, status string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.toolCalls[name+"|"+status]
}
