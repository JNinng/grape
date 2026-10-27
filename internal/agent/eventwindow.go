package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// eventWindowSpanMax 是事件窗口端点（A3）before/after 各自的上限：窗口
// 形态本身限定了返回量（before+after+1 条事件），上限防单请求拖回大会话
// 全量事件。0 值窗口（只取锚点事件）是占位符原文对照的最小用法。
const eventWindowSpanMax = 50

// eventWindow 是会话事件窗口端点的装配物（TODO A3 的受权后端端点）：
// 消费骨架既有的框架 session.Service（与 AG-UI / ack 同一实例、同一 app
// 名、同一 userKey 编码）经 WindowService 取锚点事件窗口。窗口以认证
// 身份编码的 userKey 定位会话——跨用户会话在存储层不可见，锚点查找
// 失败返回框架 ErrEventWindowAnchorNotFound，端点映射 404（不区分不
// 存在与非你所有，与 ackOwnerGuard 的口径一致）。装配期随 Stack 构造
// （NewStackWithOptions），HTTP 形态经 Stack.EventWindowHandler 交
// service 注册进 Kratos 链。
//
// 压缩对照口径（TODO A3）：压缩只作用于请求投影副本，session 事件流
// 保留全部原文（B2 保证）；压缩占位符携带 event_id / tool_call_id /
// tool_name / reason 恢复引用。运维复现视图对响应文本中出现占位符形状
// 的片段以本端点按 event_id 取回原文对照。本端点返回的是会话事件原文
// （非脱敏产物）——它与对话回放同源同权（/history 快照同样承载原文），
// 只对当前认证用户开放其名下会话，租户边界由 userKey 定位强制。
type eventWindow struct {
	windows session.WindowService
	appName string
}

// newEventWindow 构造事件窗口装配物：会话存储实现 WindowService 时持有
// 之（session/postgres 与 inmemory 均实现）；未实现时留 nil，请求期按
// 装配缺陷以 500 固定文案拒绝（不 panic、不静默降级）。
func newEventWindow(sessions session.Service, appName string) *eventWindow {
	w, _ := sessions.(session.WindowService)
	return &eventWindow{windows: w, appName: appName}
}

// eventToolCallItem 是助手消息内单个工具调用的投影。
type eventToolCallItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// eventWindowEventItem 是单条会话事件的响应投影：content 是原文（压缩
// 只作用于请求投影副本，事件流即原文）；助手消息的 toolCalls 展开供
// 复现视图与审计行的工具调用 ID 互链。
type eventWindowEventItem struct {
	EventID    string              `json:"eventId"`
	CreatedAt  string              `json:"createdAt"`
	Author     string              `json:"author,omitempty"`
	Role       string              `json:"role,omitempty"`
	Content    string              `json:"content,omitempty"`
	ToolCallID string              `json:"toolCallId,omitempty"`
	ToolName   string              `json:"toolName,omitempty"`
	ToolCalls  []eventToolCallItem `json:"toolCalls,omitempty"`
}

// eventWindowReply 是事件窗口响应体；events 恒非 nil（锚点必在窗口内，
// 至少含锚点一条）。
type eventWindowReply struct {
	SessionID     string                 `json:"sessionId"`
	AnchorEventID string                 `json:"anchorEventId"`
	Events        []eventWindowEventItem `json:"events"`
}

// ServeHTTP 处理 GET {basePath}/event-window：查询参数校验 → 认证身份
// → userKey → 框架 GetEventWindow（锚点 + before/after 窗口）。参数缺失
// 或非法 400；身份缺失（绕过认证中间件的调用）fail-closed 401；锚点不
// 可见（不存在或非你所有）404；其余内部错误 500 固定文案。
func (s *eventWindow) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	req, err := eventWindowRequestOf(r)
	if err != nil {
		writeEventWindowError(w, nethttp.StatusBadRequest, err)
		return
	}
	userKey, err := userKeyFromContext(r.Context())
	if err != nil {
		writeEventWindowError(w, nethttp.StatusUnauthorized, err)
		return
	}
	if s.windows == nil {
		// 装配缺陷而非请求错误：会话存储未实现 WindowService。细节只进
		// 服务端日志，客户端拿固定文案。
		slog.ErrorContext(r.Context(), "session store does not implement window service")
		writeEventWindowJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		return
	}
	window, err := s.windows.GetEventWindow(r.Context(), session.EventWindowRequest{
		Key: session.Key{
			AppName:   s.appName,
			UserID:    userKey,
			SessionID: req.threadID,
		},
		AnchorEventID: req.eventID,
		Before:        req.before,
		After:         req.after,
	})
	if err != nil {
		if errors.Is(err, session.ErrEventWindowAnchorNotFound) {
			writeEventWindowError(w, nethttp.StatusNotFound, err)
			return
		}
		slog.ErrorContext(r.Context(), "get event window failed", "error", err)
		writeEventWindowJSON(w, nethttp.StatusInternalServerError, map[string]string{"message": "internal error"})
		return
	}
	writeEventWindowJSON(w, nethttp.StatusOK, eventWindowReplyOf(req.threadID, window))
}

// eventWindowQuery 是已校验的窗口查询参数。
type eventWindowQuery struct {
	threadID string
	eventID  string
	before   int
	after    int
}

// eventWindowRequestOf 解析并校验查询参数：threadId / eventId 必填；
// before / after 缺省 0，负值、非数字或超上限一律拒绝（fail-closed，
// 不静默截断）。
func eventWindowRequestOf(r *nethttp.Request) (*eventWindowQuery, error) {
	q := r.URL.Query()
	threadID := strings.TrimSpace(q.Get("threadId"))
	if threadID == "" {
		return nil, errors.New("threadId is required")
	}
	eventID := strings.TrimSpace(q.Get("eventId"))
	if eventID == "" {
		return nil, errors.New("eventId is required")
	}
	before, err := eventWindowSpanOf(q.Get("before"), "before")
	if err != nil {
		return nil, err
	}
	after, err := eventWindowSpanOf(q.Get("after"), "after")
	if err != nil {
		return nil, err
	}
	return &eventWindowQuery{threadID: threadID, eventID: eventID, before: before, after: after}, nil
}

// eventWindowSpanOf 解析 before/after 单值：空取 0，非法或超上限拒绝。
func eventWindowSpanOf(raw, name string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	if parsed > eventWindowSpanMax {
		return 0, fmt.Errorf("%s exceeds maximum", name)
	}
	return parsed, nil
}

// eventWindowReplyOf 把框架窗口投影为响应 DTO：每条事件一个条目，多
// choice 的正文换行拼接、工具调用全量展开（当前链路单 choice，投影对
// 多 choice 保持证据完整而非取首）。时间以 UTC RFC3339 渲染。
func eventWindowReplyOf(threadID string, window *session.EventWindow) *eventWindowReply {
	reply := &eventWindowReply{
		SessionID:     threadID,
		AnchorEventID: window.AnchorEventID,
		Events:        make([]eventWindowEventItem, 0, len(window.Entries)),
	}
	for _, entry := range window.Entries {
		reply.Events = append(reply.Events, eventWindowEventOf(entry))
	}
	return reply
}

// eventWindowEventOf 投影单条事件：Response 缺失（非消息事件）时保留
// 事件 ID 与时间戳，正文与角色留空。
func eventWindowEventOf(entry session.EventWindowEntry) eventWindowEventItem {
	item := eventWindowEventItem{
		EventID:   entry.Event.ID,
		CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339),
		Author:    entry.Event.Author,
	}
	if entry.Event.Response == nil {
		return item
	}
	var content strings.Builder
	for i, choice := range entry.Event.Response.Choices {
		if i > 0 {
			content.WriteString("\n")
		}
		msg := choice.Message
		content.WriteString(msg.Content)
		if i == 0 {
			item.Role = msg.Role.String()
			item.ToolCallID = msg.ToolID
			item.ToolName = msg.ToolName
		}
		for _, call := range msg.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, eventToolCallItem{
				ID:        call.ID,
				Name:      call.Function.Name,
				Arguments: string(call.Function.Arguments),
			})
		}
	}
	item.Content = content.String()
	return item
}

// writeEventWindowJSON 以 UTF-8 JSON 写出响应（形态对齐 ackhttp）。
func writeEventWindowJSON(w nethttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeEventWindowError 以 JSON 写出错误响应：message 字段与前端
// toApiError 的取值顺序对齐。
func writeEventWindowError(w nethttp.ResponseWriter, status int, err error) {
	writeEventWindowJSON(w, status, map[string]string{"message": err.Error()})
}
