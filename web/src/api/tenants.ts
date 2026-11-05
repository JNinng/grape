import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// TenantService 各端点（api/tenant/v1/tenant.proto）的前端客户端：平台级
// 租户实体（sys_tenant）的 CRUD 与停用/恢复。序列化口径与后端 jsoncodec
// （protojson 覆盖）一致：字段小驼峰、int64 为字符串、时间为 RFC3339、
// optional 未设置时省略。

// TenantDTO 对齐 tenant.v1.Tenant：id 是字符串型主键；createdBy/updatedBy
// 为字符串型 int64；status 为 optional（未设置省略）；可空字段未设置省略。
export interface TenantDTO {
  id: string
  status?: number
  code: string
  name: string
  parentId?: string
  contactName?: string
  contactPhone?: string
  expiredAt?: string
  maxUserCount?: number
  defaultLocale: string
  tenantId: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// TenantSetDTO 对齐 tenant.v1.TenantSet：total 为字符串型 int64，
// include_total=false 时缺省。
export interface TenantSetDTO {
  items: TenantDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// CreateTenantInput 对齐 tenant.v1.CreateTenantSuperRequest.tenant 的可填
// 字段：id、tenantId、审计列与时间戳由服务端落值；parentId 只允许省略
//（缺省挂调用方租户）或等于调用方租户，页面直接省略；status 缺省启用，
// defaultLocale 空则服务端缺省 zh-CN。
export interface CreateTenantInput {
  status?: number
  code: string
  name: string
  parentId?: string
  contactName?: string
  contactPhone?: string
  expiredAt?: string
  maxUserCount?: number
  defaultLocale?: string
}

// UpdateTenantInput 对齐 tenant.v1.UpdateTenantSuperRequest.tenant：更新为
// 覆盖语义，status 必填；code（身份列）、parentId（结构列）与 tenantId
//（归属列）在输入中被服务端忽略，前端不上送。expiredAt/maxUserCount 省略
// 即清空（落 NULL）。
export interface UpdateTenantInput {
  id: string
  status: number
  name: string
  contactName?: string
  contactPhone?: string
  expiredAt?: string
  maxUserCount?: number
  defaultLocale?: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// createTenant 调用 POST /v1/tenants/create（body: "*"，请求消息外层字段
// 为 tenant）；code/name 未删除租户间冲突返回 409。
export async function createTenant(tenant: CreateTenantInput): Promise<TenantDTO> {
  const res = await authFetch('/v1/tenants/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ tenant }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantDTO
}

// fetchTenant 调用 GET /v1/tenants/{id}：目标不存在、已软删或租户树外
// 不可见均返回 404。
export async function fetchTenant(id: string): Promise<TenantDTO> {
  const res = await authFetch(`/v1/tenants/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantDTO
}

// listTenants 调用 POST /v1/tenants/list（body: "query"，Query 消息即体，
// 平铺分页列表）。零命中时响应没有 items 键，补齐为空数组。
export async function listTenants(query?: DynamicQuery): Promise<TenantSetDTO> {
  const res = await authFetch('/v1/tenants/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as TenantSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateTenant 调用 PUT /v1/tenants/update（body: "tenant"，Tenant 消息
// 即体，覆盖 status/name/联系人/到期时间/最大用户数/默认语言）。
export async function updateTenant(tenant: UpdateTenantInput): Promise<TenantDTO> {
  const res = await authFetch('/v1/tenants/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(tenant),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantDTO
}

// deleteTenant 调用 DELETE /v1/tenants/{id}（软删除；存在未删除子租户时
// 后端返回 TENANT_HAS_CHILDREN）。
export async function deleteTenant(id: string): Promise<void> {
  const res = await authFetch(`/v1/tenants/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// disableTenant 调用 POST /v1/tenants/{id}/disable（status 置 0，幂等目标
// 状态写）；禁止停用调用方自身租户。
export async function disableTenant(id: string): Promise<TenantDTO> {
  const res = await authFetch(`/v1/tenants/${encodeURIComponent(id)}/disable`, { method: 'POST' })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantDTO
}

// restoreTenant 调用 POST /v1/tenants/{id}/restore（status 置 1，幂等目标
// 状态写）；禁止恢复调用方自身租户。
export async function restoreTenant(id: string): Promise<TenantDTO> {
  const res = await authFetch(`/v1/tenants/${encodeURIComponent(id)}/restore`, { method: 'POST' })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantDTO
}
