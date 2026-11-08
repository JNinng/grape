import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createRole,
  deleteRole,
  fetchRole,
  fetchRoleDataScope,
  listRoleMenus,
  listRoles,
  setRoleDataScope,
  setRoleMenus,
  updateRole,
} from './roles'

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

function roleReply(): Record<string, unknown> {
  return {
    id: '5',
    status: 1,
    name: '运营',
    code: 'operator',
    roleType: 2,
    dataScope: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
  }
}

describe('createRole', () => {
  it('posts_the_whole_request_message_and_parses_the_protojson_role', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, roleReply()))

    const role = await createRole({ name: '运营', code: 'operator' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ role: { name: '运营', code: 'operator' } })
    expect(role.id).toBe('5')
    expect(role.dataScope).toBe(1)
  })

  it('omits_the_remark_key_when_unset', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, roleReply()))

    await createRole({ name: '运营', code: 'operator', remark: undefined })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ role: { name: '运营', code: 'operator' } })
  })

  it('propagates_the_backend_error_message_on_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'role already exists' }))

    await expect(createRole({ name: '运营', code: 'operator' })).rejects.toThrow('role already exists')
  })
})

describe('fetchRole', () => {
  it('gets_the_role_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, roleReply()))

    const role = await fetchRole('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5')
    expect(role.code).toBe('operator')
  })
})

describe('listRoles', () => {
  it('posts_the_dynamic_query_as_the_request_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [roleReply()], total: '1', hasMore: false, nextCursor: '', page: 1 }))

    const set = await listRoles({
      where: [{ field: 'name', op: 'contains', value: '运营' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'name', op: 'contains', value: '运营' }],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(set.items).toHaveLength(1)
    expect(set.total).toBe('1')
  })

  it('defaults_items_to_an_empty_array_when_the_set_omits_the_key', async () => {
    // 筛选零命中时 protojson 省略 items 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listRoles()

    expect(set.items).toEqual([])
  })
})

describe('updateRole', () => {
  it('puts_the_role_message_with_an_explicit_status', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, roleReply()))

    await updateRole({ id: '5', status: 0, name: '运营二部' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', status: 0, name: '运营二部' })
  })
})

describe('deleteRole', () => {
  it('deletes_the_role_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteRole('5')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5')
  })

  it('propagates_the_preset_undeletable_error_on_403', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(403, { code: 403, reason: 'ROLE_PRESET_UNDELETABLE', message: 'preset role cannot be deleted' }),
    )

    await expect(deleteRole('1')).rejects.toThrow('preset role cannot be deleted')
  })
})

describe('fetchRoleDataScope', () => {
  it('gets_the_data_scope_config_with_string_int64_ids', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        roleId: '5',
        dataScope: 5,
        scopeDeptId: '3',
        idScopes: [
          { type: 2, dataId: '3' },
          { type: 2, dataId: '9' },
        ],
        allowedDeptIds: ['3', '8'],
      }),
    )

    const cfg = await fetchRoleDataScope('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5/data-scope')
    expect(cfg.roleId).toBe('5')
    expect(cfg.dataScope).toBe(5)
    expect(cfg.scopeDeptId).toBe('3')
    expect(cfg.idScopes).toEqual([
      { type: 2, dataId: '3' },
      { type: 2, dataId: '9' },
    ])
    expect(cfg.allowedDeptIds).toEqual(['3', '8'])
  })

  it('defaults_id_scopes_and_allowed_dept_ids_to_empty_arrays_when_the_keys_are_omitted', async () => {
    // 无明细、无分发部门：protojson 省略两个 repeated 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { roleId: '5', dataScope: 1 }))

    const cfg = await fetchRoleDataScope('5')

    expect(cfg.idScopes).toEqual([])
    expect(cfg.allowedDeptIds).toEqual([])
  })
})

describe('setRoleDataScope', () => {
  it('puts_the_whole_request_and_clears_scope_dept_by_omitting_the_key', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, { roleId: '5', dataScope: 7, idScopes: [], allowedDeptIds: [] }),
    )

    const cfg = await setRoleDataScope('5', {
      dataScope: 7,
      scopeDeptId: undefined,
      idScopes: [],
      allowedDeptIds: [],
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5/data-scope')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '5',
      dataScope: 7,
      idScopes: [],
      allowedDeptIds: [],
    })
    expect(cfg.dataScope).toBe(7)
  })

  it('defaults_id_scopes_and_allowed_dept_ids_to_empty_arrays_when_the_response_omits_the_keys', async () => {
    // 清空后回读：protojson 省略空 repeated 键，返回值同样兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, { roleId: '5', dataScope: 7 }))

    const cfg = await setRoleDataScope('5', {
      dataScope: 7,
      idScopes: [],
      allowedDeptIds: [],
    })

    expect(cfg.idScopes).toEqual([])
    expect(cfg.allowedDeptIds).toEqual([])
  })

  it('sends_id_scope_entries_and_allowed_dept_ids_verbatim', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { roleId: '5', dataScope: 4, idScopes: [], allowedDeptIds: [] }))

    await setRoleDataScope('5', {
      dataScope: 4,
      scopeDeptId: null,
      idScopes: [
        { type: 1, dataId: '11' },
        { type: 1, dataId: '12' },
      ],
      allowedDeptIds: ['3'],
    })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: '5',
      dataScope: 4,
      scopeDeptId: null,
      idScopes: [
        { type: 1, dataId: '11' },
        { type: 1, dataId: '12' },
      ],
      allowedDeptIds: ['3'],
    })
  })

  it('propagates_the_invalid_argument_error_on_unknown_references', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid role argument' }))

    await expect(
      setRoleDataScope('5', { dataScope: 5, idScopes: [{ type: 2, dataId: '404' }], allowedDeptIds: [] }),
    ).rejects.toThrow('invalid role argument')
  })
})

describe('role menu grants', () => {
  it('listRoleMenus_gets_the_ascending_granted_menu_ids', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { menuIds: ['201', '202', '210'] }))

    const set = await listRoleMenus('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5/menus')
    expect(set.menuIds).toEqual(['201', '202', '210'])
  })

  it('listRoleMenus_defaults_menuIds_to_an_empty_array_when_the_key_is_omitted', async () => {
    // 无授权角色：protojson 省略 menuIds 键，客户端兜底为空数组。
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    const set = await listRoleMenus('5')

    expect(set.menuIds).toEqual([])
  })

  it('setRoleMenus_puts_the_full_selected_set_verbatim', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { menuIds: ['201'] }))

    await setRoleMenus('5', ['201'])

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/roles/5/menus')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', menuIds: ['201'] })
  })

  it('setRoleMenus_sends_an_empty_array_to_clear_all_grants', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { menuIds: [] }))

    await setRoleMenus('5', [])

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', menuIds: [] })
  })
})
