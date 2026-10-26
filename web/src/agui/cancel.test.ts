import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import { cancelRun } from './cancel'

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

describe('cancelRun request contract', () => {
  it('posts_thread_and_run_ids_to_the_agui_cancel_endpoint', async () => {
    fetchMock.mockResolvedValue(new Response('', { status: 200 }))

    const result = await cancelRun('thread-1', 'run-1')

    expect(result).toBe('cancelled')
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/agui/cancel')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toMatchObject({ threadId: 'thread-1', runId: 'run-1' })
  })

  it('treats_404_as_a_race_where_the_run_already_ended', async () => {
    fetchMock.mockResolvedValue(new Response('not found', { status: 404 }))

    const result = await cancelRun('thread-1', 'run-1')

    expect(result).toBe('not-running')
  })

  it('throws_on_unexpected_http_failures', async () => {
    fetchMock.mockResolvedValue(new Response('boom', { status: 500 }))

    await expect(cancelRun('thread-1', 'run-1')).rejects.toThrow('cancel failed: HTTP 500')
  })
})
