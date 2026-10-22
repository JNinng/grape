package task

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"grape/internal/biz"
)

// fakeMaintainer 是记录调用次数的假设施：可注入报告与错误。
type fakeMaintainer struct {
	facility string
	err      error

	mu     sync.Mutex
	calls  int
	called chan struct{}
}

func newFakeMaintainer(facility string, err error) *fakeMaintainer {
	return &fakeMaintainer{facility: facility, err: err, called: make(chan struct{}, 128)}
}

func (f *fakeMaintainer) Facility() string { return f.facility }

func (f *fakeMaintainer) Maintain(context.Context) (biz.MaintenanceReport, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.called <- struct{}{}
	if f.err != nil {
		return biz.MaintenanceReport{}, f.err
	}
	return biz.MaintenanceReport{Actions: []biz.MaintenanceAction{
		{Kind: "noop", Target: "t", Rows: 1, Performed: true},
	}}, nil
}

func (f *fakeMaintainer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitCalls 等待设施被调用至少 n 次，超时 fatal。
func waitCalls(t *testing.T, f *fakeMaintainer, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for f.callCount() < n {
		select {
		case <-f.called:
		case <-deadline:
			t.Fatalf("facility %s called %d times, want >= %d within 2s", f.facility, f.callCount(), n)
		}
	}
}

var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// TestMaintenanceTaskRunsImmediatelyAndStops Start 先立即执行一轮（两个
// 设施都被调到），Stop 后循环退出且不再有后续轮次。
func TestMaintenanceTaskRunsImmediatelyAndStops(t *testing.T) {
	a := newFakeMaintainer("a", nil)
	b := newFakeMaintainer("b", nil)
	mt := newMaintenanceTaskWithInterval([]biz.FacilityMaintenance{a, b}, testLogger, 20*time.Millisecond)

	if err := mt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 立即一轮：不等第一个 ticker 周期
	waitCalls(t, a, 1)
	waitCalls(t, b, 1)
	// 短间隔下周期执行照常
	waitCalls(t, a, 3)

	if err := mt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// 停止后不再有新一轮：等过数个周期长度后调用数不变
	stopped := a.callCount()
	time.Sleep(80 * time.Millisecond)
	if got := a.callCount(); got != stopped {
		t.Fatalf("calls after stop = %d, want %d (no more rounds)", got, stopped)
	}
}

// TestMaintenanceTaskStopIdempotent Stop 幂等：重复调用立即返回，
// 未 Start 直接 Stop 也不出错。
func TestMaintenanceTaskStopIdempotent(t *testing.T) {
	a := newFakeMaintainer("a", nil)
	mt := newMaintenanceTaskWithInterval([]biz.FacilityMaintenance{a}, testLogger, time.Hour)

	// 未 Start：立即返回
	if err := mt.Stop(context.Background()); err != nil {
		t.Fatalf("stop before start: %v", err)
	}

	if err := mt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitCalls(t, a, 1)
	if err := mt.Stop(context.Background()); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mt.Stop(ctx); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

// TestMaintenanceTaskFailureDoesNotBlockOthers 单设施失败只告警不中断：
// 排在前面的失败设施不能阻断后续设施的同轮维护。
func TestMaintenanceTaskFailureDoesNotBlockOthers(t *testing.T) {
	bad := newFakeMaintainer("bad", errors.New("boom"))
	good := newFakeMaintainer("good", nil)
	mt := newMaintenanceTaskWithInterval([]biz.FacilityMaintenance{bad, good}, testLogger, time.Hour)

	if err := mt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitCalls(t, bad, 1)
	waitCalls(t, good, 1)
	if err := mt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
