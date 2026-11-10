import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { fetchSessionUsage } from './agentSessionUsage'

// 会话用量端点走真实 authFetch（只 stub 全局 fetch）：断言请求形状、事实
// 透传与 404/畸形响应的降级口径。
const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
  setSession(loginReply())
})

afterEach(() => {
  clearSession()
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
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

describe('fetchSessionUsage', () => {
  it('queries_the_thread_and_returns_the_fact_list', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        sessionId: 'thread one',
        facts: [
          {
            messageId: 'chatcmpl-1',
            model: 'qwen3:8b',
            usage: { prompt: 1973, completion: 145, total: 2118, cached: 1972 },
            reasoningMs: 2648,
            firstTokenMs: 143,
            createdAt: '2026-01-01T00:00:01Z',
          },
        ],
      }),
    )

    const facts = await fetchSessionUsage('thread one')

    expect(fetchMock.mock.calls[0][0]).toBe('/agui/session-usage?threadId=thread%20one')
    expect(facts).toHaveLength(1)
    expect(facts[0].messageId).toBe('chatcmpl-1')
    expect(facts[0].model).toBe('qwen3:8b')
    expect(facts[0].usage?.total).toBe(2118)
  })

  it('treats_the_owner_guard_404_as_no_facts', async () => {
    // 会话不可见或尚未建档：用量是附属证据，按空列表收敛。
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'session not found' }))

    await expect(fetchSessionUsage('thread-1')).resolves.toEqual([])
  })

  it('returns_an_empty_list_when_facts_is_missing_or_malformed', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { sessionId: 'thread-1' }))

    await expect(fetchSessionUsage('thread-1')).resolves.toEqual([])
  })

  it('throws_on_other_http_failures', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { message: 'boom' }))

    await expect(fetchSessionUsage('thread-1')).rejects.toThrow('session usage failed: HTTP 500')
  })
})
