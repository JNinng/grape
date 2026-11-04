import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// SessionAdminService 各端点（api/audit/v1/session_admin.proto）的前端
// 客户端：租户内会话列表与主动撤销（幂等）。令牌哈希不进入任何 DTO。
// 序列化口径与后端 jsoncodec（protojson 覆盖）一致。

// SessionAdminDTO 对齐 audit.v1.SessionAdmin：全部只读，变更仅撤销一个动作。
export interface SessionAdminDTO {
  id: string
  status: number
  userId: string
  expiresAt: string
  refreshExpiresAt?: string
  lastActivityAt?: string
  deviceInfo?: string
  ipAddress?: string
  isRevoked: boolean
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
}

// SessionAdminSetDTO 对齐 audit.v1.SessionAdminSet。
export interface SessionAdminSetDTO {
  items: SessionAdminDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// listSessions 调用 POST /v1/sessions/list（body: "query"）。零命中时响应
// 没有 items 键，补齐为空数组。
export async function listSessions(query?: DynamicQuery): Promise<SessionAdminSetDTO> {
  const res = await authFetch('/v1/sessions/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as SessionAdminSetDTO
  return { ...set, items: set.items ?? [] }
}

// revokeSession 调用 POST /v1/sessions/revoke（body: "*"，幂等）。
export async function revokeSession(id: string): Promise<void> {
  const res = await authFetch('/v1/sessions/revoke', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
