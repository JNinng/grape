import { authFetch } from './http'
import { toApiError } from './toApiError'

// 模型调用留痕查询端点（供运维复现视图）的 DTO：字段与后端
// service/agent_model_call.go 的 JSON 形状对齐，时间为 UTC RFC3339。
// inputSummary / outputSummary / error 是落库时已脱敏的形态，前端直接
// 渲染；inputTokens / outputTokens / documentId 的 null 是「未上报 /
// 无文档事实」，不以 0 冒充。

export interface ModelCall {
  id: number
  scenario: string
  status: string
  modelKey?: string
  endpoint?: string
  documentId: number | null
  traceId?: string
  inputSummary?: string
  outputSummary?: string
  inputTokens: number | null
  outputTokens: number | null
  durationMs: number
  error?: string
  createdAt: string
}

// ModelCallFilters 是面板的过滤条件：空串/缺省不过滤。
export interface ModelCallFilters {
  scenario: string
  modelKey: string
  status: string
}

interface ModelCallsReply {
  items?: ModelCall[]
  total: number
}

// fetchModelCalls 拉取模型调用留痕分页列表：按场景/模型键/状态过滤
//（可选）。归属由服务端认证身份决定，前端不传任何归属参数。
export async function fetchModelCalls(
  filters: ModelCallFilters = { scenario: '', modelKey: '', status: '' },
  offset = 0,
  limit = 50,
): Promise<ModelCall[]> {
  const query = new URLSearchParams({ offset: String(offset), limit: String(limit) })
  if (filters.scenario !== '') {
    query.set('scenario', filters.scenario)
  }
  if (filters.modelKey !== '') {
    query.set('model_key', filters.modelKey)
  }
  if (filters.status !== '') {
    query.set('status', filters.status)
  }
  const res = await authFetch(`/v1/agent/model-calls?${query.toString()}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as ModelCallsReply
  return body.items ?? []
}
