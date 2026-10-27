import { authFetch } from './http'
import { toApiError } from './toApiError'

// 会话事件窗口端点（A3，供运维复现视图做压缩占位符原文对照）的 DTO：
// 字段与后端 service/agent_event_window.go 的 JSON 形状对齐，时间为 UTC
// RFC3339。content 是会话事件流中的原文（压缩只作用于请求投影副本）。

export interface EventWindowToolCall {
  id: string
  name: string
  arguments: string
}

export interface EventWindowEvent {
  eventId: string
  createdAt: string
  author?: string
  role?: string
  content?: string
  toolCallId?: string
  toolName?: string
  toolCalls?: EventWindowToolCall[]
}

export interface EventWindow {
  sessionId: string
  anchorEventId: string
  events: EventWindowEvent[]
}

// fetchEventWindow 按 threadId + eventId 取锚点事件窗口；before/after
// 为可选的前后事件数（后端各上限 50）。跨用户锚点统一 404。
export async function fetchEventWindow(threadId: string, eventId: string, before = 0, after = 0): Promise<EventWindow> {
  const query = new URLSearchParams({ threadId, eventId, before: String(before), after: String(after) })
  const res = await authFetch(`/agui/event-window?${query.toString()}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as EventWindow
  return { sessionId: body.sessionId, anchorEventId: body.anchorEventId, events: body.events ?? [] }
}
