package biz

import (
	"time"
)

// Agent 运行面的观测契约：由 agent 框架适配层在运行
// 起止、限流拒绝与工具调用终态处调用，实现方（internal/ops 的
// RuntimeMetrics）落 Prometheus 指标，与设施诊断同一 /metrics 出口输出。
// 依赖反转接缝与 FacilityDiagnostics 同型：biz 声明契约、事实产生方调用、
// ops 出口消费，biz 不感知 Prometheus 类型。
//
// 实现必须非阻塞、不得 panic：观测面是旁路旁听，调用侧不为其兜底
// （与审计挂点同一纪律）。nil 语义由调用侧保证——agent 层 nil 即跳过。

// AgentRunObserver 是 Agent 运行面事实的观察者。
type AgentRunObserver interface {
	// RunActive 调整当前活跃 SSE 运行数（delta ±1，配对调用）。
	RunActive(delta int)
	// RunRejected 记一次因并发上限被拒绝的运行请求。
	RunRejected()
	// ToolCallObserved 记一次工具调用终态：tool 是工具名，status 取
	// ToolCallStatusSuccess / ToolCallStatusError，d 是执行耗时。
	ToolCallObserved(tool string, status string, d time.Duration)
}
