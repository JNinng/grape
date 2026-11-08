import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// LibraryService / DocumentService 各端点（api/knowledge/v1/knowledge.proto）
// 的前端客户端。序列化口径与后端 jsoncodec（protojson 覆盖）一致：字段
// 小驼峰、int64 为字符串、时间为 RFC3339、optional 未设置时省略。

export interface LibraryDTO {
  id: string
  status?: number
  name: string
  remark?: string
  embeddingModelKey?: string
  chunkSize: number
  chunkOverlap: number
  validFrom?: string
  validTo?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

export interface LibrarySetDTO {
  items: LibraryDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

export interface DocumentDTO {
  id: string
  libraryId: string
  kind: string
  title: string
  content?: string
  sourceName?: string
  status?: number
  validFrom?: string
  validTo?: string
  indexedAt?: string
  indexedEmbedder?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

export interface DocumentSetDTO {
  items: DocumentDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

export interface ModelOptionDTO {
  modelKey: string
  name: string
  kind: string
  dimension: number
  platform: boolean
}

export interface ModelOptionSetDTO {
  items: ModelOptionDTO[]
}

export interface CreateLibraryInput {
  status?: number
  name: string
  remark?: string
  embeddingModelKey?: string
  chunkSize?: number
  chunkOverlap?: number
  validFrom?: string
  validTo?: string
}

export interface UpdateLibraryInput {
  id: string
  status: number
  name: string
  remark?: string
  embeddingModelKey?: string
  chunkSize: number
  chunkOverlap: number
  validFrom?: string
  validTo?: string
}

export interface CreateDocumentInput {
  libraryId: string
  kind?: string
  title: string
  content: string
  sourceName?: string
  status?: number
  validFrom?: string
  validTo?: string
}

export interface UpdateDocumentInput {
  id: string
  status: number
  title: string
  content?: string
  sourceName?: string
  validFrom?: string
  validTo?: string
}

function compact(body: object): string {
  return JSON.stringify(body)
}

export async function createLibrary(library: CreateLibraryInput): Promise<LibraryDTO> {
  const res = await authFetch('/v1/knowledge/libraries/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ library }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as LibraryDTO
}

export async function getLibrary(id: string): Promise<LibraryDTO> {
  const res = await authFetch(`/v1/knowledge/libraries/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as LibraryDTO
}

export async function listLibraries(query?: DynamicQuery): Promise<LibrarySetDTO> {
  const res = await authFetch('/v1/knowledge/libraries/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as LibrarySetDTO
  return { ...set, items: set.items ?? [] }
}

export async function updateLibrary(library: UpdateLibraryInput): Promise<LibraryDTO> {
  const res = await authFetch('/v1/knowledge/libraries/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(library),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as LibraryDTO
}

export async function deleteLibrary(id: string): Promise<void> {
  const res = await authFetch(`/v1/knowledge/libraries/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

export async function listModelOptions(kind?: 'text' | 'embedding'): Promise<ModelOptionSetDTO> {
  const search = kind ? `?kind=${encodeURIComponent(kind)}` : ''
  const res = await authFetch(`/v1/knowledge/model-options${search}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as ModelOptionSetDTO
  return { items: set.items ?? [] }
}

export async function createDocument(document: CreateDocumentInput): Promise<DocumentDTO> {
  const res = await authFetch('/v1/knowledge/documents/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ document }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DocumentDTO
}

export async function getDocument(id: string): Promise<DocumentDTO> {
  const res = await authFetch(`/v1/knowledge/documents/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DocumentDTO
}

export async function listDocuments(query?: DynamicQuery): Promise<DocumentSetDTO> {
  const res = await authFetch('/v1/knowledge/documents/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as DocumentSetDTO
  return { ...set, items: set.items ?? [] }
}

export async function listLibraryDocuments(
  libraryId: string,
  page: number,
  pageSize: number,
): Promise<DocumentSetDTO> {
  const search = new URLSearchParams({ page: String(page), pageSize: String(pageSize) })
  const res = await authFetch(`/v1/knowledge/libraries/${encodeURIComponent(libraryId)}/documents?${search}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as DocumentSetDTO
  return { ...set, items: set.items ?? [] }
}

export async function updateDocument(document: UpdateDocumentInput): Promise<DocumentDTO> {
  const res = await authFetch('/v1/knowledge/documents/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(document),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DocumentDTO
}

export async function deleteDocument(id: string): Promise<void> {
  const res = await authFetch(`/v1/knowledge/documents/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

export async function reindexDocument(id: string): Promise<DocumentDTO> {
  const res = await authFetch(`/v1/knowledge/documents/${encodeURIComponent(id)}/reindex`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DocumentDTO
}
