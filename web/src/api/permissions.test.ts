import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { fetchMyPermissions } from './permissions'

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

describe('fetchMyPermissions', () => {
  it('gets_the_mine_endpoint_and_returns_the_code_array', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, { codes: ['sys:role', 'sys:user', 'sys:user:create'] }),
    )

    const codes = await fetchMyPermissions()

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/permissions/mine')
    expect(codes).toEqual(['sys:role', 'sys:user', 'sys:user:create'])
  })

  it('returns_an_empty_array_when_the_reply_omits_codes', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await expect(fetchMyPermissions()).resolves.toEqual([])
  })

  it('propagates_the_access_denied_error_on_403', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(403, { code: 403, reason: 'PERM_ACCESS_DENIED', message: 'permission denied' }),
    )

    await expect(fetchMyPermissions()).rejects.toThrow('permission denied')
  })
})
