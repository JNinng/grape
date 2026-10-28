import { authFetch } from './http'
import { toApiError } from './toApiError'

// 检索留痕查询端点（供运维复现视图）的 DTO：字段与后端
// service/agent_retrieval.go 的 JSON 形状对齐，时间为 UTC RFC3339。
// queryText / filterSummary / hits 是落库时已脱敏的形态，前端直接渲染。

export interface RetrievalHit {
  documentId: string
  score: number
  anchor?: string
}

export interface RetrievalLog {
  id: number
  source: string
  status: string
  queryText?: string
  sessionId?: string
  traceId?: string
  requestedTopK: number | null
  effectiveTopK: number | null
  filterSummary: unknown
  hits: unknown
  rerankBefore?: string
  rerankAfter?: string
  durationMs: number
  createdAt: string
}

interface RetrievalLogsReply {
  items?: RetrievalLog[]
  total: number
}

// fetchRetrievalLogs 拉取检索留痕分页列表：按会话与来源过滤（可选）。
// 归属由服务端认证身份决定，前端不传任何归属参数。
export async function fetchRetrievalLogs(sessionId?: string, source = '', offset = 0, limit = 50): Promise<RetrievalLog[]> {
  const query = new URLSearchParams({ offset: String(offset), limit: String(limit) })
  if (sessionId) {
    query.set('session_id', sessionId)
  }
  if (source !== '') {
    query.set('source', source)
  }
  const res = await authFetch(`/v1/agent/retrieval-logs?${query.toString()}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as RetrievalLogsReply
  return body.items ?? []
}
