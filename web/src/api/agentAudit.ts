import { authFetch } from './http'
import { toApiError } from './toApiError'

// Agent 业务审计查询端点（供运维复现视图）的 DTO：字段与后端
// service/agent_audit.go 的 JSON 形状对齐，时间为 UTC RFC3339。参数与
// 结果摘要是落库时已脱敏的形态，前端直接渲染，不回传原文。

export interface ToolCallAudit {
  id: number
  toolCallId: string
  toolName: string
  sessionId?: string
  traceId?: string
  argumentsSummary?: string
  resultSummary?: string
  retryCount: number
  durationMs: number
  status: string
  calledAt: string
}

export interface ConfirmationAudit {
  id: number
  confirmationId: string
  sessionId: string
  toolCallId?: string
  toolName: string
  decision: string
  remember?: string
  path: string
  decidedBy: string
  traceId?: string
  decidedAt: string
}

interface ToolCallsReply {
  items?: ToolCallAudit[]
  total: number
}

interface ConfirmationsReply {
  items?: ConfirmationAudit[]
  total: number
}

// fetchToolCallAudits 拉取工具调用审计分页列表：按会话过滤（可选）。
// 归属由服务端认证身份决定，前端不传任何归属参数。
export async function fetchToolCallAudits(sessionId?: string, offset = 0, limit = 50): Promise<ToolCallAudit[]> {
  const query = new URLSearchParams({ offset: String(offset), limit: String(limit) })
  if (sessionId) {
    query.set('session_id', sessionId)
  }
  const res = await authFetch(`/v1/agent/audit/tool-calls?${query.toString()}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as ToolCallsReply
  return body.items ?? []
}

// fetchConfirmationAudits 拉取授权确认审计分页列表：按会话过滤（可选）。
export async function fetchConfirmationAudits(sessionId?: string, offset = 0, limit = 50): Promise<ConfirmationAudit[]> {
  const query = new URLSearchParams({ offset: String(offset), limit: String(limit) })
  if (sessionId) {
    query.set('session_id', sessionId)
  }
  const res = await authFetch(`/v1/agent/audit/confirmations?${query.toString()}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as ConfirmationsReply
  return body.items ?? []
}
