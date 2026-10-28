package agent

import (
	"bytes"
	"encoding/json"
	"io"
	nethttp "net/http"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ackGuardMaxBodyBytes 限制守卫预读的决定请求体大小：认证后的内存耗尽
// 面收口（决定请求体合法形态远小于此；ackhttp 对超限体的解码同样失败）。
const ackGuardMaxBodyBytes = 1 << 20 // 1MiB

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
// threadId 取值源与 ackhttp 严格对齐（防绕过的关键）：POST /confirmation
// 的 threadId 只认请求体（ackhttp decide 只解码 body），绝不采信查询
// 参数——若查询参数也被采信，攻击者可用「查询参数=自己名下会话 +
// body=他人会话」骗过归属校验；GET /confirmations 只认查询参数。其余
// ack 子路径与形状非法的请求原样透传给内层 mux（404/400 语义归 ackhttp，
// 本守卫只在 threadId 明确成立时加锁）。请求体预读上限
// ackGuardMaxBodyBytes，读失败按 400 拒绝（不放行无归属事实的请求）。
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
		// 与 ackhttp decide 完全同一的解码方式：json.NewDecoder(...).Decode
		// 只取首个 JSON 值、容忍尾随数据。取值结果必然一致——若退化为
		// json.Unmarshal（拒绝尾随数据），「合法 JSON + 尾随数据」的请求
		// 会被守卫当作形状非法放行，ackhttp 却能正常解码照常决定，
		// 形成跨用户绕过（已知绕过形态）。解码失败放行：ackhttp 对同一
		// 输入同样失败，以 400 拒绝。
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
