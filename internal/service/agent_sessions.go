package service

import (
	context "context"
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"time"

	"grape/internal/agent"
	"grape/internal/biz"
	"grape/internal/conf"

	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// agent 会话列表端点（TODO 流 A 的 A2）的默认与上限分页参数：条数上限
// 防止单请求拖回全量会话；offset/limit 语义对齐框架 ListSessions 的
// offset 分页。
const (
	agentSessionsDefaultLimit = 20
	agentSessionsMaxLimit     = 100
)

// AgentSessionsService 承载 agent 会话列表手写端点（非 proto 生成，
// A2 独占新文件）：GET {basePath}/sessions 列出当前认证用户的 agent
// 会话元数据，供前端对话页的会话切换使用。会话范围以服务端认证身份
// 编码的 userKey 为唯一来源（agent.EncodeUserKey），不信任任何请求侧
// 归属参数，跨用户天然不可见。
//
// import 边界说明：分层表约定 service 手写端点只额外导入 transport/http
// 与 agent；本文件额外导入框架 session 包与 conf，是与 C1 并行开发窗口
// 的受控例外——该窗口内 internal/agent 不允许变更，无法新增框架取用点，
// 本端点只消费 wire 既有 provider 形态（agent.NewSessionService 提供的
// session.Service 实例与 *conf.Agent），userKey 编码复用 agent 包的
// 单一代码点，不重建编码。C1 并入后应把列表能力收进 internal/agent，
// 届时归还 import 边界。
type AgentSessionsService struct {
	sessions session.Service
	appName  string
	basePath string
}

// NewAgentSessionsService 构造 agent 会话列表端点服务：会话存储即 wire
// 装配的框架 session.Service（与 AG-UI / ack 同一实例），应用名与路由
// 前缀取自既有装配物，保证与 AG-UI 会话的命名空间一致。
func NewAgentSessionsService(sessions session.Service, c *conf.Agent, agentSvc *AgentService) *AgentSessionsService {
	return &AgentSessionsService{
		sessions: sessions,
		appName:  c.GetAppName(),
		basePath: agentSvc.BasePath(),
	}
}

// RegisterHTTP 把会话列表路由注册到 Kratos HTTP 服务器。必须先于
// AgentService 的前缀通配路由注册（见 server.NewHTTPServer 的注册顺
// 序）：gorilla/mux 按注册顺序匹配，/agui/sessions 精确路径只有排在
// /agui/{rest:.*} 之前才会命中本端点。方法取 GET：只读语义；操作名即
// 路径模板，认证中间件按它判定非公开端点，缺令牌 fail-closed 返回 401。
func (s *AgentSessionsService) RegisterHTTP(srv *kratoshttp.Server) {
	route := srv.Route("/")
	route.Handle(nethttp.MethodGet, s.basePath+"/sessions", s.routeHandler(s.list))
}

// routeHandler 把列表 handler 包成 Kratos 的 HandlerFunc：与 AgentService
// 的入链形态一致（ctx.Middleware 注入身份与链路事实），桥接逻辑单独成
// 函数是因为 AgentService 的同名方法绑定在其类型上，本类型不与其耦合。
// Kratos 的 wrapper 不暴露请求替换点，中间件链产出的 ctx 以显式派生
// 请求传入 handler，handler 不再经 wrapper 读原始请求。
func (s *AgentSessionsService) routeHandler(list func(*nethttp.Request, nethttp.ResponseWriter)) func(kratoshttp.Context) error {
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

// agentSessionItem 是会话列表项的响应 DTO：仅元数据（框架
// WithListSessionOnlyMeta 语义），不含事件与轨迹。时间以 UTC RFC3339
// 渲染，前端按本地时区展示。
type agentSessionItem struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// agentSessionsReply 是会话列表响应体。sessions 恒非 nil，空会话返回
// 空数组而非 null，前端无需判空分支。
type agentSessionsReply struct {
	Sessions []agentSessionItem `json:"sessions"`
}

// list 处理 GET {basePath}/sessions：认证身份 → userKey → 框架
// ListSessions（仅元数据 + offset 分页）。分页参数非法返回 400；身份
// 缺失（绕过认证中间件的调用）fail-closed 返回 401。
func (s *AgentSessionsService) list(r *nethttp.Request, w nethttp.ResponseWriter) {
	offset, limit, err := agentSessionsPageOf(r)
	if err != nil {
		writeAgentSessionsError(w, nethttp.StatusBadRequest, err)
		return
	}
	identity, ok := biz.IdentityFromContext(r.Context())
	if !ok {
		writeAgentSessionsError(w, nethttp.StatusUnauthorized, errors.New("missing identity"))
		return
	}
	userKey, err := agent.EncodeUserKey(identity.TenantID, identity.UserID)
	if err != nil {
		writeAgentSessionsError(w, nethttp.StatusUnauthorized, err)
		return
	}
	sessions, err := s.sessions.ListSessions(r.Context(),
		session.UserKey{AppName: s.appName, UserID: userKey},
		session.WithListSessionOnlyMeta(),
		session.WithListSessionPage(offset, limit),
	)
	if err != nil {
		// 500 固定文案：内部错误细节（驱动/SQL 文本）只进服务端日志，
		// 不透传给已认证客户端（与生成端点的 reason 枚举语义对齐）。
		slog.ErrorContext(r.Context(), "list agent sessions failed", "error", err)
		writeAgentSessionsJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		return
	}
	reply := agentSessionsReply{Sessions: make([]agentSessionItem, 0, len(sessions))}
	for _, sess := range sessions {
		reply.Sessions = append(reply.Sessions, agentSessionItem{
			ID:        sess.ID,
			CreatedAt: sess.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: sess.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeAgentSessionsJSON(w, nethttp.StatusOK, reply)
}

// agentSessionsPageOf 解析 offset/limit 查询参数：缺省 offset=0、
// limit=20；负值、非数字或超出上限一律拒绝（fail-closed，不静默截断），
// 避免「看起来成功但结果被裁剪」的语义漂移。
func agentSessionsPageOf(r *nethttp.Request) (int, int, error) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	limit := agentSessionsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if parsed > agentSessionsMaxLimit {
			return 0, 0, errors.New("limit exceeds maximum")
		}
		limit = parsed
	}
	return offset, limit, nil
}

// writeAgentSessionsJSON 以 UTF-8 JSON 写出响应（形态对齐 ackhttp）。
func writeAgentSessionsJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeAgentSessionsError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐，状态码之外的文案可直达 UI。
func writeAgentSessionsError(w nethttp.ResponseWriter, status int, err error) {
	writeAgentSessionsJSON(w, status, map[string]string{"message": err.Error()})
}
