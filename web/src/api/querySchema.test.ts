import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { getListSchema } from './querySchema'

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

describe('getListSchema', () => {
  it('requests_the_schema_with_the_encoded_resource_and_parses_the_protojson_payload', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        resource: 'sys_user',
        hash: '0655207dc09f5a3f79ce8f53c2ff1b76',
        fields: [
          {
            field: 'createdAt',
            label: '创建时间',
            dataType: 'datetime',
            sortable: true,
            visibility: 'optional',
            ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'],
          },
          { field: 'lastLoginAt', label: '最后登录时间', dataType: 'datetime', nullable: true, ops: ['gt', 'gte', 'lt', 'lte', 'between', 'is_null', 'not_null'] },
        ],
        defaultSort: [{ field: 'createdAt', desc: true }],
        pagination: { defaultPageSize: 20, maxPageSize: 400, modes: ['offset', 'cursor'], includeTotal: true },
      }),
    )
    const schema = await getListSchema('sys_user')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/query/schema?resource=sys_user')
    expect(schema.resource).toBe('sys_user')
    expect(schema.fields).toHaveLength(2)
    expect(schema.fields[0]?.field).toBe('createdAt')
    expect(schema.defaultSort).toEqual([{ field: 'createdAt', desc: true }])
    expect(schema.pagination?.defaultPageSize).toBe(20)
  })

  it('encodes_unsafe_resource_names_into_the_query_string', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { resource: 'a b', fields: [] }))
    await getListSchema('a b')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/query/schema?resource=a%20b')
  })

  it('normalizes_a_missing_fields_list_to_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { resource: 'sys_user', hash: 'h' }))
    const schema = await getListSchema('sys_user')

    expect(schema.fields).toEqual([])
  })

  it('surfaces_api_errors_via_toApiError', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(400, { code: 400, reason: 'QUERY_RESOURCE_NOT_FOUND', message: 'unknown resource' }),
    )
    await expect(getListSchema('nope')).rejects.toMatchObject({
      message: expect.stringContaining('unknown resource'),
    })
  })
})
