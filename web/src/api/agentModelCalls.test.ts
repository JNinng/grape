import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import { fetchModelCalls } from './agentModelCalls'

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

describe('fetchModelCalls list loading', () => {
  it('requests_the_default_page_without_filters_and_normalizes_missing_items', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [
          {
            id: 1,
            scenario: 'embed_document',
            status: 'success',
            modelKey: 'bge-m1024',
            endpoint: 'http://embed.example/v1/embeddings',
            documentId: 42,
            inputSummary: 'chunk body',
            outputSummary: 'embedding vector of 1024 dimensions (vector not persisted)',
            inputTokens: 96,
            outputTokens: null,
            durationMs: 18,
            createdAt: '2026-01-01T00:00:00Z',
          },
        ],
        total: 1,
      }),
    )

    const rows = await fetchModelCalls()

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/agent/model-calls?offset=0&limit=50')
    expect(rows).toHaveLength(1)
    expect(rows[0].scenario).toBe('embed_document')
    expect(rows[0].outputTokens).toBeNull()
  })

  it('forwards_nonempty_filters_to_the_query_string', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: 0 }))

    await fetchModelCalls({ scenario: 'parse_document', modelKey: 'glm-4', status: 'error' }, 10, 20)

    expect(fetchMock.mock.calls[0][0]).toBe(
      '/v1/agent/model-calls?offset=10&limit=20&scenario=parse_document&model_key=glm-4&status=error',
    )
  })

  it('returns_an_empty_list_when_the_body_has_no_items', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { total: 0 }))

    const rows = await fetchModelCalls()

    expect(rows).toEqual([])
  })

  it('propagates_the_backend_error_message_on_failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid model call log argument' }))

    await expect(fetchModelCalls()).rejects.toThrow('invalid model call log argument')
  })
})
