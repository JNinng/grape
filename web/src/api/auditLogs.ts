import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// AuditService 各端点（api/audit/v1/audit.proto）的前端客户端：登录日志与
// 操作日志只读查询。序列化口径与后端 jsoncodec（protojson 覆盖）一致：
// 字段小驼峰、int64 为字符串、时间为 RFC3339、optional 未设置时省略。

// LoginLogDTO 对齐 audit.v1.LoginLog：全部字段只读（append-only）。
export interface LoginLogDTO {
  id: string
  userId: string
  username: string
  ipAddress?: string
  userAgent?: string
  loginType: string
  isSuccess: boolean
  failureReason?: string
  loginAt: string
  createdBy: string
  createdAt: string
}

// LoginLogSetDTO 对齐 audit.v1.LoginLogSet。
export interface LoginLogSetDTO {
  items: LoginLogDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// OperationLogDTO 对齐 audit.v1.OperationLog：old/new_value 为 JSON 对象
//（快照缺失或非对象时缺省）。
export interface OperationLogDTO {
  id: string
  userId: string
  username: string
  operationType: string
  targetTable?: string
  targetId?: string
  oldValue?: Record<string, unknown>
  newValue?: Record<string, unknown>
  ipAddress?: string
  userAgent?: string
  apiPath?: string
  httpMethod?: string
  durationMs?: number
  resultCode?: string
  resultMsg?: string
  requestId?: string
  operatedAt: string
  createdBy: string
  createdAt: string
}

// OperationLogSetDTO 对齐 audit.v1.OperationLogSet。
export interface OperationLogSetDTO {
  items: OperationLogDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// listLoginLogs 调用 POST /v1/audit/login-logs/list（body: "query"）。
// 零命中时响应没有 items 键，补齐为空数组。
export async function listLoginLogs(query?: DynamicQuery): Promise<LoginLogSetDTO> {
  const res = await authFetch('/v1/audit/login-logs/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as LoginLogSetDTO
  return { ...set, items: set.items ?? [] }
}

// fetchLoginLog 调用 GET /v1/audit/login-logs/{id}：跨租户不可见等同不存在
//（404 语义）。
export async function fetchLoginLog(id: string): Promise<LoginLogDTO> {
  const res = await authFetch(`/v1/audit/login-logs/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as LoginLogDTO
}

// listOperationLogs 调用 POST /v1/audit/operation-logs/list。
export async function listOperationLogs(query?: DynamicQuery): Promise<OperationLogSetDTO> {
  const res = await authFetch('/v1/audit/operation-logs/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as OperationLogSetDTO
  return { ...set, items: set.items ?? [] }
}

// fetchOperationLog 调用 GET /v1/audit/operation-logs/{id}。
export async function fetchOperationLog(id: string): Promise<OperationLogDTO> {
  const res = await authFetch(`/v1/audit/operation-logs/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as OperationLogDTO
}
