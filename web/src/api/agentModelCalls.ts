import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// AgentModelCallService 各端点（api/agent/v1/model_call.proto）的前端客户端：
// 模型调用留痕只读查询（系统设置「模型调用」管理页）。序列化口径与后端
// jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为字符串、时间为
// RFC3339、optional 未设置时省略。input/output 摘要与 error 是落库时已
// 脱敏的形态，直接渲染；inputTokens / outputTokens / documentId /
// durationMs 缺省是「未上报 / 无文档事实 / 未记录」，不以 0 冒充。

export interface ModelCallDTO {
  id: string
  userId: string
  traceId?: string
  scenario: string
  modelKey?: string
  endpoint?: string
  documentId?: string
  inputSummary?: string
  outputSummary?: string
  inputTokens?: number
  outputTokens?: number
  durationMs?: number
  status: string
  error?: string
  createdBy: string
  createdAt: string
}

// ModelCallSetDTO 对齐 agent.v1.ModelCallLogSet。
export interface ModelCallSetDTO {
  items: ModelCallDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// listModelCalls 调用 POST /v1/agent/model-calls/list（body: "query"）。
// 零命中时响应没有 items 键，补齐为空数组。
export async function listModelCalls(query?: DynamicQuery): Promise<ModelCallSetDTO> {
  const res = await authFetch('/v1/agent/model-calls/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as ModelCallSetDTO
  return { ...set, items: set.items ?? [] }
}
