package httpapi

import (
	"errors"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"

	"grape/internal/agent/identity"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SessionUsage 是会话模型用量端点的装配物：按认证身份读本人会话的事件
// 流，投影每次模型调用的事实——模型型号、token 用量，以及流程级的思考
// 与首字时长（框架只在每次运行的首个调用上测量并复制到后续调用，逐调用
// 呈现没有独立事实）。事实来自框架持久化的会话事件（事件流的 response
// 部分），不额外落库、不二次采集；跨用户不可见（会话键含 userKey），会话
// 不存在即 404。
//
// 入参校验、认证归属与错误语义与事件窗口端点一致：参数缺失或非法 400，
// 身份缺失 fail-closed 401，会话不可见 404，其余存储故障 500 固定文案。
// 装配期随 Stack 构造，HTTP 形态经 Stack.SessionUsageHandler 交 service
// 注册进 Kratos 链。
type SessionUsage struct {
	sessions session.Service
	appName  string
}

// NewSessionUsage 构造会话用量装配物：沿用骨架既有的框架 session.Service
// 与 app 名（与 AG-UI / ack / 事件窗口同实例、同命名空间）。
func NewSessionUsage(sessions session.Service, appName string) *SessionUsage {
	return &SessionUsage{sessions: sessions, appName: appName}
}

// sessionUsageTokens 是一次模型调用的 token 用量：cached/reasoning 为可选
// 细分口径，缺失时省略而非记 0（避免把「没报表」读成「用零个」）。
type sessionUsageTokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
	Cached     int `json:"cached,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
}

// sessionUsageFact 是一次模型调用的事实投影：messageId 是响应 ID，也是
// AG-UI 消息 ID——回放流与用量事实按它桥接。usage 缺失表示该调用未上报
// 用量（部分 provider 不返回），此时 tokens 不呈现。
//
// invocationId 标识一次运行内的调用链：reasoningMs / firstTokenMs 是
// **流程级**事实，框架只在首次模型调用时测量，随后复制到同一次运行的
// 后续调用上（见 internal/telemetry/metric_chat.go 的 TimingInfo 处理）。
// 因此这两个字段对同一 invocationId 的各条事实取值相同，展示端只应在该
// 运行的首条事实上呈现，不能当作逐调用的首字与思考时长。
type sessionUsageFact struct {
	MessageID    string              `json:"messageId"`
	InvocationID string              `json:"invocationId,omitempty"`
	Model        string              `json:"model,omitempty"`
	Usage        *sessionUsageTokens `json:"usage,omitempty"`
	ReasoningMs  int64               `json:"reasoningMs,omitempty"`
	FirstTokenMs int64               `json:"firstTokenMs,omitempty"`
	CreatedAt    string              `json:"createdAt"`
}

// sessionUsageReply 是会话用量响应体；facts 恒非 nil（无模型调用时为空
// 数组），按事件时间升序。
type sessionUsageReply struct {
	SessionID string             `json:"sessionId"`
	Facts     []sessionUsageFact `json:"facts"`
}

// ServeHTTP 处理 GET {basePath}/session-usage?threadId=。
func (s *SessionUsage) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	threadID := strings.TrimSpace(r.URL.Query().Get("threadId"))
	if threadID == "" {
		writeEventWindowError(w, nethttp.StatusBadRequest, errors.New("threadId is required"))
		return
	}
	userKey, err := identity.UserKeyFromContext(r.Context())
	if err != nil {
		writeEventWindowError(w, nethttp.StatusUnauthorized, err)
		return
	}
	sess, err := s.sessions.GetSession(r.Context(), session.Key{
		AppName:   s.appName,
		UserID:    userKey,
		SessionID: threadID,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "get session for usage facts failed", "error", err)
		writeEventWindowJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		return
	}
	if sess == nil {
		// 存储对「不在调用者名下或不存在」的会话返回空结果；统一 404，
		// 不区分存在性。
		writeEventWindowError(w, nethttp.StatusNotFound, errors.New("session not found"))
		return
	}
	writeEventWindowJSON(w, nethttp.StatusOK, sessionUsageReplyOf(threadID, sess))
}

// sessionUsageReplyOf 把会话事件投影为用量事实：每条带上响应用量的事件
// 一项，按响应 ID 去重——一次模型调用的终态事件与其衍生的工具结果事件
// 共享同一响应 ID 与同一份用量，重复项只保留首个；若首个未带用量而后续
// 带用量，则以带用量者替换，保证事实不因事件顺序丢失。
func sessionUsageReplyOf(threadID string, sess *session.Session) *sessionUsageReply {
	reply := &sessionUsageReply{
		SessionID: threadID,
		Facts:     make([]sessionUsageFact, 0, len(sess.Events)),
	}
	index := make(map[string]int, len(sess.Events))
	for i := range sess.Events {
		evt := sess.Events[i]
		resp := evt.Response
		if resp == nil || resp.ID == "" {
			continue
		}
		if resp.Model == "" && resp.Usage == nil {
			continue
		}
		fact := sessionUsageFact{
			MessageID:    resp.ID,
			InvocationID: evt.InvocationID,
			Model:        resp.Model,
			CreatedAt:    evt.Timestamp.UTC().Format(time.RFC3339),
		}
		if usage := resp.Usage; usage != nil {
			fact.Usage = &sessionUsageTokens{
				Prompt:     usage.PromptTokens,
				Completion: usage.CompletionTokens,
				Total:      usage.TotalTokens,
				Cached:     usage.PromptTokensDetails.CachedTokens,
				Reasoning:  usage.CompletionTokensDetails.ReasoningTokens,
			}
			if timing := usage.TimingInfo; timing != nil {
				fact.ReasoningMs = timing.ReasoningDuration.Milliseconds()
				fact.FirstTokenMs = timing.FirstTokenDuration.Milliseconds()
			}
		}
		at, exists := index[resp.ID]
		if exists && reply.Facts[at].Usage != nil {
			continue
		}
		if exists {
			reply.Facts[at] = fact
			continue
		}
		index[resp.ID] = len(reply.Facts)
		reply.Facts = append(reply.Facts, fact)
	}
	return reply
}
