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

// 模型调用留痕查询端点的分页缺省值与上限，口径与 biz 留痕用例一致。
const (
	agentModelCallDefaultLimit = 20
	agentModelCallMaxLimit     = 200
)

// AgentModelCallService 承接模型调用留痕查询的手写 HTTP 端点，未纳入
// proto 契约：按场景/模型键/状态/文档/时间范围过滤分页。依赖只有 biz
// 留痕用例；租户边界由仓储 IdentityTx 与表 RLS 强制，跨租户不可见等同
// 不存在；输入/输出摘要与错误为落库时已脱敏形态，本层不二次脱敏、不
// 回传原文。
type AgentModelCallService struct {
	uc *biz.ModelCallLogUsecase
}

// NewAgentModelCallService 构造模型调用留痕查询端点服务。
func NewAgentModelCallService(uc *biz.ModelCallLogUsecase) *AgentModelCallService {
	return &AgentModelCallService{uc: uc}
}

// RegisterHTTP 把模型调用留痕查询路由注册到 Kratos HTTP 服务器。路由不在
// agent 前缀内，与生成代码端点同享普通路由级超时；方法取 GET：只读
// 语义；操作名即路径模板，认证中间件按它判定非公开端点，缺令牌
// fail-closed 返回 401（守卫测试见 server/agent_model_call_route_test.go）。
func (s *AgentModelCallService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, "/v1/agent/model-calls", s.routeHandler(s.modelCalls))
}

// routeHandler 把查询 handler 包成 Kratos 的 HandlerFunc：与 AgentService
// 的入链形态一致（ctx.Middleware 注入身份与链路事实）。
func (s *AgentModelCallService) routeHandler(list func(*nethttp.Request, nethttp.ResponseWriter)) func(kratoshttp.Context) error {
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

// modelCallItem 是模型调用留痕项的响应 DTO：input/output 摘要与错误是
// 落库时已脱敏的形态；token 与文档 ID 以指针保留「未上报 / 无文档事实」
// （NULL）与 0 的区分。时间以 UTC RFC3339 渲染。
type modelCallItem struct {
	ID            int64  `json:"id"`
	Scenario      string `json:"scenario"`
	Status        string `json:"status"`
	ModelKey      string `json:"modelKey,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	DocumentID    *int64 `json:"documentId"`
	TraceID       string `json:"traceId,omitempty"`
	InputSummary  string `json:"inputSummary,omitempty"`
	OutputSummary string `json:"outputSummary,omitempty"`
	InputTokens   *int32 `json:"inputTokens"`
	OutputTokens  *int32 `json:"outputTokens"`
	DurationMS    int64  `json:"durationMs"`
	Error         string `json:"error,omitempty"`
	CreatedAt     string `json:"createdAt"`
}

// modelCallsReply 是模型调用留痕列表响应体；items 恒非 nil。
type modelCallsReply struct {
	Items []modelCallItem `json:"items"`
	Total int64           `json:"total"`
}

// modelCalls 处理 GET /v1/agent/model-calls：按过滤条件分页。分页与过滤
// 参数非法 400；身份缺失（绕过认证中间件的调用）fail-closed 401；存储
// 故障 500 固定文案，细节只进服务端日志。
func (s *AgentModelCallService) modelCalls(r *nethttp.Request, w nethttp.ResponseWriter) {
	filter, err := agentModelCallFilterOf(r)
	if err != nil {
		writeAgentModelCallError(w, nethttp.StatusBadRequest, err)
		return
	}
	list, err := s.uc.List(r.Context(), filter)
	if err != nil {
		switch {
		case errors.Is(err, biz.ErrSessionInvalid):
			writeAgentModelCallError(w, nethttp.StatusUnauthorized, err)
		case errors.Is(err, biz.ErrModelCallInvalidArgument):
			writeAgentModelCallError(w, nethttp.StatusBadRequest, err)
		default:
			slog.ErrorContext(r.Context(), "agent model call log query failed", "error", err)
			writeAgentModelCallJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		}
		return
	}
	reply := modelCallsReply{Items: make([]modelCallItem, 0, len(list.Items)), Total: list.Total}
	for _, rec := range list.Items {
		reply.Items = append(reply.Items, modelCallItemOf(rec))
	}
	writeAgentModelCallJSON(w, nethttp.StatusOK, reply)
}

// agentModelCallFilterOf 解析分页与过滤参数：缺省 limit=20；负值、非数字
// 或超出上限一律 400（fail-closed，不静默截断）；场景与状态取值合法性由
// biz 用例校验，此处透传；时间范围为 RFC3339（含时区偏移），解析失败
// 400。
func agentModelCallFilterOf(r *nethttp.Request) (*biz.ModelCallFilter, error) {
	f := &biz.ModelCallFilter{}
	if raw := r.URL.Query().Get("scenario"); raw != "" {
		f.Scenario = raw
	}
	if raw := r.URL.Query().Get("model_key"); raw != "" {
		f.ModelKey = raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		f.Status = raw
	}
	if raw := r.URL.Query().Get("document_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			return nil, errors.New("document_id must be a non-negative integer")
		}
		f.DocumentID = parsed
	}
	if raw := r.URL.Query().Get("created_from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, errors.New("created_from must be an RFC3339 timestamp")
		}
		f.CreatedFrom = parsed
	}
	if raw := r.URL.Query().Get("created_to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, errors.New("created_to must be an RFC3339 timestamp")
		}
		f.CreatedTo = parsed
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
		if parsed > agentModelCallMaxLimit {
			return nil, errors.New("limit exceeds maximum")
		}
		f.Limit = parsed
	}
	if f.Limit == 0 {
		f.Limit = agentModelCallDefaultLimit
	}
	return f, nil
}

// modelCallItemOf 渲染模型调用留痕项：token 与文档 ID 以指针保留 NULL
// （未上报 / 无文档事实）与 0 的区分。
func modelCallItemOf(rec *biz.ModelCallLog) modelCallItem {
	item := modelCallItem{
		ID:            rec.ID,
		Scenario:      rec.Scenario,
		Status:        rec.Status,
		ModelKey:      rec.ModelKey,
		Endpoint:      rec.Endpoint,
		TraceID:       rec.TraceID,
		InputSummary:  rec.InputSummary,
		OutputSummary: rec.OutputSummary,
		DurationMS:    rec.DurationMS,
		Error:         rec.Error,
		CreatedAt:     rec.CreatedAt.UTC().Format(time.RFC3339),
	}
	if rec.DocumentID != 0 {
		v := rec.DocumentID
		item.DocumentID = &v
	}
	if rec.InputTokens != 0 {
		v := rec.InputTokens
		item.InputTokens = &v
	}
	if rec.OutputTokens != 0 {
		v := rec.OutputTokens
		item.OutputTokens = &v
	}
	return item
}

// writeAgentModelCallJSON 以 UTF-8 JSON 写出响应（形态对齐检索留痕端点）。
func writeAgentModelCallJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAgentModelCallError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐。
func writeAgentModelCallError(w nethttp.ResponseWriter, status int, err error) {
	writeAgentModelCallJSON(w, status, map[string]string{"message": err.Error()})
}
