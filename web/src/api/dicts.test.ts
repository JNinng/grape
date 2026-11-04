import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createDictData,
  createDictType,
  deleteDictData,
  deleteDictType,
  fetchDictData,
  fetchDictType,
  listDictDatas,
  listDictTypes,
  updateDictData,
  updateDictType,
} from './dicts'

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

function typeDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '5',
    status: 1,
    name: '数据状态',
    code: 'sys_data_status',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function dataDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '9',
    status: 1,
    dictTypeId: '5',
    label: '启用',
    value: '1',
    valueType: 'integer',
    sortOrder: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('createDictType', () => {
  it('posts_the_wrapped_type_message_and_parses_the_protojson_type', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, typeDTO()))

    const typ = await createDictType({ name: '数据状态', code: 'sys_data_status' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-types/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      dictType: { name: '数据状态', code: 'sys_data_status' },
    })
    expect(typ.id).toBe('5')
    expect(typ.status).toBe(1)
  })

  it('propagates_the_conflict_message_on_a_duplicate_code', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'dict type already exists' }))

    await expect(createDictType({ name: 'x', code: 'sys_data_status' })).rejects.toThrow('dict type already exists')
  })
})

describe('fetchDictType', () => {
  it('gets_the_type_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, typeDTO()))

    const typ = await fetchDictType('5')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-types/5')
    expect(typ.code).toBe('sys_data_status')
  })

  it('propagates_the_not_found_message_for_unknown_or_cross_tenant_ids', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'dict type not found' }))

    await expect(fetchDictType('999')).rejects.toThrow('dict type not found')
  })
})

describe('listDictTypes', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listDictTypes({ where: [{ field: 'code', op: 'eq', value: 'sys_data_status' }] })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-types/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'code', op: 'eq', value: 'sys_data_status' }],
    })
    expect(set.items).toEqual([])
  })

  it('passes_the_query_through_and_returns_items_when_present', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [typeDTO()], hasMore: true, nextCursor: 'cur', page: 2, total: '3' }))

    const set = await listDictTypes({ pagination: { page: 2 } })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ pagination: { page: 2 } })
    expect(set.items).toHaveLength(1)
    expect(set.total).toBe('3')
    expect(set.nextCursor).toBe('cur')
  })
})

describe('updateDictType', () => {
  it('puts_the_type_body_with_only_mutable_fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, typeDTO({ name: '新名' })))

    await updateDictType({ id: '5', status: 0, name: '新名', remark: 'r' })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-types/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', status: 0, name: '新名', remark: 'r' })
  })

  it('omits_remark_when_empty_after_trim_by_caller', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, typeDTO()))

    await updateDictType({ id: '5', status: 1, name: 'n' })

    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '5', status: 1, name: 'n' })
  })
})

describe('deleteDictType', () => {
  it('deletes_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteDictType('5')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-types/5')
  })

  it('propagates_the_has_data_rejection', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'dict type has active data' }))

    await expect(deleteDictType('1')).rejects.toThrow('dict type has active data')
  })
})

describe('createDictData', () => {
  it('posts_the_wrapped_data_message_with_host_and_value_semantics', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, dataDTO()))

    const data = await createDictData({ dictTypeId: '5', label: '启用', value: '1', valueType: 'integer', sortOrder: 2 })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('POST')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-data/create')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      dictData: { dictTypeId: '5', label: '启用', value: '1', valueType: 'integer', sortOrder: 2 },
    })
    expect(data.id).toBe('9')
  })

  it('propagates_the_host_unknown_rejection', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid dict data argument' }))

    await expect(createDictData({ dictTypeId: '999', label: 'l', value: 'v', valueType: 'string' })).rejects.toThrow('invalid dict data argument')
  })

  it('propagates_the_duplicate_value_conflict', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, { message: 'dict data already exists' }))

    await expect(createDictData({ dictTypeId: '5', label: 'l', value: '1', valueType: 'string' })).rejects.toThrow('dict data already exists')
  })
})

describe('fetchDictData', () => {
  it('gets_the_data_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, dataDTO()))

    const data = await fetchDictData('9')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-data/9')
    expect(data.value).toBe('1')
    expect(data.valueType).toBe('integer')
  })
})

describe('fetchDictData error paths', () => {
  it('propagates_the_not_found_message_for_unknown_or_cross_tenant_ids', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'dict data not found' }))

    await expect(fetchDictData('999')).rejects.toThrow('dict data not found')
  })
})

describe('updateDictData error paths', () => {
  it('propagates_the_backend_rejection_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(400, { message: 'invalid dict data argument' }))

    await expect(updateDictData({ id: '9', status: 1, label: 'l', sortOrder: 0 })).rejects.toThrow('invalid dict data argument')
  })
})

describe('deleteDictData error paths', () => {
  it('propagates_the_not_found_message', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'dict data not found' }))

    await expect(deleteDictData('999')).rejects.toThrow('dict data not found')
  })
})

describe('listDictDatas', () => {
  it('posts_the_query_body_and_fills_missing_items_with_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { hasMore: false, nextCursor: '', page: 1 }))

    const set = await listDictDatas({ where: [{ field: 'dictTypeId', op: 'eq', value: '5' }] })

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-data/list')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({
      where: [{ field: 'dictTypeId', op: 'eq', value: '5' }],
    })
    expect(set.items).toEqual([])
  })
})

describe('updateDictData', () => {
  it('puts_the_data_body_with_only_mutable_fields', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, dataDTO({ label: '新标签', sortOrder: 3 })))

    await updateDictData({ id: '9', status: 1, label: '新标签', sortOrder: 3 })

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('PUT')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-data/update')
    expect(sentBody(fetchMock.mock.calls[0])).toEqual({ id: '9', status: 1, label: '新标签', sortOrder: 3 })
  })
})

describe('deleteDictData', () => {
  it('deletes_by_id_path', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))

    await deleteDictData('9')

    expect(requestInit(fetchMock.mock.calls[0]).method).toBe('DELETE')
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/dict-data/9')
  })
})
