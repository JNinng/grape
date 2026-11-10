import { authFetch } from '../api/http'
import { messagesToItems, type ChatItem, type SnapshotSourceMetadata } from './events'
import type { Message } from '@ag-ui/client'
import { newId } from './ids'

// /agui/history 是 POST SSE（框架 messages snapshot）：以 RunAgentInput
// 形状的请求体拉取会话快照，流内 MESSAGES_SNAPSHOT 事件携带全量消息。
// 上游 track follow 存在同毫秒事件可能被跳过的缺陷（上游已知缺陷），
// 前端不做 follow 跟流，加载会话时拉一次快照即为回放；新消息由运行
// 事件实时折叠。快照运行在快照事件后并不立即关流（框架还会走完
// run 生命周期才结束），历史不依赖也不等待它。
const historyPath = '/agui/history'

// fetchHistorySnapshot 拉取一个会话的历史并折叠为渲染项。快照缺失
// （后端未开启或空会话）返回空数组；网络或协议错误抛出由调用方展示。
// 快照 rawEvent 是后端开启源元数据时的来源索引，随消息一并折叠，回放
// 项因此携带起始时间；索引缺失即按无时间渲染。
export async function fetchHistorySnapshot(threadId: string): Promise<ChatItem[]> {
  const res = await authFetch(historyPath, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      threadId,
      runId: newId('history'),
      messages: [],
      tools: [],
      context: [],
    }),
  })
  if (!res.ok) {
    throw new Error(`history failed: HTTP ${res.status}`)
  }
  if (res.body === null) {
    return []
  }
  const snapshot = await collectSnapshotMessages(res.body)
  return messagesToItems(snapshot.messages, snapshot.source)
}

// snapshotPayload 是快照事件中本端关心的字段：消息列表与来源索引。
interface snapshotPayload {
  messages: Message[]
  source?: SnapshotSourceMetadata
}

// collectSnapshotMessages 解析 SSE 流：只关心 data 行的 JSON 载荷，拿到
// 首个 MESSAGES_SNAPSHOT 即取消读取（reader.cancel）并返回——历史立即
// 渲染，不等快照流关闭；其后的事件对快照回放没有意义。
async function collectSnapshotMessages(body: ReadableStream<Uint8Array>): Promise<snapshotPayload> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) {
      return { messages: [] }
    }
    buffer += decoder.decode(value, { stream: true })
    let newline = buffer.indexOf('\n')
    while (newline !== -1) {
      const line = buffer.slice(0, newline).replace(/\r$/, '')
      buffer = buffer.slice(newline + 1)
      if (line.startsWith('data:')) {
        const event = parseDataLine(line.slice(5).trim())
        if (event !== null && event.type === 'MESSAGES_SNAPSHOT') {
          await cancelQuietly(reader)
          return { messages: event.messages ?? [], source: event.rawEvent }
        }
      }
      newline = buffer.indexOf('\n')
    }
  }
}

// cancelQuietly 断开快照流：取消失败（对端已关流等竞态）只影响清理，
// 不影响已到手的快照。
async function cancelQuietly(reader: ReadableStreamDefaultReader<Uint8Array>): Promise<void> {
  try {
    await reader.cancel()
  } catch {
    // 快照已到手，取消失败无需上报。
  }
}

// parseDataLine 解析单条 data 载荷；非 JSON 行（心跳注释等）忽略。
function parseDataLine(
  raw: string,
): { type: string; messages?: Message[]; rawEvent?: SnapshotSourceMetadata } | null {
  if (raw === '') {
    return null
  }
  try {
    return JSON.parse(raw) as { type: string; messages?: Message[]; rawEvent?: SnapshotSourceMetadata }
  } catch {
    return null
  }
}
