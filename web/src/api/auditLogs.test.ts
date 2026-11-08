import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { fetchLoginLog, fetchOperationLog, listLoginLogs, listOperationLogs } from './auditLogs'

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

function loginLogDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '3',
    userId: '3',
    username: 'lisi',
    loginType: 'password',
    isSuccess: false,
    failureReason: '密码错误',
    loginAt: '2026-06-30T09:00:00Z',
    createdBy: '3',
    createdAt: '2026-06-30T09:00:00Z',
    ...overrides,
  }
}

function operationLogDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '2',
    userId: '1',
    username: 'admin',
    operationType: 'UPDATE',
    targetTable: 'sys_role',
    targetId: '103',
    oldValue: { data_scope: 1 },
    newValue: { data_scope: 1, remark: '默认角色' },
    durationMs: 30,
    resultCode: '200',
    requestId: 'req-002',
    operatedAt: '2026-06-01T10:10:00Z',
    createdBy: '1',
    createdAt: '2026-06-01T10:10:00Z',
    ...overrides,
  }
}

describe('listLoginLogs', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listLoginLogs({ where: [{ field: 'username', op: 'eq', value: 'lisi' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/audit/login-logs/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'username', op: 'eq', value: 'lisi' }],
    })
    expect(set.items).toEqual([])
  })

  it('returns_items_and_envelope_when_present', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [loginLogDTO()], hasMore: true, nextCursor: 'cur', page: 1, total: '4' }))

    const set = await listLoginLogs()

    expect(set.items).toHaveLength(1)
    expect(set.items[0].isSuccess).toBe(false)
    expect(set.items[0].failureReason).toBe('密码错误')
    expect(set.total).toBe('4')
    expect(set.nextCursor).toBe('cur')
  })

  it('propagates_the_backend_rejection_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid audit log argument' }))

    await expect(listLoginLogs()).rejects.toThrow('invalid audit log argument')
  })
})

describe('fetchLoginLog', () => {
  it('gets_the_log_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, loginLogDTO()))

    const row = await fetchLoginLog('3')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/audit/login-logs/3')
    expect(row.username).toBe('lisi')
  })

  it('propagates_the_not_found_message_for_unknown_or_cross_tenant_ids', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'audit log not found' }))

    await expect(fetchLoginLog('999')).rejects.toThrow('audit log not found')
  })
})

describe('listOperationLogs', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 2 }))

    const set = await listOperationLogs({ pagination: { page: 2 } })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/audit/operation-logs/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ pagination: { page: 2 } })
    expect(set.items).toEqual([])
  })

  it('keeps_the_json_snapshots_as_objects', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [operationLogDTO()], hasMore: false, nextCursor: '', page: 1 }))

    const set = await listOperationLogs()

    expect(set.items[0].oldValue).toEqual({ data_scope: 1 })
    expect(set.items[0].newValue).toEqual({ data_scope: 1, remark: '默认角色' })
  })
})

describe('fetchOperationLog', () => {
  it('gets_the_log_by_id_path_with_snapshots', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, operationLogDTO()))

    const row = await fetchOperationLog('2')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/audit/operation-logs/2')
    expect(row.operationType).toBe('UPDATE')
    expect(row.targetTable).toBe('sys_role')
    expect(row.durationMs).toBe(30)
  })

  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'audit log not found' }))

    await expect(fetchOperationLog('999')).rejects.toThrow('audit log not found')
  })
})
