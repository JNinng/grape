package ops

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// RuntimeMetrics 的指标面测试：推送事实后断言计数/观测值与出口可见性
// （同一 registry 抓取正文含指标族）。nil 接收者 no-op 语义一并覆盖。

// TestRuntimeMetrics_when_run_lifecycle_pushed_reflects_in_metrics 验证
// 活跃连接 gauge 的配对增减与拒绝计数。
func TestRuntimeMetrics_when_run_lifecycle_pushed_reflects_in_metrics(t *testing.T) {
	m := NewRuntimeMetrics()
	m.RunActive(1)
	m.RunActive(1)
	if got := testutil.ToFloat64(m.activeConnections); got != 2 {
		t.Fatalf("active connections = %v, want 2", got)
	}
	m.RunActive(-1)
	if got := testutil.ToFloat64(m.activeConnections); got != 1 {
		t.Fatalf("active connections after release = %v, want 1", got)
	}
	m.RunRejected()
	m.RunRejected()
	if got := testutil.ToFloat64(m.runRejections); got != 2 {
		t.Fatalf("rejections = %v, want 2", got)
	}
}

// TestRuntimeMetrics_when_tool_observed_counts_and_durations 验证工具
// 终态计数按 tool/status 分列、耗时观测落桶。
func TestRuntimeMetrics_when_tool_observed_counts_and_durations(t *testing.T) {
	m := NewRuntimeMetrics()
	m.ToolCallObserved("echo", "success", 20*time.Millisecond)
	m.ToolCallObserved("echo", "success", 30*time.Millisecond)
	m.ToolCallObserved("update_my_nickname", "error", 5*time.Millisecond)

	if got := testutil.ToFloat64(m.toolCalls.WithLabelValues("echo", "success")); got != 2 {
		t.Fatalf("echo success count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.toolCalls.WithLabelValues("update_my_nickname", "error")); got != 1 {
		t.Fatalf("update_my_nickname error count = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.toolDuration, "grape_agent_tool_call_duration_seconds"); got != 2 {
		t.Fatalf("duration series = %d, want 2 (one per tool)", got)
	}
}

// TestRuntimeMetrics_when_nil_receiver_noop 验证 nil 接收者 no-op：测试
// 装配省略 observer 时的零行为语义在契约层面成立。
func TestRuntimeMetrics_when_nil_receiver_noop(t *testing.T) {
	var m *RuntimeMetrics
	m.RunActive(1)
	m.RunRejected()
	m.ToolCallObserved("echo", "success", time.Millisecond)
}

// TestRuntimeMetrics_when_registered_exports_to_scrape 验证指标集注册进
// 出口后随抓取输出（与设施诊断同一 registry）。
func TestRuntimeMetrics_when_registered_exports_to_scrape(t *testing.T) {
	m := NewRuntimeMetrics()
	m.RunActive(1)
	m.RunRejected()
	m.ToolCallObserved("echo", "success", time.Millisecond)
	h := NewMetricsHandler(nil, m)
	body := scrape(t, h)
	for _, want := range []string{
		"grape_agent_sse_active_connections 1",
		"grape_agent_run_limit_rejections_total 1",
		`grape_agent_tool_calls_total{status="success",tool="echo"} 1`,
		"grape_agent_tool_call_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("scrape output missing %q\ngot:\n%s", want, body)
		}
	}
}
