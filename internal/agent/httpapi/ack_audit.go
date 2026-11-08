package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"

	"grape/internal/agent/audit"
	"grape/internal/agent/identity"
	"grape/internal/biz"

	"github.com/jninng/trpc_agent_go_ack"
)

// ack 决定路由审计：ackhttp.Mount 收具体网关、无法装饰，故在唯一挂载点
// 对 POST .../confirmation 做 HTTP 面拦截——读请求得决定事实、读 200
// 响应得结算路径后写确认行；恢复路径的代执行行由捕获器归集并组合批量。
// 归属守卫在外层，跨用户决定到不了这里；待确认负载在结算前经 Pending 取。

// ackDecisionBody 是决定请求体的审计视角投影：与 ackhttp 的解码契约
// 一致（json.NewDecoder 只取首个 JSON 值、容忍尾随数据，与归属守卫的
// 取值方式同一纪律）。
type ackDecisionBody struct {
	ThreadID       string `json:"threadId"`
	ConfirmationID string `json:"confirmationId"`
	Approved       bool   `json:"approved"`
	Remember       string `json:"remember,omitempty"`
}

// AckAuditGuard 是决定路由的审计拦截器。
type AckAuditGuard struct {
	inner   nethttp.Handler
	gateway *ack.Gateway
	hook    audit.Hook
	logger  *slog.Logger
	// route 是被拦截的决定路由完整路径（{basePath}{ackSuffix}/confirmation）。
	route string
}

// NewAckAuditGuard 构造决定审计拦截器。
func NewAckAuditGuard(inner nethttp.Handler, gateway *ack.Gateway, hook audit.Hook, logger *slog.Logger, route string) *AckAuditGuard {
	return &AckAuditGuard{inner: inner, gateway: gateway, hook: hook, logger: logger, route: route}
}

func (g *AckAuditGuard) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodPost || r.URL.Path != g.route {
		g.inner.ServeHTTP(w, r)
		return
	}
	// 预读并恢复决定体（外层守卫已限宽过一次）；读失败直接 400——请求体
	// 已耗尽，ackhttp 对同一输入同样失败，且形状非法无结算、无需审计。
	body, err := io.ReadAll(nethttp.MaxBytesReader(w, r.Body, ackGuardMaxBodyBytes))
	if err != nil {
		writeAckGuardError(w, nethttp.StatusBadRequest, "unreadable request body")
		return
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	var decided ackDecisionBody
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&decided); err != nil {
		// 解码失败放行：ackhttp 对同一输入同样失败，以 400 拒绝，无结算。
		g.inner.ServeHTTP(w, r)
		return
	}
	if decided.ThreadID == "" || decided.ConfirmationID == "" {
		g.inner.ServeHTTP(w, r)
		return
	}

	// 结算前取待确认负载：工具名与工具调用 ID 是确认审计行的必填事实。
	payload := g.lookupPayload(r, decided.ThreadID, decided.ConfirmationID)

	// 恢复路径代执行的归集：确认 ID 即工具调用 ID（ack Payload 两字段
	// 同源），注入执行 ctx 后，代执行经审计 wrapper 以它命中捕获器；
	// 续跑运行内框架会为后续调用注入新 ID，不被捕获、仍走直写。
	capture := audit.NewRecoveryCapture(decided.ConfirmationID, decided.ThreadID)
	ctx := audit.ContextWithCapture(r.Context(), capture)

	rec := &ackAuditRecorder{ResponseWriter: w, code: nethttp.StatusOK}
	g.inner.ServeHTTP(rec, r.WithContext(ctx))
	if rec.code != nethttp.StatusOK {
		// 无结算（400/404/409…）：并发决定后到者、跨用户拒绝、形状非法
		// 都在此，未生效的决定不入审计。
		return
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &result); err != nil {
		g.logger.Warn("decide response unreadable, skip confirmation audit", "error", err)
		return
	}
	g.record(ctx, &decided, payload, result.Path, capture)
}

// lookupPayload 结算前查待确认负载：Pending 合并存活等待者与会话状态
// 持久化指针，Decide 能结算的确认必然在此。查询失败按无负载处理，由
// record 记日志放弃确认行（审计旁路，不阻断决定）。
func (g *AckAuditGuard) lookupPayload(r *nethttp.Request, threadID, confirmationID string) *ack.Payload {
	items, err := g.gateway.Pending(r.Context(), threadID)
	if err != nil {
		g.logger.Warn("list pending confirmations failed, confirmation audit may be skipped",
			"sessionId", threadID, "error", err)
		return nil
	}
	for i := range items {
		if items[i].ConfirmationID == confirmationID {
			p := items[i].Payload
			return &p
		}
	}
	return nil
}

// record 结算成功后写确认审计行：live 只写确认行（工具行由终态 wrapper
// 自然落库）；recovery 的代执行行由捕获器归集，与确认行组合批量写入
// （无代执行则单边批量）。deny 归一 Remember=""；decided_by 取网关回填的
// userKey。
func (g *AckAuditGuard) record(ctx context.Context, decided *ackDecisionBody, payload *ack.Payload, path string, capture *audit.RecoveryCapture) {
	decidedBy, err := identity.UserKeyFromContext(ctx)
	if err != nil {
		// 认证链正常时不可达（外层中间件 401、归属守卫 404 已先行）；
		// 无决定人无法构造合法确认行，记日志放弃。
		g.logger.Warn("missing identity for decide audit, skip confirmation audit",
			"sessionId", decided.ThreadID, "confirmationId", decided.ConfirmationID, "error", err)
		return
	}
	if payload == nil || payload.ToolName == "" {
		// 确认行事实不可得时放弃确认行，但已归集的代执行工具行必须单边
		// 落库：它是恢复路径执行证据的唯一通路，不因预读失败连坐丢失。
		if tool := capture.Take(); tool != nil {
			audit.WriteAudit(ctx, g.logger, "record recovered tool call", func(wctx context.Context) error {
				return g.hook.RecordToolCallWithConfirmation(wctx, tool, nil)
			})
			return
		}
		g.logger.Warn("pending payload unavailable, skip confirmation audit",
			"sessionId", decided.ThreadID, "confirmationId", decided.ConfirmationID)
		return
	}
	decision := biz.ConfirmationDecisionDenied
	remember := ""
	if decided.Approved {
		decision = biz.ConfirmationDecisionApproved
		remember = string(decided.Remember)
	}
	conf := &biz.ConfirmationRecordInput{
		ConfirmationID: decided.ConfirmationID,
		SessionID:      decided.ThreadID,
		ToolCallID:     payload.ToolCallID,
		ToolName:       payload.ToolName,
		Decision:       decision,
		Remember:       remember,
		Path:           path,
		DecidedBy:      decidedBy,
	}
	if path == ack.PathLive {
		audit.WriteAudit(ctx, g.logger, "record confirmation", func(wctx context.Context) error {
			return g.hook.RecordConfirmation(wctx, conf)
		})
		return
	}
	audit.WriteAudit(ctx, g.logger, "record confirmation with tool call", func(wctx context.Context) error {
		return g.hook.RecordToolCallWithConfirmation(wctx, capture.Take(), conf)
	})
}

// ackAuditRecorder 捕获决定路由的响应状态与响应体：生效路径在 200 响应
// 体里。仅用于该单条 JSON 路由，无流式需求。
type ackAuditRecorder struct {
	nethttp.ResponseWriter
	code int
	body bytes.Buffer
}

func (r *ackAuditRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *ackAuditRecorder) Write(p []byte) (int, error) {
	r.body.Write(p)
	return r.ResponseWriter.Write(p)
}
