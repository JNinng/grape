import type { ContentPart, Event, Message } from '@ag-ui/client'

// AG-UI 事件 reducer（纯函数）：把框架事件流折叠为对话页可渲染的状态。
// 所有事件类型参考 @ag-ui/core 的协议定义；未知事件一律忽略（协议向前
// 兼容），确认载荷形状与 ack 库 payload.go 的 JSON 字段一字对齐。

// ToolConfirmationPayload 是 tool_confirmation 自定义事件的载荷：字段名
// 与 ack.Payload（confirmationId/toolCallId/toolName/arguments/sessionId/
// status/decidedBy/createdAt）及 PendingItem 的 live 标记一一对应。
export interface ToolConfirmationPayload {
  confirmationId: string
  toolCallId: string
  toolName: string
  arguments?: unknown
  sessionId: string
  status: 'pending' | 'approved' | 'denied'
  decidedBy?: string
  createdAt: number
  live?: boolean
}

// MessageItem 是按 arrive 顺序渲染的文本消息。
export interface MessageItem {
  kind: 'message'
  id: string
  role: 'user' | 'assistant' | 'system' | 'developer'
  content: string
}

// ToolCallItem 是一次工具调用的完整视图：参数增量累积，结果在
// TOOL_CALL_RESULT 到达后填充；结果与参数一律视为不可信文本。
export interface ToolCallItem {
  kind: 'tool'
  toolCallId: string
  name: string
  argsText: string
  result: string | null
  done: boolean
}

export type ChatItem = MessageItem | ToolCallItem

// StepInfo 是一次步骤（STEP_STARTED / STEP_FINISHED 对）的展示状态。
export interface StepInfo {
  name: string
  finished: boolean
}

// ChatState 是对话页的完整可渲染状态：items 保序渲染，confirmations
// 只保留待决定项（终态事件把它移除），running/error 驱动 UI 分支。
export interface ChatState {
  items: ChatItem[]
  steps: StepInfo[]
  running: boolean
  error: string | null
  confirmations: ToolConfirmationPayload[]
}

export function initialChatState(): ChatState {
  return { items: [], steps: [], running: false, error: null, confirmations: [] }
}

// contentToText 把协议内容（纯文本或分部件数组）归一为可渲染文本：
// 文本部件取 text，其余部件（媒体等）以占位说明呈现。产物只作为
// React 文本节点渲染，绝不进 HTML 注入路径。
export function contentToText(content: string | ContentPart[] | undefined | null): string {
  if (content === undefined || content === null) {
    return ''
  }
  if (typeof content === 'string') {
    return content
  }
  return content
    .map((part) => (part.type === 'text' ? part.text : `[${part.type}]`))
    .join('')
}

// isToolConfirmationPayload 收窄自定义事件载荷：字段不全即视为无关
// 事件忽略，绝不把任意形状渲染成确认卡。
export function isToolConfirmationPayload(value: unknown): value is ToolConfirmationPayload {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const v = value as Record<string, unknown>
  return (
    typeof v.confirmationId === 'string' &&
    typeof v.toolCallId === 'string' &&
    typeof v.toolName === 'string' &&
    typeof v.sessionId === 'string' &&
    (v.status === 'pending' || v.status === 'approved' || v.status === 'denied')
  )
}

// applyChatEvent 折叠单个事件：不可变更新，调用方以返回值替换状态。
export function applyChatEvent(state: ChatState, event: Event): ChatState {
  switch (event.type) {
    case 'RUN_STARTED':
      return { ...state, running: true, error: null }
    case 'RUN_FINISHED':
      return { ...state, running: false }
    case 'RUN_ERROR':
      return { ...state, running: false, error: event.message }
    case 'STEP_STARTED':
      return { ...state, steps: [...state.steps, { name: event.stepName, finished: false }] }
    case 'STEP_FINISHED': {
      const steps = [...state.steps]
      for (let i = steps.length - 1; i >= 0; i -= 1) {
        if (steps[i].name === event.stepName && !steps[i].finished) {
          steps[i] = { ...steps[i], finished: true }
          break
        }
      }
      return { ...state, steps }
    }
    case 'TEXT_MESSAGE_START': {
      const role = event.role ?? 'assistant'
      if (state.items.some((item) => item.kind === 'message' && item.id === event.messageId)) {
        return state
      }
      const item: MessageItem = { kind: 'message', id: event.messageId, role, content: '' }
      return { ...state, items: [...state.items, item] }
    }
    case 'TEXT_MESSAGE_CONTENT': {
      const items = state.items.map((item) =>
        item.kind === 'message' && item.id === event.messageId
          ? { ...item, content: item.content + event.delta }
          : item,
      )
      return { ...state, items }
    }
    case 'TOOL_CALL_START': {
      if (state.items.some((item) => item.kind === 'tool' && item.toolCallId === event.toolCallId)) {
        return state
      }
      const item: ToolCallItem = {
        kind: 'tool',
        toolCallId: event.toolCallId,
        name: event.toolCallName,
        argsText: '',
        result: null,
        done: false,
      }
      return { ...state, items: [...state.items, item] }
    }
    case 'TOOL_CALL_ARGS': {
      const items = state.items.map((item) =>
        item.kind === 'tool' && item.toolCallId === event.toolCallId
          ? { ...item, argsText: item.argsText + event.delta }
          : item,
      )
      return { ...state, items }
    }
    case 'TOOL_CALL_END': {
      const items = state.items.map((item) =>
        item.kind === 'tool' && item.toolCallId === event.toolCallId ? { ...item, done: true } : item,
      )
      return { ...state, items }
    }
    case 'TOOL_CALL_RESULT': {
      const text = contentToText(event.content)
      const known = state.items.some((item) => item.kind === 'tool' && item.toolCallId === event.toolCallId)
      const items = known
        ? state.items.map((item) =>
            item.kind === 'tool' && item.toolCallId === event.toolCallId
              ? { ...item, result: text, done: true }
              : item,
          )
        : [
            ...state.items,
            {
              kind: 'tool' as const,
              toolCallId: event.toolCallId,
              name: '',
              argsText: '',
              result: text,
              done: true,
            },
          ]
      return { ...state, items }
    }
    case 'CUSTOM': {
      if (event.name !== 'tool_confirmation' || !isToolConfirmationPayload(event.value)) {
        return state
      }
      const payload = event.value
      // 待确认项按 confirmationId 去重；终态（approved/denied）事件把
      // 该项移出待决列表——决定沿原流回推时 UI 即时收敛。
      const confirmations =
        payload.status === 'pending'
          ? upsertConfirmation(state.confirmations, payload)
          : state.confirmations.filter((c) => c.confirmationId !== payload.confirmationId)
      return { ...state, confirmations }
    }
    default:
      return state
  }
}

// upsertConfirmation 以 confirmationId 为键去重合并。
function upsertConfirmation(
  list: ToolConfirmationPayload[],
  payload: ToolConfirmationPayload,
): ToolConfirmationPayload[] {
  const rest = list.filter((c) => c.confirmationId !== payload.confirmationId)
  return [...rest, payload]
}

// messagesToItems 把 /agui/history 快照回放的消息列表折叠为渲染项：
// user/assistant 文本直映；assistant.toolCalls 展开为工具项；tool 消息
// 按 toolCallId 合并进既有工具项的结果，孤儿结果单独成项（与流式
// reducer 的 TOOL_CALL_RESULT 语义一致）。
export function messagesToItems(messages: Message[]): ChatItem[] {
  const items: ChatItem[] = []
  const toolIndex = new Map<string, ToolCallItem>()
  const appendTool = (item: ToolCallItem) => {
    items.push(item)
    toolIndex.set(item.toolCallId, item)
  }
  for (const message of messages) {
    if (message.role === 'user') {
      items.push({
        kind: 'message',
        id: message.id,
        role: 'user',
        content: contentToText(message.content),
      })
      continue
    }
    if (message.role === 'assistant') {
      if (message.content) {
        items.push({ kind: 'message', id: message.id, role: 'assistant', content: message.content })
      }
      for (const call of message.toolCalls ?? []) {
        appendTool({
          kind: 'tool',
          toolCallId: call.id,
          name: call.function.name,
          argsText: call.function.arguments,
          result: null,
          done: true,
        })
      }
      continue
    }
    if (message.role === 'tool') {
      const existing = toolIndex.get(message.toolCallId)
      const result = contentToText(message.content)
      if (existing) {
        existing.result = result
      } else {
        appendTool({
          kind: 'tool',
          toolCallId: message.toolCallId,
          name: '',
          argsText: '',
          result,
          done: true,
        })
      }
    }
  }
  return items
}
