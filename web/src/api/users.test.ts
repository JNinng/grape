import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  assignRoles,
  createUser,
  deleteUser,
  fetchUser,
  listUsers,
  setUserPassword,
  transferUserOwner,
  updateUser,
} from './users'

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

// requestInit 便于从 fetch 调用参数还原 method / path / body 的断言。
function requestInit(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function sentBody(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = requestInit(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

describe('createUser', () => {
  it('posts_the_whole_request_message_and_parses_the_protojson_user', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        id: '11',
        status: 1,
        name: 'bob',
        nickname: 'Bob',
        locale: 'zh-CN',
        createdBy: '7',
        updatedBy: '7',
        createdAt: '2026-01-01T00:00:00Z',
      }),
    )

    const user = await createUser({ name: 'bob', nickname: 'Bob' }, 'secret-1')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      user: { name: 'bob', nickname: 'Bob' },
      password: 'secret-1',
    })
    expect(user.id).toBe('11')
    expect(user.status).toBe(1)
  })

  it('omits_unset_optional_fields_from_the_nested_user', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1' }))

    await createUser(
      { name: 'bob', nickname: 'Bob', email: 'b@x.io', remark: undefined, locale: 'zh-CN' },
      'secret-1',
    )

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      user: { name: 'bob', nickname: 'Bob', email: 'b@x.io', locale: 'zh-CN' },
      password: 'secret-1',
    })
  })

  it('passes_the_selected_ownership_triple_to_the_nested_user', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1' }))

    await createUser(
      { name: 'bob', nickname: 'Bob', ownerUserId: '7', ownerDeptId: '3', ownerUserGroupId: '5' },
      'secret-1',
    )

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      user: { name: 'bob', nickname: 'Bob', ownerUserId: '7', ownerDeptId: '3', ownerUserGroupId: '5' },
      password: 'secret-1',
    })
  })

  it('propagates_the_backend_error_message_on_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'name already exists' }))

    await expect(createUser({ name: 'bob', nickname: 'Bob' }, 'p')).rejects.toThrow(
      'name already exists',
    )
  })
})

describe('fetchUser', () => {
  it('gets_the_user_by_id_path', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        id: '42',
        name: 'carol',
        nickname: 'Carol',
        locale: 'zh-CN',
        ownerDeptId: '3',
      }),
    )

    const user = await fetchUser('42')

    // GET 经 authFetch(path) 直发：不携带 init，方法由 fetch 默认。
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/42')
    expect(requestInit(fetchMock.mock.calls[0]).method).toBeUndefined()
    expect(user.ownerDeptId).toBe('3')
  })

  it('surfaces_the_not_found_message_when_the_user_is_invisible', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user not found' }))

    await expect(fetchUser('42')).rejects.toThrow('user not found')
  })
})

describe('listUsers', () => {
  it('posts_the_dynamic_query_message_as_the_body', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [{ id: '1', name: 'alice', nickname: 'Alice', locale: 'zh-CN' }],
        total: '2',
        hasMore: true,
        nextCursor: '',
        page: 1,
      }),
    )

    const page = await listUsers({
      where: [{ field: 'name', op: 'contains', value: 'al' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'name', op: 'contains', value: 'al' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(page.total).toBe('2')
    expect(page.hasMore).toBe(true)
    expect(page.items[0]?.name).toBe('alice')
  })

  it('sends_an_empty_object_when_no_query_is_given', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], hasMore: false, nextCursor: '', page: 1 }))

    await listUsers()

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({})
  })

  it('defaults_items_to_an_empty_array_when_the_set_omits_the_key', async () => {
    // 筛选零命中时 protojson 省略 items 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const page = await listUsers()

    expect(page.items).toEqual([])
  })

  it('propagates_the_invalid_query_reason', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'unknown field badField' }))

    await expect(listUsers({ where: [{ field: 'badField', op: 'eq', value: 1 }] })).rejects.toThrow(
      'unknown field badField',
    )
  })
})

describe('updateUser', () => {
  it('puts_the_user_message_with_required_status', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1', status: 0, name: 'bob', nickname: 'Bob', locale: 'zh-CN' }))

    const user = await updateUser({ id: '1', status: 0, name: 'bob', nickname: 'Bob' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '1',
      status: 0,
      name: 'bob',
      nickname: 'Bob',
    })
    expect(user.status).toBe(0)
  })

  it('propagates_the_backend_error_on_invalid_status', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid status' }))

    await expect(updateUser({ id: '1', status: 9, name: 'bob', nickname: 'Bob' })).rejects.toThrow(
      'invalid status',
    )
  })
})

describe('setUserPassword', () => {
  it('posts_the_id_and_password_and_ignores_the_empty_reply', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await expect(setUserPassword('5', 'new-pass')).resolves.toBeUndefined()

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/password')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', password: 'new-pass' })
  })

  it('propagates_the_backend_error_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'password too weak' }))

    await expect(setUserPassword('5', 'weak')).rejects.toThrow('password too weak')
  })
})

describe('deleteUser', () => {
  it('deletes_by_id_path_without_a_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await expect(deleteUser('9')).resolves.toBeUndefined()

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/9')
    expect(requestInit(fetchMock.mock.calls[0]).body).toBeUndefined()
  })

  it('propagates_the_backend_error_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user not found' }))

    await expect(deleteUser('9')).rejects.toThrow('user not found')
  })
})

describe('assignRoles', () => {
  it('posts_stringified_role_ids_and_returns_the_added_count', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { added: '2' }))

    const reply = await assignRoles('1', ['3', '5'])

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/assign-roles')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ userId: '1', roleIds: ['3', '5'] })
    expect(reply.added).toBe('2')
  })

  it('propagates_the_forbidden_message_when_a_role_is_not_assignable', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { message: 'role not assignable' }))

    await expect(assignRoles('1', ['3'])).rejects.toThrow('role not assignable')
  })
})

describe('transferUserOwner', () => {
  it('posts_the_full_triple_with_present_ids', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1', ownerDeptId: '3', locale: 'zh-CN' }))

    const user = await transferUserOwner('1', {
      ownerUserId: '8',
      ownerDeptId: '3',
      ownerUserGroupId: undefined,
    })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/users/transfer-owner')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '1',
      ownerUserId: '8',
      ownerDeptId: '3',
    })
    expect(user.ownerDeptId).toBe('3')
  })

  it('keeps_explicit_nulls_so_unset_fields_clear_the_ownership', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1', locale: 'zh-CN' }))

    await transferUserOwner('1', { ownerUserId: null })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '1', ownerUserId: null })
  })

  it('propagates_the_forbidden_message_when_the_new_owner_is_unreachable', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { message: 'owner not reachable' }))

    await expect(transferUserOwner('1', { ownerUserId: '8' })).rejects.toThrow(
      'owner not reachable',
    )
  })
})

describe('error normalization', () => {
  it('falls_back_to_the_status_text_for_non_json_error_bodies', async () => {
    fetchMock.mockResolvedValue(new Response('oops', { status: 502 }))

    await expect(fetchUser('1')).rejects.toThrow('HTTP 502')
  })
})
