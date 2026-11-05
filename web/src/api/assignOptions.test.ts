import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  listAssignableRoles,
  listOwnerDeptOptions,
  listOwnerGroupOptions,
  listOwnerUserOptions,
  listUserRoles,
  setUserRoles,
} from './assignOptions'

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
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function requestInit(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function sentBody(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = requestInit(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

describe('listAssignableRoles', () => {
  it('posts_the_target_and_option_query_and_defaults_items', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, page: 1 }))

    const set = await listAssignableRoles('11', { keyword: '普通', page: 1, pageSize: 20 })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/assignable-roles')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      targetUserId: '11',
      query: { keyword: '普通', page: 1, pageSize: 20 },
    })
    expect(set.items).toEqual([])
  })

  it('propagates_the_not_found_reason_for_an_invisible_target', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { reason: 'USER_NOT_FOUND', message: 'user not found' }))

    await expect(listAssignableRoles('11')).rejects.toMatchObject({
      message: 'user not found',
      reason: 'USER_NOT_FOUND',
    })
  })
})

describe('listUserRoles', () => {
  it('gets_the_roles_path_and_keeps_the_string_unmanaged_count', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [
          { value: '103', label: '普通用户', enabled: true, reason: '', assigned: true },
        ],
        unmanagedCount: '2',
        version: 'ver-1',
      }),
    )

    const set = await listUserRoles('11')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/11/roles')
    expect(requestInit(fetchMock.mock.calls[0]).method).toBeUndefined()
    expect(set.unmanagedCount).toBe('2')
    expect(set.version).toBe('ver-1')
    expect(set.items[0]?.value).toBe('103')
  })

  it('defaults_items_and_unmanaged_count_when_the_set_omits_them', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { version: 'ver-1' }))

    const set = await listUserRoles('11')

    expect(set.items).toEqual([])
    expect(set.unmanagedCount).toBe('0')
  })
})

describe('setUserRoles', () => {
  it('posts_the_managed_set_with_the_expected_version', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [{ value: '103', label: '普通用户', enabled: true, reason: '', assigned: true }],
        unmanagedCount: '0',
        version: 'ver-2',
      }),
    )

    const set = await setUserRoles('11', ['103', '104'], 'ver-1')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/set-roles')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      userId: '11',
      roleIds: ['103', '104'],
      expectedVersion: 'ver-1',
    })
    expect(set.version).toBe('ver-2')
  })

  it('surfaces_the_conflict_reason_for_a_stale_version', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, { reason: 'USER_ROLE_SET_CHANGED', message: 'user role set changed' }),
    )

    await expect(setUserRoles('11', ['103'], 'ver-1')).rejects.toMatchObject({
      reason: 'USER_ROLE_SET_CHANGED',
    })
  })
})

describe('owner option endpoints', () => {
  it('posts_the_option_query_to_each_source', async () => {
    fetchMock.mockImplementation(async () =>
      jsonResponse(200, { items: [], hasMore: false, page: 1 }),
    )

    await listOwnerUserOptions({ keyword: '张', page: 1, pageSize: 20 })
    await listOwnerDeptOptions({ page: 1 })
    await listOwnerGroupOptions({ page: 2, pageSize: 10 })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/owner-user-options')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      query: { keyword: '张', page: 1, pageSize: 20 },
    })
    expect(fetchMock.mock.calls[1][0]).toBe('/v1/users/owner-dept-options')
    expect(sentBody(fetchMock.mock.calls[1])).toEqual({ query: { page: 1 } })
    expect(fetchMock.mock.calls[2][0]).toBe('/v1/users/owner-group-options')
    expect(sentBody(fetchMock.mock.calls[2])).toEqual({ query: { page: 2, pageSize: 10 } })
  })

  it('sends_an_empty_body_query_when_no_query_is_given', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], hasMore: false, page: 1 }))

    await listOwnerUserOptions()

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({})
  })
})
