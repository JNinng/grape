import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { createMenu, deleteMenu, fetchMenu, fetchMenuTree, moveMenu, updateMenu } from './menus'

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

function menuReply(): Record<string, unknown> {
  return {
    id: '214',
    status: 1,
    name: '菜单管理',
    permissionCode: 'sys:menu',
    menuType: 2,
    sortOrder: 3,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
  }
}

describe('fetchMenuTree', () => {
  it('gets_the_tree_endpoint_and_parses_the_nested_nodes', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        roots: [
          {
            menu: menuReply(),
            children: [{ menu: { ...menuReply(), id: '215', permissionCode: 'sys:menu:query', menuType: 3, parentId: '214', sortOrder: 1 }, children: [] }],
          },
        ],
        total: '2',
      }),
    )

    const tree = await fetchMenuTree()

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/tree')
    expect(tree.total).toBe('2')
    expect(tree.roots[0].menu.id).toBe('214')
    expect(tree.roots[0].children[0].menu.parentId).toBe('214')
  })

  it('normalizes_leaf_nodes_without_the_children_key_to_empty_arrays', async () => {
    // 真实 protojson 形状：叶子节点的 children 未填充即省略该键（而非空
    // 数组），客户端归一化后必须可安全遍历。
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        roots: [
          {
            menu: menuReply(),
            children: [
              { menu: { ...menuReply(), id: '215', permissionCode: 'sys:menu:query', menuType: 3, parentId: '214', sortOrder: 1 } },
            ],
          },
        ],
        total: '2',
      }),
    )

    const tree = await fetchMenuTree()

    expect(tree.roots[0].children).toHaveLength(1)
    expect(tree.roots[0].children[0].children).toEqual([])
  })

  it('defaults_roots_to_an_empty_array_when_the_empty_tree_omits_the_key', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { total: '0' }))

    const tree = await fetchMenuTree()

    expect(tree.roots).toEqual([])
    expect(tree.total).toBe('0')
  })
})

describe('createMenu', () => {
  it('posts_the_whole_request_message_with_the_menu_payload', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    const menu = await createMenu({
      name: '菜单管理',
      permissionCode: 'sys:menu',
      menuType: 2,
      sortOrder: 3,
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      menu: { name: '菜单管理', permissionCode: 'sys:menu', menuType: 2, sortOrder: 3 },
    })
    expect(menu.id).toBe('214')
  })

  it('keeps_an_explicit_parent_and_omits_unset_optional_fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    await createMenu({
      name: '菜单查询',
      permissionCode: 'sys:menu:query',
      parentId: '214',
      menuType: 3,
      sortOrder: 1,
      icon: undefined,
      remark: '按钮',
    })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      menu: {
        name: '菜单查询',
        permissionCode: 'sys:menu:query',
        parentId: '214',
        menuType: 3,
        sortOrder: 1,
        remark: '按钮',
      },
    })
  })

  it('propagates_the_backend_error_message_on_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'menu already exists' }))

    await expect(
      createMenu({ name: '菜单管理', permissionCode: 'sys:menu', menuType: 2, sortOrder: 0 }),
    ).rejects.toThrow('menu already exists')
  })
})

describe('updateMenu', () => {
  it('puts_the_menu_message_with_mutable_fields_only', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    await updateMenu({
      id: '214',
      status: 0,
      name: '菜单管理',
      sortOrder: 5,
      permissionExpr: undefined,
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/update')
    // permissionCode / menuType / parentId 不在更新载荷中（服务端忽略，前端不上送）。
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '214', status: 0, name: '菜单管理', sortOrder: 5 })
  })
})

describe('moveMenu', () => {
  it('puts_the_move_request_with_the_new_parent', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    await moveMenu('215', '201')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/move')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '215', parentId: '201' })
  })

  it('omits_the_parent_key_to_move_to_the_root', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    await moveMenu('215', undefined)

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '215' })
  })
})

describe('deleteMenu', () => {
  it('deletes_the_menu_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteMenu('215')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/215')
  })

  it('propagates_the_has_children_error_on_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'menu has active children' }))

    await expect(deleteMenu('214')).rejects.toThrow('menu has active children')
  })
})

describe('fetchMenu', () => {
  it('gets_the_menu_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, menuReply()))

    const menu = await fetchMenu('214')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/menus/214')
    expect(menu.permissionCode).toBe('sys:menu')
  })
})
