package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"time"

	"grape/internal/biz"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
)

// 审计查询端点（供运维复现视图）的默认与上限分页
// 参数：口径与 biz 审计用例一致（默认 20、单页上限 200）。
const (
	agentAuditDefaultLimit = 20
	agentAuditMaxLimit     = 200
)

// AgentAuditService 承载 Agent 业务审计查询的手写端点（非 proto 生成）：
//
//	GET /v1/agent/audit/tool-calls                 工具调用审计列表
//	GET /v1/agent/audit/tool-calls?tool_call_id=…  按工具调用 ID 点查
//	GET /v1/agent/audit/confirmations              授权确认审计列表
//	GET /v1/agent/audit/confirmations?confirmation_id=…  按确认 ID 点查
//
// 点查走查询参数而非路径模板：手写端点取原始 *http.Request 形态（见
// routeHandler），不引入路径变量绑定机制，ID 经查询串传入语义等价且
// 少一个绑定面。
//
// 依赖只有 biz 审计用例——不含 internal/agent 与框架包，不复制
// agent_sessions.go 的受控例外。租户边界由仓储 IdentityTx 与表 RLS
// 强制（跨租户不可见等同不存在，404）；返回的摘要是落库时已脱敏的
// 形态（写入侧经 pkg/redact 注册表），本端点不二次脱敏也不回传原文。
type AgentAuditService struct {
	uc *biz.AuditUsecase
}

// NewAgentAuditService 构造审计查询端点服务。
func NewAgentAuditService(uc *biz.AuditUsecase) *AgentAuditService {
	return &AgentAuditService{uc: uc}
}

// RegisterHTTP 把审计查询路由注册到 Kratos HTTP 服务器。路由不在 agent
// 前缀内，与生成代码端点同享普通路由级超时；方法取 GET：只读语义；
// 操作名即路径模板，认证中间件按它判定非公开端点，缺令牌 fail-closed
// 返回 401（守卫测试见 server/agent_audit_route_test.go）。
func (s *AgentAuditService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, "/v1/agent/audit/tool-calls", s.routeHandler(s.toolCalls))
	route.Handle(nethttp.MethodGet, "/v1/agent/audit/confirmations", s.routeHandler(s.confirmations))
}

// routeHandler 把查询 handler 包成 Kratos 的 HandlerFunc：与 AgentService
// 的入链形态一致（ctx.Middleware 注入身份与链路事实）。桥接逻辑单独成
// 函数是因为 AgentService 的同名方法绑定在其类型上，本类型不与其耦合。
func (s *AgentAuditService) routeHandler(list func(*nethttp.Request, nethttp.ResponseWriter)) func(kratoshttp.Context) error {
	return func(ctx kratoshttp.Context) error {
		req, res := ctx.Request(), ctx.Response()
		inner := func(mctx context.Context, _ any) (any, error) {
			list(req.WithContext(mctx), res)
			return nil, nil
		}
		_, err := ctx.Middleware(middleware.Handler(inner))(ctx, nil)
		return err
	}
}

// auditToolCallItem 是工具调用审计项的响应 DTO：摘要是落库时已脱敏的
// 形态。时间以 UTC RFC3339 渲染。
type auditToolCallItem struct {
	ID               int64  `json:"id"`
	ToolCallID       string `json:"toolCallId"`
	ToolName         string `json:"toolName"`
	SessionID        string `json:"sessionId,omitempty"`
	TraceID          string `json:"traceId,omitempty"`
	ArgumentsSummary string `json:"argumentsSummary,omitempty"`
	ResultSummary    string `json:"resultSummary,omitempty"`
	RetryCount       int32  `json:"retryCount"`
	DurationMS       int64  `json:"durationMs"`
	Status           string `json:"status"`
	CalledAt         string `json:"calledAt"`
}

// auditConfirmationItem 是授权确认审计项的响应 DTO。
type auditConfirmationItem struct {
	ID             int64  `json:"id"`
	ConfirmationID string `json:"confirmationId"`
	SessionID      string `json:"sessionId"`
	ToolCallID     string `json:"toolCallId,omitempty"`
	ToolName       string `json:"toolName"`
	Decision       string `json:"decision"`
	Remember       string `json:"remember,omitempty"`
	Path           string `json:"path"`
	DecidedBy      string `json:"decidedBy"`
	TraceID        string `json:"traceId,omitempty"`
	DecidedAt      string `json:"decidedAt"`
}

// auditToolCallsReply 是工具调用审计列表响应体；items 恒非 nil。
type auditToolCallsReply struct {
	Items []auditToolCallItem `json:"items"`
	Total int64               `json:"total"`
}

// auditConfirmationsReply 是授权确认审计列表响应体；items 恒非 nil。
type auditConfirmationsReply struct {
	Items []auditConfirmationItem `json:"items"`
	Total int64                   `json:"total"`
}

// toolCalls 处理 GET /v1/agent/audit/tool-calls：带 tool_call_id 时点查
// （未命中 404），否则按会话过滤分页。分页参数非法 400；身份缺失（绕过
// 认证中间件的调用）fail-closed 401；用例的类型化错误按状态码映射。
func (s *AgentAuditService) toolCalls(r *nethttp.Request, w nethttp.ResponseWriter) {
	if id := r.URL.Query().Get("tool_call_id"); id != "" {
		if err := rejectMixedQuery(r, "tool_call_id"); err != nil {
			writeAgentAuditError(w, nethttp.StatusBadRequest, err)
			return
		}
		rec, err := s.uc.GetToolCall(r.Context(), id)
		if err != nil {
			s.writeAuditError(w, r, err)
			return
		}
		writeAgentAuditJSON(w, nethttp.StatusOK, auditToolCallItemOf(rec))
		return
	}
	filter, err := agentAuditFilterOf(r)
	if err != nil {
		writeAgentAuditError(w, nethttp.StatusBadRequest, err)
		return
	}
	list, err := s.uc.ListToolCalls(r.Context(), filter)
	if err != nil {
		s.writeAuditError(w, r, err)
		return
	}
	reply := auditToolCallsReply{Items: make([]auditToolCallItem, 0, len(list.Items)), Total: list.Total}
	for _, rec := range list.Items {
		reply.Items = append(reply.Items, auditToolCallItemOf(rec))
	}
	writeAgentAuditJSON(w, nethttp.StatusOK, reply)
}

// confirmations 处理 GET /v1/agent/audit/confirmations：带
// confirmation_id 时点查（未命中 404），否则按会话过滤分页。
func (s *AgentAuditService) confirmations(r *nethttp.Request, w nethttp.ResponseWriter) {
	if id := r.URL.Query().Get("confirmation_id"); id != "" {
		if err := rejectMixedQuery(r, "confirmation_id"); err != nil {
			writeAgentAuditError(w, nethttp.StatusBadRequest, err)
			return
		}
		rec, err := s.uc.GetConfirmation(r.Context(), id)
		if err != nil {
			s.writeAuditError(w, r, err)
			return
		}
		writeAgentAuditJSON(w, nethttp.StatusOK, auditConfirmationItemOf(rec))
		return
	}
	filter, err := agentAuditFilterOf(r)
	if err != nil {
		writeAgentAuditError(w, nethttp.StatusBadRequest, err)
		return
	}
	list, err := s.uc.ListConfirmations(r.Context(), filter)
	if err != nil {
		s.writeAuditError(w, r, err)
		return
	}
	reply := auditConfirmationsReply{Items: make([]auditConfirmationItem, 0, len(list.Items)), Total: list.Total}
	for _, rec := range list.Items {
		reply.Items = append(reply.Items, auditConfirmationItemOf(rec))
	}
	writeAgentAuditJSON(w, nethttp.StatusOK, reply)
}

// writeAuditError 把用例错误渲染为状态码：身份缺失（未认证）401、
// 入参非法 400、不存在或跨租户不可见 404；其余按内部错误处理——500
// 固定文案，细节只进服务端日志（与 agent_sessions.go 的口径一致）。
func (s *AgentAuditService) writeAuditError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	switch {
	case errors.Is(err, biz.ErrSessionInvalid):
		writeAgentAuditError(w, nethttp.StatusUnauthorized, err)
	case errors.Is(err, biz.ErrAuditInvalidArgument):
		writeAgentAuditError(w, nethttp.StatusBadRequest, err)
	case errors.Is(err, biz.ErrAuditNotFound):
		writeAgentAuditError(w, nethttp.StatusNotFound, err)
	default:
		slog.ErrorContext(r.Context(), "agent audit query failed", "error", err)
		writeAgentAuditJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
	}
}

// agentAuditFilterOf 解析分页与会话过滤参数：缺省 limit=20；负值、非
// 数字或超出上限一律 400（fail-closed，不静默截断）。
func agentAuditFilterOf(r *nethttp.Request) (*biz.AuditFilter, error) {
	f := &biz.AuditFilter{}
	if raw := r.URL.Query().Get("session_id"); raw != "" {
		f.SessionID = raw
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return nil, errors.New("offset must be a non-negative integer")
		}
		f.Offset = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		if parsed > agentAuditMaxLimit {
			return nil, errors.New("limit exceeds maximum")
		}
		f.Limit = parsed
	}
	if f.Limit == 0 {
		f.Limit = agentAuditDefaultLimit
	}
	return f, nil
}

// rejectMixedQuery 拒绝点查与列表参数混用：点查 ID 与 session_id /
// offset / limit 同时出现属歧义请求，显式 400 而非按优先级静默取舍。
func rejectMixedQuery(r *nethttp.Request, idParam string) error {
	q := r.URL.Query()
	for _, name := range []string{"session_id", "offset", "limit"} {
		if q.Get(name) != "" {
			return errors.New(idParam + " lookup does not accept " + name)
		}
	}
	return nil
}

// auditToolCallItemOf 渲染工具调用审计项。
func auditToolCallItemOf(rec *biz.ToolCallAudit) auditToolCallItem {
	return auditToolCallItem{
		ID:               rec.ID,
		ToolCallID:       rec.ToolCallID,
		ToolName:         rec.ToolName,
		SessionID:        rec.SessionID,
		TraceID:          rec.TraceID,
		ArgumentsSummary: rec.ArgumentsSummary,
		ResultSummary:    rec.ResultSummary,
		RetryCount:       rec.RetryCount,
		DurationMS:       rec.DurationMS,
		Status:           rec.Status,
		CalledAt:         rec.CalledAt.UTC().Format(time.RFC3339),
	}
}

// auditConfirmationItemOf 渲染授权确认审计项。
func auditConfirmationItemOf(rec *biz.ConfirmationAudit) auditConfirmationItem {
	return auditConfirmationItem{
		ID:             rec.ID,
		ConfirmationID: rec.ConfirmationID,
		SessionID:      rec.SessionID,
		ToolCallID:     rec.ToolCallID,
		ToolName:       rec.ToolName,
		Decision:       rec.Decision,
		Remember:       rec.Remember,
		Path:           rec.Path,
		DecidedBy:      rec.DecidedBy,
		TraceID:        rec.TraceID,
		DecidedAt:      rec.DecidedAt.UTC().Format(time.RFC3339),
	}
}

// writeAgentAuditJSON 以 UTF-8 JSON 写出响应（形态对齐 ackhttp）。
func writeAgentAuditJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAgentAuditError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐。
func writeAgentAuditError(w nethttp.ResponseWriter, status int, err error) {
	writeAgentAuditJSON(w, status, map[string]string{"message": err.Error()})
}
