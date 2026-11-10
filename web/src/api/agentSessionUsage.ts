import { authFetch } from './http'

// /agui/session-usage 按 threadId 返回每次模型调用的事实：模型型号、token
// 用量、思考时长与首字时长。事实来自服务端持久化的会话事件（事件流的
// response 部分），只读；messageId 与 AG-UI 消息 ID 同源，复现视图以它
// 把事实挂到具体消息与工具调用上。
const sessionUsagePath = '/agui/session-usage'

// SessionUsageTokens 是一次模型调用的 token 用量：cached/reasoning 是可选
// 细分口径，缺失表示服务端未上报，不是「用零个」。
export interface SessionUsageTokens {
  prompt: number
  completion: number
  total: number
  cached?: number
  reasoning?: number
}

// SessionUsageFact 是一次模型调用的事实投影；usage 缺失表示该调用未上报
// 用量（部分 provider 不返回），此时不呈现 token。
//
// reasoningMs / firstTokenMs 是流程级事实：框架只在一次运行的首个模型调用
// 上测量，随后复制到同一次运行后续调用的用量里，因此同一 invocationId 的
// 各条事实取值相同。展示端只在每组 invocationId 的首条事实上呈现，不能
// 当作逐调用耗时。
export interface SessionUsageFact {
  messageId: string
  invocationId?: string
  model?: string
  usage?: SessionUsageTokens
  reasoningMs?: number
  firstTokenMs?: number
  createdAt: string
}

// fetchSessionUsage 拉取一个会话的模型调用事实。会话不可见或尚未建档
// （归属守卫 404）按空数组处理——与复现页其余证据面的空态一致；其余失败
// 抛出由调用方降级（用量是辅助证据，不阻断回放）。
export async function fetchSessionUsage(threadId: string): Promise<SessionUsageFact[]> {
  const res = await authFetch(`${sessionUsagePath}?threadId=${encodeURIComponent(threadId)}`)
  if (res.status === 404) {
    return []
  }
  if (!res.ok) {
    throw new Error(`session usage failed: HTTP ${res.status}`)
  }
  const body = (await res.json()) as { facts?: SessionUsageFact[] }
  if (!Array.isArray(body.facts)) {
    return []
  }
  return body.facts
}
