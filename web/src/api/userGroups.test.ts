import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  addMembers,
  createUserGroup,
  deleteUserGroup,
  fetchUserGroup,
  listUserGroupMembers,
  listUserGroups,
  removeMembers,
  updateUserGroup,
} from './userGroups'

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

function groupDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '5',
    status: 1,
    name: 'ops',
    code: 'OPS',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('createUserGroup', () => {
  it('posts_the_wrapped_group_message_and_parses_the_protojson_group', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, groupDTO()))

    const group = await createUserGroup({ name: 'ops', code: 'OPS' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ group: { name: 'ops', code: 'OPS' } })
    expect(group.id).toBe('5')
    expect(group.status).toBe(1)
  })

  it('sends_the_optional_fields_when_provided', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, groupDTO({ remark: 'on-call', ownerUserId: '7' })))

    await createUserGroup({ name: 'ops', code: 'OPS', remark: 'on-call', ownerUserId: '7', status: 0 })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      group: { name: 'ops', code: 'OPS', remark: 'on-call', ownerUserId: '7', status: 0 },
    })
  })

  it('propagates_the_conflict_message_on_a_duplicate_code_or_name', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'user group already exists' }))

    await expect(createUserGroup({ name: 'ops', code: 'OPS' })).rejects.toThrow('user group already exists')
  })
})

describe('fetchUserGroup', () => {
  it('gets_the_group_by_id_path_and_keeps_optional_fields_omitted', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, groupDTO({ remark: 'ops team', ownerUserId: '9', updatedAt: '2026-01-02T00:00:00Z' })))

    const group = await fetchUserGroup('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/5')
    expect(requestInit(fetchMock.mock.calls[0]).method).toBeUndefined()
    expect(group.remark).toBe('ops team')
    expect(group.ownerUserId).toBe('9')
  })

  it('surfaces_the_not_found_message_when_the_group_is_invisible', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user group not found' }))

    await expect(fetchUserGroup('5')).rejects.toThrow('user group not found')
  })
})

describe('listUserGroups', () => {
  it('posts_the_dynamic_query_message_as_the_body', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [groupDTO()],
        total: '3',
        hasMore: true,
        nextCursor: '',
        page: 1,
      }),
    )

    const page = await listUserGroups({
      where: [{ field: 'name', op: 'contains', value: 'ops' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'name', op: 'contains', value: 'ops' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(page.total).toBe('3')
    expect(page.items[0]?.code).toBe('OPS')
  })

  it('sends_an_empty_object_when_no_query_is_given', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], hasMore: false, nextCursor: '', page: 1 }))

    await listUserGroups()

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({})
  })

  it('defaults_items_to_an_empty_array_when_the_set_omits_the_key', async () => {
    // 筛选零命中时 protojson 省略 items 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const page = await listUserGroups()

    expect(page.items).toEqual([])
  })

  it('propagates_the_invalid_query_reason', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'unknown field badField' }))

    await expect(listUserGroups({ where: [{ field: 'badField', op: 'eq', value: 1 }] })).rejects.toThrow(
      'unknown field badField',
    )
  })
})

describe('updateUserGroup', () => {
  it('puts_the_group_message_with_required_status_and_no_identity_fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, groupDTO({ name: 'operations', status: 0 })))

    const group = await updateUserGroup({ id: '5', status: 0, name: 'operations' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/update')
    // 更新面只送 id/name/status/remark：code 与 ownerUserId 由后端忽略，
    // 前端不上送。
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', status: 0, name: 'operations' })
    expect(group.status).toBe(0)
  })

  it('propagates_the_backend_error_on_invalid_argument', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid user group argument' }))

    await expect(updateUserGroup({ id: '5', status: 1, name: '' })).rejects.toThrow('invalid user group argument')
  })
})

describe('deleteUserGroup', () => {
  it('deletes_by_id_path_without_a_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await expect(deleteUserGroup('5')).resolves.toBeUndefined()

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/5')
    expect(requestInit(fetchMock.mock.calls[0]).body).toBeUndefined()
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user group not found' }))

    await expect(deleteUserGroup('5')).rejects.toThrow('user group not found')
  })
})

describe('listUserGroupMembers', () => {
  it('gets_the_members_with_page_and_page_size_query_params', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [
          { userId: '11', name: 'alice', nickname: 'Alice', status: 1, addedBy: '7', addedAt: '2026-01-03T00:00:00Z' },
        ],
        total: '1',
        hasMore: false,
      }),
    )

    const set = await listUserGroupMembers('5', 2, 50)

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/5/members?page=2&page_size=50')
    const member = set.items[0]
    expect(member?.userId).toBe('11')
    expect(member?.nickname).toBe('Alice')
    expect(set.total).toBe('1')
    expect(set.hasMore).toBe(false)
  })

  it('uses_the_default_pagination_when_no_arguments_are_given', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: '0', hasMore: false }))

    await listUserGroupMembers('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/5/members?page=1&page_size=20')
  })

  it('defaults_items_to_an_empty_array_when_an_empty_group_omits_the_key', async () => {
    // 空组没有成员：protojson 省略 items 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { total: '0', hasMore: false }))

    const set = await listUserGroupMembers('5')

    expect(set.items).toEqual([])
    expect(set.total).toBe('0')
    expect(set.hasMore).toBe(false)
  })

  it('propagates_the_not_found_message_when_the_group_is_unknown', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user group not found' }))

    await expect(listUserGroupMembers('5')).rejects.toThrow('user group not found')
  })
})

describe('addMembers', () => {
  it('posts_the_id_and_user_ids_and_parses_the_added_count', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { added: '2' }))

    const reply = await addMembers('5', ['11', '12'])

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/add-members')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', userIds: ['11', '12'] })
    expect(reply.added).toBe('2')
  })

  it('propagates_the_whole_batch_rejection_when_any_user_is_unknown', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid user group argument' }))

    await expect(addMembers('5', ['11', '999'])).rejects.toThrow('invalid user group argument')
  })
})

describe('removeMembers', () => {
  it('posts_the_id_and_user_ids_and_parses_the_removed_count', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { removed: '1' }))

    const reply = await removeMembers('5', ['11'])

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/user-groups/remove-members')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', userIds: ['11'] })
    expect(reply.removed).toBe('1')
  })

  it('propagates_the_not_found_message_when_the_group_is_unknown', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'user group not found' }))

    await expect(removeMembers('5', ['11'])).rejects.toThrow('user group not found')
  })
})

describe('error normalization', () => {
  it('falls_back_to_the_status_text_for_non_json_error_bodies', async () => {
    fetchMock.mockResolvedValue(new Response('oops', { status: 502 }))

    await expect(fetchUserGroup('5')).rejects.toThrow('HTTP 502')
  })
})
