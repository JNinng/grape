import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createDept,
  deleteDept,
  fetchDept,
  fetchDeptTree,
  listDepts,
  moveDept,
  updateDept,
} from './depts'

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

describe('fetchDeptTree', () => {
  it('gets_the_full_tree_and_parses_nested_nodes', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        roots: [
          {
            dept: { id: '1', status: 1, name: 'hq', ownerUserId: '7', createdBy: '7', updatedBy: '7', createdAt: '2026-01-01T00:00:00Z' },
            children: [
              {
                dept: { id: '2', status: 1, name: 'eng', parentId: '1', ownerUserId: '7', createdBy: '7', updatedBy: '7', createdAt: '2026-01-02T00:00:00Z' },
                children: [],
              },
            ],
          },
        ],
        total: '2',
      }),
    )

    const tree = await fetchDeptTree()

    // GET 经 authFetch(path) 直发：不携带 init，方法由 fetch 默认。
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/tree')
    expect(requestInit(fetchMock.mock.calls[0]).method).toBeUndefined()
    expect(tree.total).toBe('2')
    expect(tree.roots[0]?.dept.name).toBe('hq')
    expect(tree.roots[0]?.children[0]?.dept.parentId).toBe('1')
  })

  it('normalizes_leaf_nodes_without_the_children_key_to_empty_arrays', async () => {
    // 真实 protojson 形状：叶子节点的 children 未填充即省略该键（而非空
    // 数组），客户端归一化后必须可安全遍历。
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        roots: [
          {
            dept: { id: '1', status: 1, name: 'hq', ownerUserId: '7', createdBy: '7', updatedBy: '7', createdAt: '2026-01-01T00:00:00Z' },
            children: [
              {
                dept: { id: '2', status: 1, name: 'eng', parentId: '1', ownerUserId: '7', createdBy: '7', updatedBy: '7', createdAt: '2026-01-02T00:00:00Z' },
              },
            ],
          },
        ],
        total: '2',
      }),
    )

    const tree = await fetchDeptTree()

    expect(tree.roots[0]?.children).toHaveLength(1)
    expect(tree.roots[0]?.children[0]?.children).toEqual([])
  })

  it('defaults_roots_to_an_empty_array_when_the_empty_tree_omits_the_key', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { total: '0' }))

    const tree = await fetchDeptTree()

    expect(tree.roots).toEqual([])
    expect(tree.total).toBe('0')
  })

  it('propagates_the_backend_error_message_when_the_tree_fails', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { message: 'tree unavailable' }))

    await expect(fetchDeptTree()).rejects.toThrow('tree unavailable')
  })
})

describe('createDept', () => {
  it('posts_the_wrapped_dept_message_and_parses_the_protojson_dept', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        id: '9',
        status: 1,
        name: 'eng',
        ownerUserId: '7',
        createdBy: '7',
        updatedBy: '7',
        createdAt: '2026-01-01T00:00:00Z',
      }),
    )

    const dept = await createDept({ name: 'eng', ownerUserId: '7' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ dept: { name: 'eng', ownerUserId: '7' } })
    expect(dept.id).toBe('9')
    expect(dept.status).toBe(1)
  })

  it('sends_the_parent_id_when_creating_a_child_dept', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '10' }))

    await createDept({ name: 'web', parentId: '9', ownerUserId: '7', ownerUserGroupId: '5' })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      dept: { name: 'web', parentId: '9', ownerUserId: '7', ownerUserGroupId: '5' },
    })
  })

  it('propagates_the_conflict_message_on_a_duplicate_name', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'dept already exists' }))

    await expect(createDept({ name: 'eng', ownerUserId: '7' })).rejects.toThrow('dept already exists')
  })

  it('propagates_the_invalid_argument_message_when_the_parent_is_unknown', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid dept argument' }))

    await expect(createDept({ name: 'web', parentId: '99', ownerUserId: '7' })).rejects.toThrow(
      'invalid dept argument',
    )
  })
})

describe('fetchDept', () => {
  it('gets_the_dept_by_id_path', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        id: '42',
        status: 1,
        name: 'eng',
        parentId: '3',
        ownerUserId: '7',
        createdBy: '7',
        updatedBy: '7',
        createdAt: '2026-01-01T00:00:00Z',
      }),
    )

    const dept = await fetchDept('42')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/42')
    expect(dept.parentId).toBe('3')
  })

  it('surfaces_the_not_found_message_when_the_dept_is_invisible', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'dept not found' }))

    await expect(fetchDept('42')).rejects.toThrow('dept not found')
  })
})

describe('listDepts', () => {
  it('posts_the_dynamic_query_message_as_the_body', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        items: [{ id: '1', status: 1, name: 'hq', ownerUserId: '7' }],
        total: '2',
        hasMore: true,
        nextCursor: '',
        page: 1,
      }),
    )

    const page = await listDepts({
      where: [{ field: 'name', op: 'contains', value: 'hq' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'name', op: 'contains', value: 'hq' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(page.total).toBe('2')
    expect(page.items[0]?.name).toBe('hq')
  })

  it('sends_an_empty_object_when_no_query_is_given', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], hasMore: false, nextCursor: '', page: 1 }))

    await listDepts()

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({})
  })

  it('defaults_items_to_an_empty_array_when_the_set_omits_the_key', async () => {
    // 筛选零命中时 protojson 省略 items 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const page = await listDepts()

    expect(page.items).toEqual([])
  })

  it('propagates_the_invalid_query_reason', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'unknown field badField' }))

    await expect(listDepts({ where: [{ field: 'badField', op: 'eq', value: 1 }] })).rejects.toThrow(
      'unknown field badField',
    )
  })
})

describe('updateDept', () => {
  it('puts_the_full_mutable_set_with_required_status', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1', status: 0, name: 'eng' }))

    const dept = await updateDept({
      id: '1',
      status: 0,
      name: 'eng',
      parentId: '2',
      ownerUserId: '7',
      ownerUserGroupId: '3',
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '1',
      status: 0,
      name: 'eng',
      parentId: '2',
      ownerUserId: '7',
      ownerUserGroupId: '3',
    })
    expect(dept.status).toBe(0)
  })

  it('omits_parent_and_group_to_move_to_root_and_clear_the_group', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '1' }))

    await updateDept({ id: '1', status: 1, name: 'eng', ownerUserId: '7' })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '1',
      status: 1,
      name: 'eng',
      ownerUserId: '7',
    })
  })

  it('propagates_the_backend_error_on_invalid_argument', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid dept argument' }))

    await expect(
      updateDept({ id: '1', status: 0, name: '', ownerUserId: '7' }),
    ).rejects.toThrow('invalid dept argument')
  })
})

describe('moveDept', () => {
  it('posts_the_id_only_when_moving_to_the_root', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '5', status: 1, name: 'eng' }))

    const dept = await moveDept('5')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/move')
    // parentId 未设置（键省略）即移为根部门。
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5' })
    expect(dept.id).toBe('5')
  })

  it('posts_the_new_parent_id_when_moving_under_a_parent', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: '5', parentId: '3' }))

    const dept = await moveDept('5', '3')

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', parentId: '3' })
    expect(dept.parentId).toBe('3')
  })

  it('propagates_the_invalid_argument_message_on_a_cycle', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid dept argument' }))

    await expect(moveDept('5', '6')).rejects.toThrow('invalid dept argument')
  })
})

describe('deleteDept', () => {
  it('deletes_by_id_path_without_a_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await expect(deleteDept('9')).resolves.toBeUndefined()

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/depts/9')
    expect(requestInit(fetchMock.mock.calls[0]).body).toBeUndefined()
  })

  it('propagates_the_has_children_conflict_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'dept has active children' }))

    await expect(deleteDept('9')).rejects.toThrow('dept has active children')
  })
})

describe('error normalization', () => {
  it('falls_back_to_the_status_text_for_non_json_error_bodies', async () => {
    fetchMock.mockResolvedValue(new Response('oops', { status: 502 }))

    await expect(fetchDeptTree()).rejects.toThrow('HTTP 502')
  })
})
