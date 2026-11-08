import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { listSessions, revokeSession } from './sessionAdmin'

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

// requestInit 便于从 fetch 调用参数还原 method / path / body 的断言。
function requestInit(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function sentBody(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = requestInit(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

function sessionDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '5',
    status: 1,
    userId: '7',
    expiresAt: '2026-01-01T00:30:00Z',
    refreshExpiresAt: '2026-01-08T00:00:00Z',
    deviceInfo: 'Windows Chrome',
    ipAddress: '192.168.1.100',
    isRevoked: false,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('listSessions', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listSessions({ where: [{ field: 'userId', op: 'eq', value: '7' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/sessions/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'userId', op: 'eq', value: '7' }],
    })
    expect(set.items).toEqual([])
  })

  it('returns_items_with_revocation_state', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {
      items: [sessionDTO(), sessionDTO({ id: '6', isRevoked: true })],
      hasMore: true,
      nextCursor: 'cur',
      page: 1,
      total: '2',
    }))

    const set = await listSessions()

    expect(set.items).toHaveLength(2)
    expect(set.items[0].isRevoked).toBe(false)
    expect(set.items[1].isRevoked).toBe(true)
    // 令牌哈希不进入 DTO：类型面即无该字段，运行时断言未泄露。
    expect(set.items[0]).not.toHaveProperty('tokenHash')
    expect(set.items[0]).not.toHaveProperty('refreshTokenHash')
    expect(set.total).toBe('2')
    expect(set.nextCursor).toBe('cur')
  })

  it('propagates_the_backend_rejection_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid session argument' }))

    await expect(listSessions()).rejects.toThrow('invalid session argument')
  })
})

describe('revokeSession', () => {
  it('posts_the_wrapped_id_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await revokeSession('5')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/sessions/revoke')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5' })
  })

  it('propagates_the_backend_rejection_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid session argument' }))

    await expect(revokeSession('0')).rejects.toThrow('invalid session argument')
  })
})
