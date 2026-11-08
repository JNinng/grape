package ops

import (
	"time"

	"grape/internal/biz"

	"github.com/prometheus/client_golang/prometheus"
)

// RuntimeMetrics 是 agent 运行面的 Prometheus 指标集，实现
// biz.AgentRunObserver：事实由 agent 适配层在运行起止、限流拒绝与工具
// 终态推送，经 /metrics 与设施诊断同一出口输出。标签基数有界：tool 取
// 装配层清单、status 取 biz 两值枚举，无用户/租户维度。零值不可用，
// 一律经 NewRuntimeMetrics 构造；扩展点与接线落点见 README。
type RuntimeMetrics struct {
	activeConnections prometheus.Gauge
	runRejections     prometheus.Counter
	toolCalls         *prometheus.CounterVec
	toolDuration      *prometheus.HistogramVec
}

// NewRuntimeMetrics 构造 agent 运行面指标集。
func NewRuntimeMetrics() *RuntimeMetrics {
	return &RuntimeMetrics{
		activeConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "grape_agent_sse_active_connections",
			Help: "Currently active AG-UI SSE runs.",
		}),
		runRejections: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "grape_agent_run_limit_rejections_total",
			Help: "AG-UI run requests rejected by the per-identity concurrency limit.",
		}),
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grape_agent_tool_calls_total",
			Help: "Tool call terminal states by tool and status.",
		}, []string{"tool", "status"}),
		toolDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grape_agent_tool_call_duration_seconds",
			Help:    "Tool call execution duration by tool.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"tool"}),
	}
}

// Describe / Collect 委托给内部采集器：指标集整体注册进出口 registry。
func (m *RuntimeMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.activeConnections.Describe(ch)
	m.runRejections.Describe(ch)
	m.toolCalls.Describe(ch)
	m.toolDuration.Describe(ch)
}

func (m *RuntimeMetrics) Collect(ch chan<- prometheus.Metric) {
	m.activeConnections.Collect(ch)
	m.runRejections.Collect(ch)
	m.toolCalls.Collect(ch)
	m.toolDuration.Collect(ch)
}

// RunActive 调整活跃 SSE 运行数；nil 接收者安全（no-op），agent 侧测试
// 装配省略 observer 时无需判空。
func (m *RuntimeMetrics) RunActive(delta int) {
	if m == nil {
		return
	}
	m.activeConnections.Add(float64(delta))
}

// RunRejected 记一次并发上限拒绝。
func (m *RuntimeMetrics) RunRejected() {
	if m == nil {
		return
	}
	m.runRejections.Inc()
}

// ToolCallObserved 记一次工具调用终态计数与耗时观测。
func (m *RuntimeMetrics) ToolCallObserved(tool string, status string, d time.Duration) {
	if m == nil {
		return
	}
	m.toolCalls.WithLabelValues(tool, status).Inc()
	m.toolDuration.WithLabelValues(tool).Observe(d.Seconds())
}

// 接口符合性：指标集直接充当 biz 观测契约（ops 只依赖 biz 契约的分层
// 纪律不破）。
var _ biz.AgentRunObserver = (*RuntimeMetrics)(nil)
