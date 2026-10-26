package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"time"

	"grape/internal/biz"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/trpc_agent_go_ack"
)

// storedGrants 是许可源的真实现（C2，替换 C1 的 noopGrants）：ack 的
// string UserID 是 userKey（tenant_id:user_id），经 DecodeUserKey 映射
// 为内部 (tenant_id, user_id) 后交 biz.GrantUsecase 落 agent_tool_grant
// 表（经 IdentityTx 走 RLS；仓储查询另显式携带租户过滤，RLS 关闭时
// 跨租户仍不可见）。Lookup 的「会话级先于全局级」次序由用例层的
// Resolve 显式编排（对齐 ack grants.go memoryGrants 的既有语义）。
//
// 错误姿态：许可只能把 confirm 层级降级为放行，因此一切失败（身份
// 解码失败、存储故障）都按「未命中」处理——确认照常发起，fail-closed
// 的代价只是重复确认，绝不因故障静默放行（与 ack 对策略源故障升级
// confirm 的方向一致）。存储故障记日志便于排障。
type storedGrants struct {
	grants *biz.GrantUsecase
}

// newStoredGrants 构造许可源适配器。
func newStoredGrants(grants *biz.GrantUsecase) *storedGrants {
	return &storedGrants{grants: grants}
}

// Lookup 实现 ack.GrantSource：userKey 解码失败按未命中处理（畸形身份
// 不可能持有许可，也不向调用方区分失败原因）；存储故障记日志并按未
// 命中处理（重复确认优于静默放行）。
func (s *storedGrants) Lookup(ctx context.Context, q ack.GrantQuery) (ack.Grant, bool) {
	tenant, user, err := DecodeUserKey(q.UserID)
	if err != nil {
		return ack.Grant{}, false
	}
	g, err := s.grants.Resolve(ctx, biz.Identity{TenantID: tenant, UserID: user}, user, q.SessionID, q.ToolName)
	if err != nil {
		if !errors.Is(err, biz.ErrGrantNotFound) {
			slog.ErrorContext(ctx, "grant lookup failed", "error", err,
				"tool", q.ToolName, "session", q.SessionID)
		}
		return ack.Grant{}, false
	}
	if g == nil {
		return ack.Grant{}, false
	}
	return ack.Grant{
		UserID:    q.UserID,
		SessionID: q.SessionID,
		ToolName:  g.ToolName,
		CreatedAt: g.CreatedAt.UnixMilli(),
	}, true
}

// Save 实现 ack.GrantSource：ack 在人工同意且指定 remember 时写入。
// 会话级/全局级两形态按 SessionID 是否为空区分（空即全局级）；
// created_by 记属主本人——归属守卫保证确认决定者即会话属主，许可归属
// 与授权来源一致（DecidedBy 的决定者审计留痕归 E2 的确认日志面）。
func (s *storedGrants) Save(ctx context.Context, g ack.Grant) error {
	tenant, user, err := DecodeUserKey(g.UserID)
	if err != nil {
		return fmt.Errorf("agent: save grant for %q: %w", g.UserID, err)
	}
	if err := s.grants.Remember(ctx, biz.Identity{TenantID: tenant, UserID: user}, g.SessionID, g.ToolName); err != nil {
		return fmt.Errorf("agent: save grant for tool %s: %w", g.ToolName, err)
	}
	return nil
}

// grantAdminScopeValue 是授权列表 scope 参数取值。
const (
	grantScopeSelf   = "self"
	grantScopeTenant = "tenant"
)

// grantAdmin 是授权管理 REST 端点（C2）的装配物：列出与吊销当前认证
// 身份相关的工具授权。装配期随 Stack 构造并挂进外层 ServeMux
//（GET {basePath}/grants 与 DELETE {basePath}/grants/{id}，Go ServeMux
// 按模式特异性命中，先于 AG-UI 前缀子树），认证链路由 AgentService 的
// 前缀入链保证。权限码语义收在 biz.GrantUsecase：本人列表/本人吊销
// 不设码，全租户列表验 agent:grant:query、吊销他人验 agent:grant:revoke，
// 不通过 403；目标不可见（跨租户）统一 404 不泄露存在性。
type grantAdmin struct {
	grants *biz.GrantUsecase
}

// newGrantAdmin 构造授权管理端点装配物。
func newGrantAdmin(grants *biz.GrantUsecase) *grantAdmin {
	return &grantAdmin{grants: grants}
}

// grantItem 是授权列表项的响应 DTO：sessionId 为 null 即全局级；
// scope 冗余一份人类可读作用域，前端免判空分支。
type grantItem struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	ToolName  string `json:"toolName"`
	SessionID *string `json:"sessionId"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"createdAt"`
}

// grantListReply 是授权列表响应体。grants 恒非 nil，空授权返回空数组。
type grantListReply struct {
	Grants []grantItem `json:"grants"`
}

// ServeHTTP 分发列表（GET）与吊销（DELETE）。身份缺失（绕过认证中间件
// 的调用）fail-closed 401，与会话列表端点的绕过口径一致。
func (h *grantAdmin) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	auth, ok := biz.IdentityFromContext(r.Context())
	if !ok {
		writeGrantJSON(w, nethttp.StatusUnauthorized, map[string]string{"message": "missing identity in context"})
		return
	}
	switch r.Method {
	case nethttp.MethodGet:
		h.list(w, r, auth)
	case nethttp.MethodDelete:
		h.revoke(w, r, auth)
	default:
		writeGrantJSON(w, nethttp.StatusMethodNotAllowed, map[string]string{"message": "method not allowed"})
	}
}

// list 处理 GET {basePath}/grants?scope=self|tenant：scope 缺省 self，
// 非法取值 400。
func (h *grantAdmin) list(w nethttp.ResponseWriter, r *nethttp.Request, auth biz.Identity) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = grantScopeSelf
	}
	var (
		grants []*biz.ToolGrant
		err    error
	)
	switch scope {
	case grantScopeSelf:
		grants, err = h.grants.ListOwn(r.Context(), auth)
	case grantScopeTenant:
		grants, err = h.grants.ListTenant(r.Context(), auth)
	default:
		writeGrantJSON(w, nethttp.StatusBadRequest, map[string]string{"message": "scope must be self or tenant"})
		return
	}
	if err != nil {
		writeGrantError(r.Context(), w, err)
		return
	}
	reply := grantListReply{Grants: make([]grantItem, 0, len(grants))}
	for _, g := range grants {
		item := grantItem{
			ID:        g.ID,
			UserID:    g.UserID,
			ToolName:  g.ToolName,
			SessionID: g.SessionID,
			Scope:     "global",
			CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
		}
		if g.SessionID != nil {
			item.Scope = "session"
		}
		reply.Grants = append(reply.Grants, item)
	}
	writeGrantJSON(w, nethttp.StatusOK, reply)
}

// revoke 处理 DELETE {basePath}/grants/{id}。
func (h *grantAdmin) revoke(w nethttp.ResponseWriter, r *nethttp.Request, auth biz.Identity) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeGrantJSON(w, nethttp.StatusBadRequest, map[string]string{"message": "grant id must be an integer"})
		return
	}
	if err := h.grants.Revoke(r.Context(), auth, id); err != nil {
		writeGrantError(r.Context(), w, err)
		return
	}
	writeGrantJSON(w, nethttp.StatusOK, map[string]any{"id": id, "revoked": true})
}

// writeGrantError 把 biz 错误映射为 HTTP 状态：kratos 错误自带 HTTP 码
// （400/403/404），其余按内部故障 500 固定文案——错误细节（驱动/SQL
// 文本）只进服务端日志，不透传给已认证客户端。
func writeGrantError(ctx context.Context, w nethttp.ResponseWriter, err error) {
	if e := kratoserrors.FromError(err); e.Code != kratoserrors.UnknownCode {
		writeGrantJSON(w, int(e.Code), map[string]string{"message": e.Message})
		return
	}
	slog.ErrorContext(ctx, "grant admin endpoint failed", "error", err)
	writeGrantJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
}

// writeGrantJSON 以 UTF-8 JSON 写出响应（形态对齐会话列表端点）。
func writeGrantJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
