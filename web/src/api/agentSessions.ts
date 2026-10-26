import { authFetch } from './http'
import { toApiError } from './toApiError'

// agent 会话列表端点（GET /agui/sessions）的 DTO：字段与后端
// service/agent_sessions.go 的 JSON 形状对齐，时间为 UTC RFC3339。
export interface AgentSessionMeta {
  id: string
  createdAt: string
  updatedAt: string
}

// fetchAgentSessions 拉取当前用户的 agent 会话元数据列表：归属由服务
// 端认证身份决定，前端不传任何归属参数。默认取前 50 条（后端上限
// 100），按 updatedAt 降序排列供会话侧栏展示。
export async function fetchAgentSessions(offset = 0, limit = 50): Promise<AgentSessionMeta[]> {
  const res = await authFetch(`/agui/sessions?offset=${offset}&limit=${limit}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const body = (await res.json()) as { sessions?: AgentSessionMeta[] }
  const sessions = body.sessions ?? []
  return [...sessions].sort(
    (a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime(),
  )
}
