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
  // 切分区分串：空串表示不按区分串切分；缺省（未返回）视为 "\n\n"。
  chunkDelimiter?: string
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
  // 省略取服务端缺省区分串 "\n\n"；空串关闭区分串切分。
  chunkDelimiter?: string
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
  // 省略保持原值；空串关闭区分串切分。
  chunkDelimiter?: string
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

export interface SearchDocumentsInput {
  query: string
  libraryId?: string
  topK?: number
}

// SearchHitDTO 是单条检索命中：documentId 为切片 ID，sourceId 为来源条目
// ID（可经 getDocument 查看条目原文）；content 为不可信原文，只按文本渲染。
export interface SearchHitDTO {
  documentId: string
  sourceId: string
  sourceName: string
  anchor: string
  score: number
  content: string
}

export interface SearchDocumentsResultDTO {
  items: SearchHitDTO[]
  total: number
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

// searchDocuments 检索租户内知识库。protojson 省略零值字段，命中缺省
// 键在此补齐为空串/0，空结果的 items/total 同样补齐。
export async function searchDocuments(input: SearchDocumentsInput): Promise<SearchDocumentsResultDTO> {
  const res = await authFetch('/v1/knowledge/documents/search', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(input),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const reply = (await res.json()) as { items?: Partial<SearchHitDTO>[]; total?: number }
  const items = (reply.items ?? []).map((hit) => ({
    documentId: hit.documentId ?? '',
    sourceId: hit.sourceId ?? '',
    sourceName: hit.sourceName ?? '',
    anchor: hit.anchor ?? '',
    score: hit.score ?? 0,
    content: hit.content ?? '',
  }))
  return { items, total: reply.total ?? 0 }
}
