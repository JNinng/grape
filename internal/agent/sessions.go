package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// sessionListDefaultLimit / sessionListMaxLimit 是 agent 会话列表端点
// （A2）的默认与上限分页参数：条数上限防止单请求拖回全量会话；
// offset/limit 语义对齐框架 ListSessions 的 offset 分页。
const (
	sessionListDefaultLimit = 20
	sessionListMaxLimit     = 100
)

// sessionList 是 agent 会话列表端点的装配物：消费骨架既有的框架
// session.Service 与 app 名（与 AG-UI / ack 同一实例、同一命名空间），
// 归属过滤以认证身份经 EncodeUserKey 编码的 userKey 为唯一来源，不信任
// 任何请求侧归属参数。装配期随 Stack 构造（NewStackWithOptions），HTTP
// 形态经 Stack.SessionsHandler 交 service 注册进 Kratos 链。
type sessionList struct {
	sessions session.Service
	appName  string
}

// newSessionList 构造会话列表装配物。
func newSessionList(sessions session.Service, appName string) *sessionList {
	return &sessionList{sessions: sessions, appName: appName}
}

// sessionMeta 是会话列表项的元数据投影：不含事件与轨迹（框架
// WithListSessionOnlyMeta 语义）。
type sessionMeta struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// listSessions 是会话列表的程序化取用点：按 userKey 列出会话元数据
// （框架 WithListSessionOnlyMeta + offset 分页）。HTTP 形态与后续非
// HTTP 消费方都经此取用，不直触框架会话存储。
func (s *sessionList) listSessions(ctx context.Context, userKey string, offset, limit int) ([]sessionMeta, error) {
	sessions, err := s.sessions.ListSessions(ctx,
		session.UserKey{AppName: s.appName, UserID: userKey},
		session.WithListSessionOnlyMeta(),
		session.WithListSessionPage(offset, limit),
	)
	if err != nil {
		return nil, err
	}
	metas := make([]sessionMeta, 0, len(sessions))
	for _, sess := range sessions {
		metas = append(metas, sessionMeta{
			ID:        sess.ID,
			CreatedAt: sess.CreatedAt,
			UpdatedAt: sess.UpdatedAt,
		})
	}
	return metas, nil
}

// sessionItem 是会话列表项的响应 DTO：时间以 UTC RFC3339 渲染，前端按
// 本地时区展示。
type sessionItem struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// sessionListReply 是会话列表响应体。sessions 恒非 nil，空会话返回空
// 数组而非 null，前端无需判空分支。
type sessionListReply struct {
	Sessions []sessionItem `json:"sessions"`
}

// ServeHTTP 处理 GET {basePath}/sessions（路由注册与入链归 service，
// 精确路径先于 agent 前缀通配）：分页校验 → 认证身份 → userKey →
// 框架 ListSessions（仅元数据 + offset 分页）。身份解析与 ack 守卫同走
// userKeyFromContext 单一代码点；身份缺失（绕过认证中间件的调用）
// fail-closed 返回 401，与 ackOwnerGuard 的绕过口径一致。分页参数非法
// 返回 400。
func (s *sessionList) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	offset, limit, err := sessionListPageOf(r)
	if err != nil {
		writeSessionListError(w, nethttp.StatusBadRequest, err)
		return
	}
	userKey, err := userKeyFromContext(r.Context())
	if err != nil {
		writeSessionListError(w, nethttp.StatusUnauthorized, err)
		return
	}
	metas, err := s.listSessions(r.Context(), userKey, offset, limit)
	if err != nil {
		// 500 固定文案：内部错误细节（驱动/SQL 文本）只进服务端日志，
		// 不透传给已认证客户端（与生成端点的 reason 枚举语义对齐）。
		slog.ErrorContext(r.Context(), "list agent sessions failed", "error", err)
		writeSessionListJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		return
	}
	reply := sessionListReply{Sessions: make([]sessionItem, 0, len(metas))}
	for _, meta := range metas {
		reply.Sessions = append(reply.Sessions, sessionItem{
			ID:        meta.ID,
			CreatedAt: meta.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: meta.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeSessionListJSON(w, nethttp.StatusOK, reply)
}

// sessionListPageOf 解析 offset/limit 查询参数：缺省 offset=0、
// limit=20；负值、非数字或超出上限一律拒绝（fail-closed，不静默截断），
// 避免「看起来成功但结果被裁剪」的语义漂移。
func sessionListPageOf(r *nethttp.Request) (int, int, error) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	limit := sessionListDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		if parsed > sessionListMaxLimit {
			return 0, 0, errors.New("limit exceeds maximum")
		}
		limit = parsed
	}
	return offset, limit, nil
}

// writeSessionListJSON 以 UTF-8 JSON 写出响应（形态对齐 ackhttp）。
func writeSessionListJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeSessionListError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐，状态码之外的文案可直达 UI。
func writeSessionListError(w nethttp.ResponseWriter, status int, err error) {
	writeSessionListJSON(w, status, map[string]string{"message": err.Error()})
}
