import { authFetch } from './http'
import { toApiError } from './toApiError'

// ModelService 各端点（api/model/v1/model.proto）的前端客户端。
// 密钥只写不读：响应只返回 hasApiKey；更新时省略 apiKey 表示不变，
// clearApiKey 表示清除。

export interface ModelDTO {
  id: string
  platform: boolean
  status?: number
  kind: string
  name: string
  modelKey: string
  provider: string
  baseUrl?: string
  model: string
  dimension?: number
  timeoutMs?: number
  hasApiKey: boolean
  apiKeyEnv?: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

export interface ModelSetDTO {
  items: ModelDTO[]
}

export interface CreateModelInput {
  status?: number
  kind: string
  name: string
  modelKey: string
  provider?: string
  baseUrl?: string
  model: string
  dimension?: number
  timeoutMs?: number
  apiKeyEnv?: string
  remark?: string
}

// ModelUpdateSpec 是更新变更集：只含创建后可变字段。类别（kind）与
// modelKey 是创建后不可变的身份列，不在此类型内，客户端无法经更新协议
// 伪造。
export interface ModelUpdateSpec {
  status: number
  name: string
  provider?: string
  baseUrl?: string
  model: string
  dimension?: number
  timeoutMs?: number
  apiKeyEnv?: string
  remark?: string
}

export interface UpdateModelInput {
  id: string
  model: ModelUpdateSpec
  apiKey?: string
  clearApiKey?: boolean
}

function compact(body: object): string {
  return JSON.stringify(body)
}

// MODEL_KINDS 是模型类别封闭枚举（与 sys_model_kind_chk 同口径）。
export const MODEL_KINDS = ['text', 'embedding', 'rerank'] as const

export type ModelKind = (typeof MODEL_KINDS)[number]

export async function listModels(kind?: ModelKind): Promise<ModelSetDTO> {
  const search = kind ? `?kind=${encodeURIComponent(kind)}` : ''
  const res = await authFetch(`/v1/models${search}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as ModelSetDTO
  return { items: set.items ?? [] }
}

export async function getModel(id: string): Promise<ModelDTO> {
  const res = await authFetch(`/v1/models/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ModelDTO
}

export async function createModel(model: CreateModelInput, apiKey?: string): Promise<ModelDTO> {
  const res = await authFetch('/v1/models/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ model, ...(apiKey ? { apiKey } : {}) }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ModelDTO
}

export async function updateModel(input: UpdateModelInput): Promise<ModelDTO> {
  const res = await authFetch('/v1/models/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact({
      id: input.id,
      model: input.model,
      ...(input.apiKey ? { apiKey: input.apiKey } : {}),
      ...(input.clearApiKey ? { clearApiKey: true } : {}),
    }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ModelDTO
}

export async function deleteModel(id: string): Promise<void> {
  const res = await authFetch(`/v1/models/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

export async function createModelSuper(model: CreateModelInput, apiKey?: string): Promise<ModelDTO> {
  const res = await authFetch('/v1/models/platform/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ model, ...(apiKey ? { apiKey } : {}) }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ModelDTO
}

export async function updateModelSuper(input: UpdateModelInput): Promise<ModelDTO> {
  const res = await authFetch('/v1/models/platform/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact({
      id: input.id,
      model: input.model,
      ...(input.apiKey ? { apiKey: input.apiKey } : {}),
      ...(input.clearApiKey ? { clearApiKey: true } : {}),
    }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ModelDTO
}

export async function deleteModelSuper(id: string): Promise<void> {
  const res = await authFetch(`/v1/models/platform/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
