package agent

import (
	"bytes"
	"encoding/json"
	"io"
	nethttp "net/http"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ackOwnerGuard 是 ack 路由的会话归属守卫。
//
// 背景与必要性：ack 的等待者以（会话 ID, 确认 ID）隔离命名空间，live
// 路径的 Decide 不校验决定者与会话归属（其恢复路径经 userKey 会话定位
// 天然隔离，但 live 路径无此检查）；ackhttp 自身不做鉴权。grape 的验收
// 要求跨用户不能决定/查询他人确认，故在唯一挂载点对 ack 子树强制归属：
// 以请求解析出的 threadId + 当前认证身份的 userKey 定位会话，会话不
// 存在于调用者名下即 404（不区分「不存在」与「非你所有」，与会话存储
// 的跨用户读取语义一致）。
//
// 覆盖面：POST {prefix}/confirmation（决定）与 GET {prefix}/confirmations
// （待确认列表）。其余 ack 子路径原样透传给内层 mux（404 语义不变）。
// threadId 解析失败时不代答——请求形状校验（400）归 ackhttp，本守卫只
// 在 threadId 明确存在时加锁。
type ackOwnerGuard struct {
	inner    nethttp.Handler
	sessions session.Service
	appName  string
}

// newAckOwnerGuard 构造归属守卫。
func newAckOwnerGuard(inner nethttp.Handler, sessions session.Service, appName string) *ackOwnerGuard {
	return &ackOwnerGuard{inner: inner, sessions: sessions, appName: appName}
}

func (g *ackOwnerGuard) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	threadID := r.URL.Query().Get("threadId")
	if threadID == "" && r.Method == nethttp.MethodPost {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeAckGuardError(w, nethttp.StatusBadRequest, "unreadable request body")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			ThreadID string `json:"threadId"`
		}
		// 解析失败放行：缺形状的请求由内层 ackhttp 以 400 拒绝。
		_ = json.Unmarshal(body, &req)
		threadID = req.ThreadID
	}
	if threadID != "" {
		key, err := userKeyFromContext(r.Context())
		if err != nil {
			// 认证链正常时不可达（外层中间件已 401）；到此处即挂载
			// 被绕过，按未认证 fail-closed。
			writeAckGuardError(w, nethttp.StatusUnauthorized, errMissingIdentityMsg)
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
