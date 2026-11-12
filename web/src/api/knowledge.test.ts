import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createDocument,
  createLibrary,
  deleteDocument,
  deleteLibrary,
  getDocumentChunk,
  listDocumentChunks,
  listLibraries,
  listLibraryDocuments,
  listModelOptions,
  reindexDocument,
  searchDocuments,
  updateDocument,
  updateLibrary,
} from './knowledge'

const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  setSession(loginReply('access-1', 'refresh-1'))
})

afterEach(() => {
  clearSession()
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

function loginReply(accessToken: string, refreshToken: string): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken,
    refreshToken,
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: '7', tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function init(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function body(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = init(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

function libraryDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '100',
    status: 1,
    name: '手册',
    chunkSize: 800,
    chunkOverlap: 100,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function documentDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '200',
    libraryId: '100',
    kind: 'text',
    title: '退款政策',
    status: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('knowledge api', () => {
  it('creates_a_library_with_the_wrapped_library_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, libraryDTO()))
    await createLibrary({ status: 1, name: '手册', chunkSize: 800, chunkOverlap: 100 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/libraries/create')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({
      library: { status: 1, name: '手册', chunkSize: 800, chunkOverlap: 100 },
    })
  })

  it('creates_a_library_with_an_explicit_empty_chunk_delimiter', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, libraryDTO({ chunkDelimiter: '' })))
    const created = await createLibrary({ name: '手册', chunkDelimiter: '' })
    expect(body(fetchMock.mock.calls[0])).toEqual({ library: { name: '手册', chunkDelimiter: '' } })
    expect(created.chunkDelimiter).toBe('')
  })

  it('updates_a_library_with_the_library_message_as_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, libraryDTO()))
    await updateLibrary({ id: '100', status: 0, name: '手册', chunkSize: 800, chunkOverlap: 100 })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/libraries/update')
    expect(init(call).method).toBe('PUT')
    expect(body(call)).toEqual({ id: '100', status: 0, name: '手册', chunkSize: 800, chunkOverlap: 100 })
  })

  it('lists_libraries_and_normalizes_missing_items', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))
    const set = await listLibraries({ pagination: { page: 1, pageSize: 20 } })
    expect(set.items).toEqual([])
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/libraries/list')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({ pagination: { page: 1, pageSize: 20 } })
  })

  it('deletes_a_library_by_path_id', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await deleteLibrary('100')
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/libraries/100')
    expect(init(call).method).toBe('DELETE')
  })

  it('lists_model_options_with_the_kind_query', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [{ modelKey: 'qwen3-embedding:0.6b' }] }))
    const set = await listModelOptions('embedding')
    expect(set.items).toHaveLength(1)
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/model-options?kind=embedding')
    expect(init(call).method).toBeUndefined()
  })

  it('creates_a_document_with_the_wrapped_document_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, documentDTO()))
    await createDocument({ libraryId: '100', kind: 'text', title: '退款政策', content: '正文' })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/documents/create')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({
      document: { libraryId: '100', kind: 'text', title: '退款政策', content: '正文' },
    })
  })

  it('updates_a_document_with_the_document_message_as_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, documentDTO()))
    await updateDocument({ id: '200', status: 0, title: '退款政策' })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/documents/update')
    expect(init(call).method).toBe('PUT')
    expect(body(call)).toEqual({ id: '200', status: 0, title: '退款政策' })
  })

  it('lists_library_documents_with_page_and_page_size', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [documentDTO()], total: '1', hasMore: false, page: 1 }))
    const set = await listLibraryDocuments('100', 2, 20)
    expect(set.items).toHaveLength(1)
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/libraries/100/documents?page=2&pageSize=20')
  })

  it('deletes_a_document_by_path_id', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await deleteDocument('200')
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/documents/200')
    expect(init(call).method).toBe('DELETE')
  })

  it('reindexes_a_document_with_an_empty_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, documentDTO()))
    await reindexDocument('200')
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/documents/200/reindex')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({})
  })

  it('searches_documents_with_query_library_and_top_k_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: 0 }))
    await searchDocuments({ query: '年假', libraryId: '100', topK: 8 })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/knowledge/documents/search')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({ query: '年假', libraryId: '100', topK: 8 })
  })

  it('searches_documents_without_optional_fields_omits_them', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await searchDocuments({ query: '年假' })
    expect(body(fetchMock.mock.calls[0])).toEqual({ query: '年假' })
  })

  it('search_reply_with_omitted_zero_values_is_normalized', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, { items: [{ documentId: '200#0#a1b2c3d4', content: '正文' }], total: 1 }),
    )
    const result = await searchDocuments({ query: '年假' })
    expect(result).toEqual({
      items: [{ documentId: '200#0#a1b2c3d4', sourceId: '', sourceName: '', anchor: '', score: 0, content: '正文' }],
      total: 1,
    })
  })

  it('search_empty_reply_returns_no_items_and_zero_total', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    const result = await searchDocuments({ query: '年假' })
    expect(result).toEqual({ items: [], total: 0 })
  })

  it('search_error_response_throws_api_error', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(503, { code: 503, reason: 'SEARCH_UNAVAILABLE', message: 'knowledge search is unavailable' }),
    )
    await expect(searchDocuments({ query: '年假' })).rejects.toThrow('knowledge search is unavailable')
  })
})

describe('knowledge chunk api', () => {
  it('lists_document_chunks_by_path_id', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [{ index: 0, chunkId: '200#0#a1b2c3d4', content: '正文' }], total: 1 }))
    const set = await listDocumentChunks('200')
    expect(String(fetchMock.mock.calls[0][0])).toBe('/v1/knowledge/documents/200/chunks')
    expect(init(fetchMock.mock.calls[0]).method).toBeUndefined()
    expect(set).toEqual({
      items: [{ index: 0, chunkId: '200#0#a1b2c3d4', anchor: '', content: '正文', embedder: '' }],
      total: 1,
    })
  })

  it('empty_chunk_reply_normalizes_to_no_items', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    expect(await listDocumentChunks('200')).toEqual({ items: [], total: 0 })
  })

  it('chunk_error_response_throws_api_error', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(503, { code: 503, reason: 'DOCUMENT_CHUNKS_UNAVAILABLE', message: 'knowledge document chunks unavailable' }),
    )
    await expect(listDocumentChunks('200')).rejects.toThrow('knowledge document chunks unavailable')
  })

  it('get_document_chunk_encodes_the_chunk_id_and_normalizes_the_detail', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        chunk: { index: 1, chunkId: '200#1#a1b2c3d4', anchor: 'runes 10-20', content: '正文', embedder: 'space@v1' },
        document: documentDTO({ id: '200', title: '退款政策' }),
        supersededAt: '2026-02-03T04:05:06Z',
      }),
    )
    const detail = await getDocumentChunk('200#1#a1b2c3d4')
    expect(String(fetchMock.mock.calls[0][0])).toBe('/v1/knowledge/documents/chunks/200%231%23a1b2c3d4')
    expect(detail.chunk).toEqual({
      index: 1,
      chunkId: '200#1#a1b2c3d4',
      anchor: 'runes 10-20',
      content: '正文',
      embedder: 'space@v1',
    })
    expect(detail.document.id).toBe('200')
    expect(detail.supersededAt).toBe('2026-02-03T04:05:06Z')
  })

  it('get_document_chunk_empty_response_normalizes_missing_fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    expect(await getDocumentChunk('200#0#a1b2c3d4')).toEqual({
      chunk: { index: 0, chunkId: '', anchor: '', content: '', embedder: '' },
      document: {
        id: '',
        libraryId: '',
        kind: '',
        title: '',
        content: undefined,
        sourceName: undefined,
        status: undefined,
        validFrom: undefined,
        validTo: undefined,
        indexedAt: undefined,
        indexedEmbedder: undefined,
        createdBy: '',
        updatedBy: '',
        createdAt: '',
        updatedAt: undefined,
        deletedAt: undefined,
      },
      supersededAt: '',
    })
  })
})
