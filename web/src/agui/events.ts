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

// MessageItem 是按 arrive 顺序渲染的文本消息。startedAt 是起始时间
// （毫秒时间戳），只有 /history 快照能提供：快照 rawEvent 按 messageId
// 索引事件起始时间，转换时回填；实时流事件不带该事实。
export interface MessageItem {
  kind: 'message'
  id: string
  role: 'user' | 'assistant' | 'system' | 'developer'
  content: string
  startedAt?: number
  // streaming 标记流式文本仍在增量中（START 之后、END 之前）：渲染端据此
  // 给出输出中的载入提示。快照折叠的历史消息与用户消息一律不带该标记。
  streaming?: boolean
}

// ReasoningItem 是模型思考内容（协议 role=reasoning 的消息）：实时流由
// REASONING_* 事件折叠，历史快照由 role=reasoning 消息直映。思考内容与
// 正文同样是不可信文本，只以 React 文本节点渲染；streaming 为 true 表示
// 该段思考仍在增量中。
export interface ReasoningItem {
  kind: 'reasoning'
  id: string
  content: string
  streaming: boolean
  startedAt?: number
}

// ToolCallItem 是一次工具调用的完整视图：参数增量累积，结果在
// TOOL_CALL_RESULT 到达后填充；结果与参数一律视为不可信文本。startedAt
// 是工具调用起始时间，来源同 MessageItem.startedAt——快照 rawEvent 按
// toolCallId 索引。parentMessageId 是发起该调用的助手消息 ID（快照折叠
// 时由 assistant.toolCalls 的所属消息给出），用于把模型调用事实（模型
// 型号与 token 用量）挂到工具卡片上：工具决策轮通常没有正文，助手消息
// 不单独成项。
export interface ToolCallItem {
  kind: 'tool'
  toolCallId: string
  name: string
  argsText: string
  result: string | null
  done: boolean
  startedAt?: number
  parentMessageId?: string
}

export type ChatItem = MessageItem | ReasoningItem | ToolCallItem

// ChatUnit 是渲染分组：助手回合把思考项与其后的助手消息合为一体，共用
// 一个「助手」角色标签（思考框因此落在标签之下，而不是流内的独立卡片）；
// 其余项各自独立成组。分组只影响呈现，不改变流内时序。
export type ChatUnit =
  | { kind: 'assistant'; reasoning: ReasoningItem[]; message?: MessageItem }
  | { kind: 'item'; item: MessageItem | ToolCallItem }

// groupChatUnits 把保序渲染项折叠为渲染分组：连续思考项与紧随其后的
// 助手消息同属一轮；思考后没有助手消息（如直接进入工具调用）时仍成
// 组，避免思考框脱离角色标签。
export function groupChatUnits(items: ChatItem[]): ChatUnit[] {
  const units: ChatUnit[] = []
  let reasoning: ReasoningItem[] = []
  const flushReasoning = () => {
    if (reasoning.length > 0) {
      units.push({ kind: 'assistant', reasoning })
      reasoning = []
    }
  }
  for (const item of items) {
    if (item.kind === 'reasoning') {
      reasoning.push(item)
      continue
    }
    if (item.kind === 'message' && item.role === 'assistant') {
      units.push({ kind: 'assistant', reasoning, message: item })
      reasoning = []
      continue
    }
    flushReasoning()
    units.push({ kind: 'item', item })
  }
  flushReasoning()
  return units
}

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
      // 终态兜底收口仍在增量中的流：正常路径 START/CONTENT/END 成对出现，
      // 断流（取消、异常）时 END 可能缺失，不能让载入提示永久停留。
      return { ...state, running: false, items: settleStreamingItems(state.items) }
    case 'RUN_ERROR':
      return { ...state, running: false, error: event.message, items: settleStreamingItems(state.items) }
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
      const item: MessageItem = { kind: 'message', id: event.messageId, role, content: '', streaming: true }
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
    case 'TEXT_MESSAGE_END': {
      const items = state.items.map((item) =>
        item.kind === 'message' && item.id === event.messageId ? { ...item, streaming: false } : item,
      )
      return { ...state, items }
    }
    // REASONING_START/REASONING_END 是思考区段（span）的开关，内容仍按
    // REASONING_MESSAGE_* 增量折叠；只有后者的 messageId 定位到渲染项。
    case 'REASONING_START':
    case 'REASONING_END':
    case 'REASONING_ENCRYPTED_VALUE':
      return state
    case 'REASONING_MESSAGE_START': {
      if (state.items.some((item) => item.kind === 'reasoning' && item.id === event.messageId)) {
        return state
      }
      const item: ReasoningItem = { kind: 'reasoning', id: event.messageId, content: '', streaming: true }
      return { ...state, items: [...state.items, item] }
    }
    case 'REASONING_MESSAGE_CONTENT':
      return { ...state, items: appendReasoningDelta(state.items, event.messageId, event.delta) }
    // CHUNK 是 START/CONTENT/END 的缩写：缺 messageId 时延续上一段仍在
    // 增量中的思考。
    case 'REASONING_MESSAGE_CHUNK':
      return { ...state, items: appendReasoningDelta(state.items, event.messageId, event.delta ?? '') }
    case 'REASONING_MESSAGE_END': {
      const items = state.items.map((item) =>
        item.kind === 'reasoning' && item.id === event.messageId ? { ...item, streaming: false } : item,
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

// lastOpenReasoningId 取最后一段仍在增量中的思考 ID：REASONING_MESSAGE_CHUNK
// 的 messageId 可省略，协议语义是延续当前段落。
function lastOpenReasoningId(items: ChatItem[]): string | undefined {
  for (let i = items.length - 1; i >= 0; i -= 1) {
    const item = items[i]
    if (item.kind === 'reasoning' && item.streaming) {
      return item.id
    }
  }
  return undefined
}

// appendReasoningDelta 把一段思考增量并入对应段落：缺 messageId 时延续
// 最后一段未收口的思考；有 messageId 而尚无对应项时按新段承接（START
// 缺失的协议断流兜底，不丢内容）；两者都无归属则原样返回。
function appendReasoningDelta(items: ChatItem[], messageId: string | undefined, delta: string): ChatItem[] {
  const id = messageId === undefined || messageId === '' ? lastOpenReasoningId(items) : messageId
  if (id === undefined) {
    return items
  }
  if (!items.some((item) => item.kind === 'reasoning' && item.id === id)) {
    const item: ReasoningItem = { kind: 'reasoning', id, content: delta, streaming: true }
    return [...items, item]
  }
  return items.map((item) =>
    item.kind === 'reasoning' && item.id === id ? { ...item, content: item.content + delta } : item,
  )
}

// settleStreamingItems 收口所有仍在增量中的项（文本与思考）：运行终态兜底，
// 保证载入提示不会因 END 缺失而残留；无流式项时原引用返回。
function settleStreamingItems(items: ChatItem[]): ChatItem[] {
  let changed = false
  const settled = items.map((item) => {
    if (item.kind === 'reasoning' && item.streaming) {
      changed = true
      return { ...item, streaming: false }
    }
    if (item.kind === 'message' && item.streaming) {
      changed = true
      return { ...item, streaming: false }
    }
    return item
  })
  return changed ? settled : items
}

// SnapshotSourceMetadata 是 /history 快照 rawEvent 内按消息 ID 与工具调用
// ID 索引的来源时间事实（框架 WithEventSourceMetadataEnabled 的输出）；
// 只取复现视图需要的起始时间，其余字段忽略。
export interface SnapshotSourceMetadata {
  messages?: Record<string, { timestamp?: number } | undefined>
  toolCalls?: Record<string, { timestamp?: number } | undefined>
}

// startedAtOf 从来源索引取起始时间：非有限数字或非正数视为缺失（不渲染
// 伪时间），缺失返回 undefined 交给展示端降级。
function startedAtOf(index: Record<string, { timestamp?: number } | undefined> | undefined, key: string): number | undefined {
  const ts = index?.[key]?.timestamp
  return typeof ts === 'number' && Number.isFinite(ts) && ts > 0 ? ts : undefined
}

// messagesToItems 把 /agui/history 快照回放的消息列表折叠为渲染项：
// user/assistant 文本直映；reasoning 消息折叠为思考项（空内容不造空块）；
// assistant.toolCalls 展开为工具项；tool 消息按 toolCallId 合并进既有工具
// 项的结果，孤儿结果单独成项（与流式 reducer 的 TOOL_CALL_RESULT 语义
// 一致）。source 非空时按 messageId / toolCallId 回填起始时间，缺失即不
// 设该字段。
export function messagesToItems(messages: Message[], source?: SnapshotSourceMetadata): ChatItem[] {
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
        startedAt: startedAtOf(source?.messages, message.id),
      })
      continue
    }
    if (message.role === 'reasoning') {
      const content = contentToText(message.content)
      if (content !== '') {
        items.push({
          kind: 'reasoning',
          id: message.id,
          content,
          streaming: false,
          startedAt: startedAtOf(source?.messages, message.id),
        })
      }
      continue
    }
    if (message.role === 'assistant') {
      if (message.content) {
        items.push({
          kind: 'message',
          id: message.id,
          role: 'assistant',
          content: message.content,
          startedAt: startedAtOf(source?.messages, message.id),
        })
      }
      for (const call of message.toolCalls ?? []) {
        appendTool({
          kind: 'tool',
          toolCallId: call.id,
          name: call.function.name,
          argsText: call.function.arguments,
          result: null,
          done: true,
          startedAt: startedAtOf(source?.toolCalls, call.id),
          parentMessageId: message.id,
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
          startedAt: startedAtOf(source?.toolCalls, message.toolCallId),
        })
      }
    }
  }
  return items
}
