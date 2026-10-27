package agent

import (
	"encoding/json"
	nethttp "net/http"
	"sync"

	"grape/internal/biz"
)

// AG-UI 运行入口的并发治理：对 chat 运行请求按租户/用户
// （userKey）限制同时在途的运行数，超限以 429 拒绝——单个失控客户端不
// 能占满框架运行面与 LLM 配额。身份解析失败 fail-closed 401（与 sessions
// / ack 守卫同一口径）。
//
// 取舍声明：限流只做「并发上限」单一维度，不做请求
// 速率（token bucket 之类）——运行请求的破坏面集中在长驻 SSE 流的并发
// 占用，速率突刺由运行超时（agent.agui.timeout）与 MaxToolIterations 收
// 敛；速率窗口的误拒复杂度在当前暴露面下不成比例，运维有实测需要再加。
//
// 计数为进程内内存态：多实例部署时上限是「每实例」上限而非全局上限，
// 全局并发的准确治理需外部协调面（如网关信号量），见部署文档。
//
// maxConcurrentRuns ≤ 0（conf 默认）时门控整体关闭：wrap 直接透传原
// handler（零包装零开销，行为与无本面完全一致，既有测试语义不变）。

// runLimitExceededMsg 是超限拒绝的固定文案：不回显 userKey 等内部细节。
const runLimitExceededMsg = "concurrent run limit exceeded"

// runGate 是按 userKey 的在途运行计数器与门控。observer 可为 nil（不观测，
// 行为不变）；非 nil 时占槽/释放配对推送活跃数、拒绝推送拒绝计数。
type runGate struct {
	limit    int
	observer biz.AgentRunObserver
	inflight map[string]int

	mu sync.Mutex
}

// newRunGate 构造门控；maxConcurrentRuns ≤ 0 返回 nil（关闭，不包装）。
func newRunGate(maxConcurrentRuns int32, observer biz.AgentRunObserver) *runGate {
	if maxConcurrentRuns <= 0 {
		return nil
	}
	return &runGate{limit: int(maxConcurrentRuns), observer: observer, inflight: make(map[string]int)}
}

// isChatRun 判定请求是否为 chat 运行入口：POST 打在前缀根（/agui/）。
// /history、/cancel、/sessions、事件窗口与 ack 确认路由都不是运行入口，
// 不受限也不计数。
func isChatRun(r *nethttp.Request, basePath string) bool {
	return r.Method == nethttp.MethodPost && r.URL.Path == basePath+"/"
}

// wrap 包装 AG-UI handler：非运行请求直接透传；运行请求先按身份计数
// 占槽，handler 返回（SSE 流收尾，含 panic）时释放。
func (g *runGate) wrap(basePath string, h nethttp.Handler) nethttp.Handler {
	if g == nil {
		return h
	}
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if !isChatRun(r, basePath) {
			h.ServeHTTP(w, r)
			return
		}
		userKey, err := userKeyFromContext(r.Context())
		if err != nil {
			// 身份缺失（绕过认证中间件的路径）fail-closed 401，不占槽。
			writeRunGateError(w, nethttp.StatusUnauthorized, err.Error())
			return
		}
		if !g.acquire(userKey) {
			if g.observer != nil {
				g.observer.RunRejected()
			}
			writeRunGateError(w, nethttp.StatusTooManyRequests, runLimitExceededMsg)
			return
		}
		if g.observer != nil {
			g.observer.RunActive(1)
		}
		defer func() {
			if g.observer != nil {
				g.observer.RunActive(-1)
			}
			g.release(userKey)
		}()
		h.ServeHTTP(w, r)
	})
}

// acquire 占一个该身份的在途槽；已满返回 false。
func (g *runGate) acquire(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inflight[key] >= g.limit {
		return false
	}
	g.inflight[key]++
	return true
}

// release 释放一个在途槽：归零即删键，计数器不随用户数无界增长。
func (g *runGate) release(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inflight[key] <= 1 {
		delete(g.inflight, key)
		return
	}
	g.inflight[key]--
}

// writeRunGateError 以 JSON 写出错误响应：message 字段形态与会话列表
// 端点一致（前端 toApiError 直读）。
func writeRunGateError(w nethttp.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
