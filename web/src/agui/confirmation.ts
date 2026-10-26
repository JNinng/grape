import { authFetch } from '../api/http'
import type { ToolConfirmationPayload } from './events'

// 确认卡与 ack HTTP 契约的对接层：请求体字段与 ackhttp decideRequest
// （threadId/confirmationId/approved/remember）一字对齐；remember 仅在
// 显式选择作用域时携带，缺省不生成许可。查询返回 ack PendingItem 数组
// （Payload + live 标记）。
const confirmPath = '/agui/ack/confirmation'
const confirmationsPath = '/agui/ack/confirmations'

// RememberOption 是确认卡的记住范围：none 不携带 remember 字段；
// session/global 对应 ack.ScopeSession / ack.ScopeGlobal。
export type RememberOption = 'none' | 'session' | 'global'

// DecisionBody 是 POST /agui/ack/confirmation 的请求体形状。
export interface DecisionBody {
  threadId: string
  confirmationId: string
  approved: boolean
  remember?: 'session' | 'global'
}

// buildDecisionBody 构造决定请求体：approved 为线上布尔（true=同意，
// false=拒绝）；remember 只在 session/global 时携带（none 省略字段，
// 与 ackhttp 的空串缺省语义等价且更明确）。
export function buildDecisionBody(
  threadId: string,
  confirmationId: string,
  approved: boolean,
  remember: RememberOption,
): DecisionBody {
  const body: DecisionBody = { threadId, confirmationId, approved }
  if (remember === 'session' || remember === 'global') {
    body.remember = remember
  }
  return body
}

// decideConfirmation 提交一次决定。404 表示确认已不存在（并发已决定或
// 运行被取消），由调用方按「已失效」收敛；其余非 2xx 抛错展示。
export async function decideConfirmation(input: {
  threadId: string
  confirmationId: string
  approved: boolean
  remember: RememberOption
}): Promise<void> {
  const res = await authFetch(confirmPath, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(
      buildDecisionBody(input.threadId, input.confirmationId, input.approved, input.remember),
    ),
  })
  if (res.status === 404) {
    throw new AlreadyDecidedError()
  }
  if (!res.ok) {
    throw new Error(`confirmation failed: HTTP ${res.status}`)
  }
}

// AlreadyDecidedError 标记「确认项已失效」：卡片应移除而非报错。
export class AlreadyDecidedError extends Error {
  constructor() {
    super('confirmation already decided')
    this.name = 'AlreadyDecidedError'
  }
}

// fetchPendingConfirmations 拉取会话的待确认列表（页面加载与运行间隙
// 的兜底查询）。仅保留 pending 项：持久化指针里不应残留终态，防御性
// 过滤避免与流事件的状态机冲突。
export async function fetchPendingConfirmations(threadId: string): Promise<ToolConfirmationPayload[]> {
  const res = await authFetch(`${confirmationsPath}?threadId=${encodeURIComponent(threadId)}`)
  if (!res.ok) {
    throw new Error(`confirmations failed: HTTP ${res.status}`)
  }
  const items = (await res.json()) as ToolConfirmationPayload[]
  if (!Array.isArray(items)) {
    return []
  }
  return items.filter((item) => item.status === 'pending')
}
