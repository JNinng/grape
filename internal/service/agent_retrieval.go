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

// 检索留痕查询端点（供运维复现视图）的默认与上限
// 分页参数：口径与 biz 留痕用例一致（默认 20、单页上限 200）。
const (
	agentRetrievalDefaultLimit = 20
	agentRetrievalMaxLimit     = 200
)

// AgentRetrievalService 承载检索留痕查询的手写端点（非 proto 生成）：
//
//	GET /v1/agent/retrieval-logs                    检索留痕列表
//	GET /v1/agent/retrieval-logs?session_id=…       按会话过滤
//	GET /v1/agent/retrieval-logs?source=tool|pipeline  按来源过滤
//
// 依赖只有 biz 留痕用例——不含 internal/agent 与框架包，遵守审计
// 端点的同款纪律。租户边界由仓储 IdentityTx 与表 RLS 强制（跨租户不可
// 见等同不存在）；返回的 query_text / 过滤摘要 / 命中是落库时已脱敏的
// 形态（写入侧经 pkg/redact 注册表），本端点不二次脱敏也不回传原文。
type AgentRetrievalService struct {
	uc *biz.RetrievalLogUsecase
}

// NewAgentRetrievalService 构造检索留痕查询端点服务。
func NewAgentRetrievalService(uc *biz.RetrievalLogUsecase) *AgentRetrievalService {
	return &AgentRetrievalService{uc: uc}
}

// RegisterHTTP 把检索留痕查询路由注册到 Kratos HTTP 服务器。路由不在
// agent 前缀内，与生成代码端点同享普通路由级超时；方法取 GET：只读
// 语义；操作名即路径模板，认证中间件按它判定非公开端点，缺令牌
// fail-closed 返回 401（守卫测试见 server/agent_retrieval_route_test.go）。
func (s *AgentRetrievalService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, "/v1/agent/retrieval-logs", s.routeHandler(s.retrievalLogs))
}

// routeHandler 把查询 handler 包成 Kratos 的 HandlerFunc：与 AgentService
// 的入链形态一致（ctx.Middleware 注入身份与链路事实）。
func (s *AgentRetrievalService) routeHandler(list func(*nethttp.Request, nethttp.ResponseWriter)) func(kratoshttp.Context) error {
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

// retrievalLogItem 是检索留痕项的响应 DTO：query_text 是落库时已脱敏的
// 形态；过滤摘要与命中以落库的 JSON 形态原样透传（证据面渲染），
// 落库为 NULL 的两列渲染为 null。时间以 UTC RFC3339 渲染。
type retrievalLogItem struct {
	ID            int64           `json:"id"`
	Source        string          `json:"source"`
	Status        string          `json:"status"`
	QueryText     string          `json:"queryText,omitempty"`
	SessionID     string          `json:"sessionId,omitempty"`
	TraceID       string          `json:"traceId,omitempty"`
	RequestedTopK *int32          `json:"requestedTopK"`
	EffectiveTopK *int32          `json:"effectiveTopK"`
	FilterSummary json.RawMessage `json:"filterSummary"`
	Hits          json.RawMessage `json:"hits"`
	RerankBefore  string          `json:"rerankBefore,omitempty"`
	RerankAfter   string          `json:"rerankAfter,omitempty"`
	DurationMS    int64           `json:"durationMs"`
	CreatedAt     string          `json:"createdAt"`
}

// retrievalLogsReply 是检索留痕列表响应体；items 恒非 nil。
type retrievalLogsReply struct {
	Items []retrievalLogItem `json:"items"`
	Total int64              `json:"total"`
}

// retrievalLogs 处理 GET /v1/agent/retrieval-logs：按会话/来源过滤分页。
// 分页与来源参数非法 400；身份缺失（绕过认证中间件的调用）fail-closed
// 401；存储故障 500 固定文案，细节只进服务端日志。
func (s *AgentRetrievalService) retrievalLogs(r *nethttp.Request, w nethttp.ResponseWriter) {
	filter, err := agentRetrievalFilterOf(r)
	if err != nil {
		writeAgentRetrievalError(w, nethttp.StatusBadRequest, err)
		return
	}
	list, err := s.uc.List(r.Context(), filter)
	if err != nil {
		switch {
		case errors.Is(err, biz.ErrSessionInvalid):
			writeAgentRetrievalError(w, nethttp.StatusUnauthorized, err)
		case errors.Is(err, biz.ErrRetrievalLogInvalidArgument):
			writeAgentRetrievalError(w, nethttp.StatusBadRequest, err)
		default:
			slog.ErrorContext(r.Context(), "agent retrieval log query failed", "error", err)
			writeAgentRetrievalJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		}
		return
	}
	reply := retrievalLogsReply{Items: make([]retrievalLogItem, 0, len(list.Items)), Total: list.Total}
	for _, rec := range list.Items {
		reply.Items = append(reply.Items, retrievalLogItemOf(rec))
	}
	writeAgentRetrievalJSON(w, nethttp.StatusOK, reply)
}

// agentRetrievalFilterOf 解析分页、会话与来源过滤参数：缺省 limit=20；
// 负值、非数字或超出上限一律 400（fail-closed，不静默截断）；source 取
// 值合法性由 biz 用例校验，此处透传。
func agentRetrievalFilterOf(r *nethttp.Request) (*biz.RetrievalLogFilter, error) {
	f := &biz.RetrievalLogFilter{}
	if raw := r.URL.Query().Get("session_id"); raw != "" {
		f.SessionID = raw
	}
	if raw := r.URL.Query().Get("source"); raw != "" {
		f.Source = raw
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
		if parsed > agentRetrievalMaxLimit {
			return nil, errors.New("limit exceeds maximum")
		}
		f.Limit = parsed
	}
	if f.Limit == 0 {
		f.Limit = agentRetrievalDefaultLimit
	}
	return f, nil
}

// retrievalLogItemOf 渲染检索留痕项：top_k 两值以指针保留「未指定 /
// 不适用」（NULL）与 0 的区分。
func retrievalLogItemOf(rec *biz.RetrievalLog) retrievalLogItem {
	item := retrievalLogItem{
		ID:            rec.ID,
		Source:        rec.Source,
		Status:        rec.Status,
		QueryText:     rec.QueryText,
		SessionID:     rec.SessionID,
		TraceID:       rec.TraceID,
		FilterSummary: rec.FilterSummary,
		Hits:          rec.Hits,
		RerankBefore:  rec.RerankBefore,
		RerankAfter:   rec.RerankAfter,
		DurationMS:    rec.DurationMS,
		CreatedAt:     rec.CreatedAt.UTC().Format(time.RFC3339),
	}
	if rec.RequestedTopK != 0 {
		v := rec.RequestedTopK
		item.RequestedTopK = &v
	}
	if rec.EffectiveTopK != 0 {
		v := rec.EffectiveTopK
		item.EffectiveTopK = &v
	}
	return item
}

// writeAgentRetrievalJSON 以 UTF-8 JSON 写出响应（形态对齐 ackhttp）。
func writeAgentRetrievalJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAgentRetrievalError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐。
func writeAgentRetrievalError(w nethttp.ResponseWriter, status int, err error) {
	writeAgentRetrievalJSON(w, status, map[string]string{"message": err.Error()})
}
