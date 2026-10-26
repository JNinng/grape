import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { setSessionExpiredHandler } from '../auth/callbacks'
import {
  clearSession,
  getAccessToken,
  getRefreshToken,
  setSession,
} from '../auth/store'
import { authFetch } from './http'

const fetchMock = vi.fn<typeof fetch>()

beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  clearSession()
  setSessionExpiredHandler(() => {})
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

// 路由 mock：refresh 端点单独计数；业务路径按请求所附令牌应答——旧
// 令牌一律 401、新令牌一律 200，使并发交织下的断言确定。
function routeFetch(options: { refreshReply: Response }) {
  let refreshCalls = 0
  let businessCalls = 0
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input)
    if (url === '/v1/auth/refresh') {
      refreshCalls += 1
      return options.refreshReply
    }
    businessCalls += 1
    const auth = new Headers(init?.headers).get('Authorization')
    if (auth === 'Bearer access-1') {
      return jsonResponse(401, { message: 'expired' })
    }
    return jsonResponse(200, { ok: true })
  })
  return {
    refreshCalls: () => refreshCalls,
    businessCalls: () => businessCalls,
  }
}

describe('authFetch 401 state machine', () => {
  it('attaches the bearer token to outgoing requests', async () => {
    setSession(loginReply('access-1', 'refresh-1'))
    fetchMock.mockResolvedValue(jsonResponse(200, { ok: true }))

    const res = await authFetch('/v1/users/list')

    expect(res.status).toBe(200)
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers)
    expect(headers.get('Authorization')).toBe('Bearer access-1')
  })

  it('retries once with the rotated token after a 401 refresh', async () => {
    setSession(loginReply('access-1', 'refresh-1'))
    const routed = routeFetch({
      refreshReply: jsonResponse(200, loginReply('access-2', 'refresh-2')),
    })

    const res = await authFetch('/v1/users/list')

    expect(res.status).toBe(200)
    expect(routed.refreshCalls()).toBe(1)
    expect(routed.businessCalls()).toBe(2)
    expect(getAccessToken()).toBe('access-2')
    const retryHeaders = new Headers(fetchMock.mock.calls[2][1]?.headers)
    expect(retryHeaders.get('Authorization')).toBe('Bearer access-2')
  })

  it('shares one refresh across concurrent 401 requests', async () => {
    setSession(loginReply('access-1', 'refresh-1'))
    const routed = routeFetch({
      refreshReply: jsonResponse(200, loginReply('access-2', 'refresh-2')),
    })

    const [first, second] = await Promise.all([
      authFetch('/v1/users/list'),
      authFetch('/v1/users/list'),
    ])

    expect(first.status).toBe(200)
    expect(second.status).toBe(200)
    expect(routed.refreshCalls()).toBe(1)
    expect(routed.businessCalls()).toBe(4)
  })

  it('clears the session and notifies handlers when refresh fails', async () => {
    setSession(loginReply('access-1', 'refresh-1'))
    const expired = vi.fn()
    setSessionExpiredHandler(expired)
    const routed = routeFetch({
      refreshReply: jsonResponse(401, { message: 'refresh rejected' }),
    })

    const res = await authFetch('/v1/users/list')

    expect(res.status).toBe(401)
    expect(expired).toHaveBeenCalledTimes(1)
    expect(getAccessToken()).toBeNull()
    expect(getRefreshToken()).toBeNull()
    expect(routed.businessCalls()).toBe(1)
  })

  it('does not attempt a refresh when no token was attached', async () => {
    clearSession()
    const expired = vi.fn()
    setSessionExpiredHandler(expired)
    fetchMock.mockResolvedValue(jsonResponse(401, { message: 'anonymous' }))

    const res = await authFetch('/v1/users/list')

    expect(res.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(expired).not.toHaveBeenCalled()
  })

  it('passes non-401 responses through untouched', async () => {
    setSession(loginReply('access-1', 'refresh-1'))
    fetchMock.mockResolvedValue(jsonResponse(500, { message: 'boom' }))

    const res = await authFetch('/v1/users/list')

    expect(res.status).toBe(500)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
