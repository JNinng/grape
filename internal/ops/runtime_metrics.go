package ops

import (
	"time"

	"grape/internal/biz"

	"github.com/prometheus/client_golang/prometheus"
)

// RuntimeMetrics 是 agent 运行面的 Prometheus 指标集：实现
// biz.AgentRunObserver，事实由 agent 框架适配层在运行起止、限流拒绝与
// 工具终态推送，经 /metrics 与设施诊断同一出口输出（NewMetricsHandler
// 注册本采集器）。标签基数有界：tool 取装配层工具清单（个位数）、
// status 取 biz 的两值枚举，无用户/租户维度（基数爆炸与 PII 风险都不
// 进入指标面）。
//
// 当前指标：
//   - grape_agent_sse_active_connections：活跃 SSE 运行数（gauge，
//     chat 运行起止配对增减）；
//   - grape_agent_run_limit_rejections_total：因并发上限被拒绝的运行
//     请求数（counter）；
//   - grape_agent_tool_calls_total：工具调用终态计数（counter，按
//     tool/status 分）；含 panic 终态（status=error）与恢复路径代执行；
//   - grape_agent_tool_call_duration_seconds：工具执行耗时（histogram，
//     按 tool 分；成功与失败终态都观测，失败常更快，对比时注意幸存者
//     偏差）。
//
// 扩展点备案（量未交付，接线落点已明确）：
//   - 检索耗时/召回命中率：事实在 data 层留痕装饰器（retrieval_log.go
//     已有耗时与命中数），接线需扩充 biz 观测契约（如
//     RetrievalObserved）或把装饰器改为可注入 observer 的构造参数；
//   - LLM 调用计数与 token 用量：事实在模型层 logging 包装的 model.Callbacks 前后
//     回调（llm.go 的 logging 包装已逐候选尝试记日志），同一位置可推
//     observer；token 用量需框架 usage 字段随回调透出后接线；
//   - 压缩指标：框架 ContextCompactionStats 仅在 processor 内部 debug
//     日志，无结构化回调，接入需上游先暴露。

// RuntimeMetrics 构造即自持全部采集器；零值不可用，一律经 NewRuntimeMetrics。
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
// 装配省observer 时无需判空。
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
