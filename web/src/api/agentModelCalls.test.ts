import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import { listModelCalls } from './agentModelCalls'

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

describe('listModelCalls dynamic query', () => {
  it('posts_the_dynamic_query_and_normalizes_missing_items', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [
          {
            id: '1',
            userId: '7',
            scenario: 'embed_document',
            status: 'success',
            modelKey: 'bge-m1024',
            endpoint: 'http://embed.example/v1/embeddings',
            documentId: '42',
            inputSummary: 'chunk body',
            outputSummary: 'embedding vector of 1024 dimensions (vector not persisted)',
            inputTokens: 96,
            durationMs: 18,
            createdBy: '7',
            createdAt: '2026-01-01T00:00:00Z',
          },
        ],
        total: '1',
        hasMore: false,
        nextCursor: '',
        page: 1,
      }),
    )

    const set = await listModelCalls({
      where: [{ field: 'scenario', op: 'eq', value: 'embed_document' }],
      pagination: { page: 1, pageSize: 20 },
    })

    const [target, init] = fetchMock.mock.calls[0]
    expect(target).toBe('/v1/agent/model-calls/list')
    expect((init as RequestInit).method).toBe('POST')
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({
      where: [{ field: 'scenario', op: 'eq', value: 'embed_document' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(set.items).toHaveLength(1)
    // protojson 口径：int64 为字符串、optional 缺省即未上报（不以 0 冒充）。
    expect(set.items[0].id).toBe('1')
    expect(set.items[0].documentId).toBe('42')
    expect(set.items[0].outputTokens).toBeUndefined()
    expect(set.total).toBe('1')
  })

  it('sends_an_empty_object_without_a_query_and_returns_an_empty_list', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listModelCalls()

    expect(JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string)).toEqual({})
    expect(set.items).toEqual([])
    expect(set.hasMore).toBe(false)
  })

  it('propagates_the_backend_error_message_on_failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid model call log argument' }))

    await expect(listModelCalls()).rejects.toThrow('invalid model call log argument')
  })
})
