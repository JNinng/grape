import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import { fetchAgentSessions } from './agentSessions'

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
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('fetchAgentSessions list loading', () => {
  it('requests_the_default_page_and_sorts_by_updated_desc', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        sessions: [
          { id: 's-old', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z' },
          { id: 's-new', createdAt: '2026-01-02T00:00:00Z', updatedAt: '2026-01-03T00:00:00Z' },
        ],
      }),
    )

    const sessions = await fetchAgentSessions()

    expect(fetchMock.mock.calls[0][0]).toBe('/agui/sessions?offset=0&limit=50')
    expect(sessions.map((s) => s.id)).toEqual(['s-new', 's-old'])
  })

  it('returns_an_empty_list_when_the_body_has_no_sessions', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { sessions: [] }))

    const sessions = await fetchAgentSessions()

    expect(sessions).toEqual([])
  })

  it('propagates_the_backend_error_message_on_failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { message: 'token expired' }))

    await expect(fetchAgentSessions()).rejects.toThrow('token expired')
  })
})
