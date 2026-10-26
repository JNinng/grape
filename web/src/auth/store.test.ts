import { afterEach, describe, expect, it } from 'vitest'
import type { LoginReply } from '../api/types'
import {
  clearSession,
  getAccessToken,
  getRefreshToken,
  getUser,
  refreshTokenStorageKey,
  setSession,
} from './store'

function loginReply(overrides: Partial<LoginReply> = {}): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
    ...overrides,
  }
}

afterEach(() => {
  clearSession()
})

describe('session store', () => {
  it('keeps the access token in memory only', () => {
    setSession(loginReply())

    expect(getAccessToken()).toBe('access-1')
    expect(sessionStorage.getItem(refreshTokenStorageKey)).not.toContain('access')
  })

  it('persists the refresh token in sessionStorage', () => {
    setSession(loginReply())

    expect(getRefreshToken()).toBe('refresh-1')
    expect(sessionStorage.getItem(refreshTokenStorageKey)).toBe('refresh-1')
  })

  it('keeps the session user for UI display', () => {
    setSession(loginReply())

    expect(getUser()).toEqual({ id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' })
  })

  it('stores an absent user as null', () => {
    setSession(loginReply({ user: undefined }))

    expect(getUser()).toBeNull()
  })

  it('drops every session fact on clear', () => {
    setSession(loginReply())

    clearSession()

    expect(getAccessToken()).toBeNull()
    expect(getRefreshToken()).toBeNull()
    expect(getUser()).toBeNull()
  })
})
