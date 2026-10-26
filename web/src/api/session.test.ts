import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, getAccessToken, getRefreshToken, setSession } from '../auth/store'
import { login, logout } from './session'

vi.mock('./http', () => ({
  authFetch: vi.fn(),
  toApiError: (res: Response) => new Error(`HTTP ${res.status}`),
}))

import { authFetch } from './http'

const mockAuthFetch = vi.mocked(authFetch)

afterEach(() => {
  clearSession()
  mockAuthFetch.mockReset()
})

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

describe('login', () => {
  it('posts credentials without an empty tenant and stores the session', async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json(loginReply(), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await login({ tenantId: '  ', userName: 'alice', password: 'secret' })

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/v1/auth/login')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({ userName: 'alice', password: 'secret' })
    expect(getAccessToken()).toBe('access-1')
    expect(getRefreshToken()).toBe('refresh-1')
    vi.unstubAllGlobals()
  })

  it('sends the tenant when provided', async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json(loginReply(), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await login({ tenantId: 't-9', userName: 'alice', password: 'secret' })

    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body)).tenantId).toBe('t-9')
    vi.unstubAllGlobals()
  })

  it('leaves the session untouched when login is rejected', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(Response.json({ message: 'bad password' }, { status: 401 })),
    )

    // 错误文案来自后端错误体的 message（真实 toApiError 解析）。
    await expect(login({ tenantId: '', userName: 'alice', password: 'nope' })).rejects.toThrow(
      'bad password',
    )
    expect(getAccessToken()).toBeNull()
    expect(getRefreshToken()).toBeNull()
    vi.unstubAllGlobals()
  })
})

describe('logout', () => {
  it('asks the backend to revoke the current access token', async () => {
    setSession(loginReply())
    mockAuthFetch.mockResolvedValue(new Response(null, { status: 200 }))

    await logout()

    expect(mockAuthFetch).toHaveBeenCalledTimes(1)
    expect(mockAuthFetch).toHaveBeenCalledWith('/v1/auth/logout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    })
  })

  it('clears the local session even when the backend fails', async () => {
    setSession(loginReply())
    mockAuthFetch.mockResolvedValue(new Response(null, { status: 500 }))

    await logout()

    expect(getAccessToken()).toBeNull()
    expect(getRefreshToken()).toBeNull()
  })

  it('clears the local session when the backend is unreachable', async () => {
    setSession(loginReply())
    mockAuthFetch.mockRejectedValue(new TypeError('network down'))

    await expect(logout()).resolves.toBeUndefined()
    expect(getAccessToken()).toBeNull()
  })

  it('skips the backend call when no access token exists', async () => {
    clearSession()

    await logout()

    expect(mockAuthFetch).not.toHaveBeenCalled()
  })
})
