import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createTenant,
  deleteTenant,
  disableTenant,
  fetchTenant,
  listTenants,
  restoreTenant,
  updateTenant,
} from './tenants'

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
    user: { id: '7', tenantId: 't-parent', name: 'alice', nickname: 'Alice' },
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

function sentMethod(call: readonly [unknown, RequestInit?]): string {
  return (requestInit(call).method ?? 'GET').toUpperCase()
}

function tenantDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 't-0123456789abcd',
    status: 1,
    code: 'demo',
    name: '演示租户',
    parentId: 't-parent',
    contactName: '张三',
    contactPhone: '13800000000',
    expiredAt: '2026-12-31T00:00:00Z',
    maxUserCount: 100,
    defaultLocale: 'zh-CN',
    tenantId: 't-parent',
    createdBy: '9007199254740993',
    updatedBy: '9007199254740993',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('createTenant', () => {
  it('posts_the_wrapped_tenant_message_and_preserves_int64_strings', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantDTO()))

    const created = await createTenant({
      status: 1,
      code: 'demo',
      name: '演示租户',
      contactName: '张三',
      expiredAt: '2026-12-31T00:00:00Z',
      maxUserCount: 100,
      defaultLocale: 'zh-CN',
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      tenant: {
        status: 1,
        code: 'demo',
        name: '演示租户',
        contactName: '张三',
        expiredAt: '2026-12-31T00:00:00Z',
        maxUserCount: 100,
        defaultLocale: 'zh-CN',
      },
    })
    expect(created.id).toBe('t-0123456789abcd')
    expect(created.createdBy).toBe('9007199254740993')
    expect(created.updatedBy).toBe('9007199254740993')
  })

  it('propagates_the_duplicate_tenant_conflict', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, { reason: 'TENANT_ALREADY_EXISTS', message: 'tenant already exists' }),
    )

    await expect(createTenant({ code: 'demo', name: '演示租户' })).rejects.toMatchObject({
      message: 'tenant already exists',
      reason: 'TENANT_ALREADY_EXISTS',
    })
  })
})

describe('fetchTenant', () => {
  it('gets_the_tenant_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantDTO()))

    const tenant = await fetchTenant('t-0123456789abcd')

    expect(sentMethod(fetchMock.mock.calls[0])).toBe('GET')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/t-0123456789abcd')
    expect(tenant.code).toBe('demo')
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TENANT_NOT_FOUND', message: 'tenant not found' }),
    )

    await expect(fetchTenant('t-missing')).rejects.toThrow('tenant not found')
  })
})

describe('listTenants', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listTenants({ where: [{ field: 'code', op: 'prefix', value: 'demo' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'code', op: 'prefix', value: 'demo' }],
    })
    expect(set.items).toEqual([])
  })

  it('returns_the_page_envelope_when_items_are_present', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, { items: [tenantDTO()], hasMore: true, nextCursor: 'cur', page: 2, total: '3' }),
    )

    const set = await listTenants({ pagination: { page: 2 } })

    expect(set.items).toHaveLength(1)
    expect(set.total).toBe('3')
    expect(set.hasMore).toBe(true)
    expect(set.nextCursor).toBe('cur')
    expect(set.page).toBe(2)
  })
})

describe('updateTenant', () => {
  it('puts_the_tenant_message_as_the_request_body_without_identity_columns', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantDTO({ name: '演示租户改' })))

    await updateTenant({
      id: 't-0123456789abcd',
      status: 1,
      name: '演示租户改',
      contactName: '张三',
      expiredAt: '2026-12-31T00:00:00Z',
      maxUserCount: 100,
      defaultLocale: 'zh-CN',
    })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/update')
    // body: "tenant" 的 HTTP 规则把 Tenant 消息本身作为请求体，不是外层包装。
    // code/parentId/tenantId 是身份与结构列，前端不上送。
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      id: 't-0123456789abcd',
      status: 1,
      name: '演示租户改',
      contactName: '张三',
      expiredAt: '2026-12-31T00:00:00Z',
      maxUserCount: 100,
      defaultLocale: 'zh-CN',
    })
  })

  it('propagates_the_invalid_argument_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { reason: 'TENANT_INVALID_ARGUMENT', message: 'invalid tenant argument' }),
    )

    await expect(updateTenant({ id: 't-1', status: 1, name: '' })).rejects.toThrow(
      'invalid tenant argument',
    )
  })
})

describe('deleteTenant', () => {
  it('deletes_the_tenant_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteTenant('t-0123456789abcd')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/t-0123456789abcd')
  })

  it('propagates_the_has_children_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, { reason: 'TENANT_HAS_CHILDREN', message: 'tenant has active children' }),
    )

    await expect(deleteTenant('t-0123456789abcd')).rejects.toMatchObject({
      message: 'tenant has active children',
      reason: 'TENANT_HAS_CHILDREN',
    })
  })

  it('propagates_the_not_found_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TENANT_NOT_FOUND', message: 'tenant not found' }),
    )

    await expect(deleteTenant('t-missing')).rejects.toThrow('tenant not found')
  })
})

describe('disableTenant', () => {
  it('posts_to_the_disable_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantDTO({ status: 0 })))

    const tenant = await disableTenant('t-0123456789abcd')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/t-0123456789abcd/disable')
    expect(tenant.status).toBe(0)
  })

  it('propagates_the_self_tenant_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, {
        reason: 'TENANT_INVALID_ARGUMENT',
        message: 'invalid tenant argument',
      }),
    )

    await expect(disableTenant('t-parent')).rejects.toThrow('invalid tenant argument')
  })
})

describe('restoreTenant', () => {
  it('posts_to_the_restore_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, tenantDTO({ status: 1 })))

    const tenant = await restoreTenant('t-0123456789abcd')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/tenants/t-0123456789abcd/restore')
    expect(tenant.status).toBe(1)
  })

  it('propagates_the_not_found_rejection', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(404, { reason: 'TENANT_NOT_FOUND', message: 'tenant not found' }),
    )

    await expect(restoreTenant('t-missing')).rejects.toThrow('tenant not found')
  })
})
