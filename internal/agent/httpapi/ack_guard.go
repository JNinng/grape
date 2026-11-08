package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	nethttp "net/http"

	"grape/internal/agent/identity"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ackGuardMaxBodyBytes 限制守卫预读的决定请求体大小：认证后的内存耗尽
// 面收口（决定请求体合法形态远小于此；ackhttp 对超限体的解码同样失败）。
const ackGuardMaxBodyBytes = 1 << 20 // 1MiB

// AckOwnerGuard 在 ack 子树的唯一挂载点强制会话归属：threadId 加认证
// userKey 定位会话，不在调用者名下即 404（不区分不存在与非你所有）。
// threadId 取值源必须与 ackhttp 严格对齐——POST 只认请求体、GET 只认
// 查询参数；其余形状非法的请求原样透传给内层 ackhttp。安全约束见
// README「ack 归属守卫」。
type AckOwnerGuard struct {
	inner    nethttp.Handler
	sessions session.Service
	appName  string
}

// NewAckOwnerGuard 构造归属守卫。
func NewAckOwnerGuard(inner nethttp.Handler, sessions session.Service, appName string) *AckOwnerGuard {
	return &AckOwnerGuard{inner: inner, sessions: sessions, appName: appName}
}

func (g *AckOwnerGuard) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	threadID := ""
	switch r.Method {
	case nethttp.MethodPost:
		body, err := io.ReadAll(nethttp.MaxBytesReader(w, r.Body, ackGuardMaxBodyBytes))
		if err != nil {
			writeAckGuardError(w, nethttp.StatusBadRequest, "unreadable request body")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		// 与 ackhttp decide 同一解码方式（Decode 只取首个 JSON 值、容忍
		// 尾随数据）：退化为 Unmarshal 会让带尾随数据的请求被守卫放行、
		// ackhttp 照常决定，形成跨用户绕过。解码失败交 ackhttp 以 400 收场。
		var req struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err == nil {
			threadID = req.ThreadID
		}
	case nethttp.MethodGet:
		threadID = r.URL.Query().Get("threadId")
	}
	if threadID != "" {
		key, err := identity.UserKeyFromContext(r.Context())
		if err != nil {
			// 认证链正常时不可达（外层中间件已 401）；到此处即挂载
			// 被绕过，按未认证 fail-closed。
			writeAckGuardError(w, nethttp.StatusUnauthorized, identity.ErrMissingIdentity.Error())
			return
		}
		sess, err := g.sessions.GetSession(r.Context(), session.Key{
			AppName: g.appName, UserID: key, SessionID: threadID,
		})
		if err != nil {
			writeAckGuardError(w, nethttp.StatusInternalServerError, "session lookup failed")
			return
		}
		if sess == nil {
			writeAckGuardError(w, nethttp.StatusNotFound, "session not found")
			return
		}
	}
	g.inner.ServeHTTP(w, r)
}

// writeAckGuardError 以 ackhttp 相同的 JSON 错误形状应答。
func writeAckGuardError(w nethttp.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
