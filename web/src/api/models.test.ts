import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import {
  createModel,
  createModelSuper,
  deleteModel,
  deleteModelSuper,
  listModels,
  updateModel,
  updateModelSuper,
} from './models'

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

function init(call: readonly [unknown, RequestInit?]): RequestInit {
  return call[1] ?? {}
}

function body(call: readonly [unknown, RequestInit?]): Record<string, unknown> {
  const raw = init(call).body
  expect(typeof raw).toBe('string')
  return JSON.parse(raw as string) as Record<string, unknown>
}

function modelDTO(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '300',
    platform: false,
    status: 1,
    kind: 'text',
    name: 'Qwen3 8B',
    modelKey: 'qwen3:8b',
    provider: 'openai',
    model: 'qwen3:8b',
    hasApiKey: false,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('model api', () => {
  it('lists_models_with_the_kind_query_and_normalizes_items', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    const set = await listModels('text')
    expect(set.items).toEqual([])
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models?kind=text')
  })

  it('creates_a_tenant_model_with_the_api_key', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, modelDTO()))
    await createModel(
      { status: 1, kind: 'text', name: 'Qwen3 8B', modelKey: 'qwen3:8b', model: 'qwen3:8b' },
      'secret',
    )
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/create')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({
      model: { status: 1, kind: 'text', name: 'Qwen3 8B', modelKey: 'qwen3:8b', model: 'qwen3:8b' },
      apiKey: 'secret',
    })
  })

  it('updates_a_tenant_model_with_clear_api_key', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, modelDTO()))
    await updateModel({ id: '300', model: { status: 0, name: 'Qwen3 8B', model: 'qwen3:8b' }, clearApiKey: true })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/update')
    expect(init(call).method).toBe('PUT')
    expect(body(call)).toEqual({
      id: '300',
      model: { status: 0, name: 'Qwen3 8B', model: 'qwen3:8b' },
      clearApiKey: true,
    })
  })

  it('deletes_a_tenant_model_by_path_id', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await deleteModel('300')
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/300')
    expect(init(call).method).toBe('DELETE')
  })

  it('creates_a_platform_model_on_the_super_route', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, modelDTO({ platform: true })))
    await createModelSuper(
      { kind: 'embedding', name: 'Embedding 0.6B', modelKey: 'qwen3-embedding:0.6b', model: 'qwen3-embedding:0.6b', dimension: 1024 },
      'secret',
    )
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/platform/create')
    expect(init(call).method).toBe('POST')
    expect(body(call)).toEqual({
      model: {
        kind: 'embedding',
        name: 'Embedding 0.6B',
        modelKey: 'qwen3-embedding:0.6b',
        model: 'qwen3-embedding:0.6b',
        dimension: 1024,
      },
      apiKey: 'secret',
    })
  })

  it('updates_a_platform_model_on_the_super_route', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, modelDTO({ platform: true })))
    await updateModelSuper({ id: '300', model: { status: 1, name: 'Embedding 0.6B', model: 'qwen3-embedding:0.6b', dimension: 1024 } })
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/platform/update')
    expect(init(call).method).toBe('PUT')
    expect(body(call)).toEqual({
      id: '300',
      model: { status: 1, name: 'Embedding 0.6B', model: 'qwen3-embedding:0.6b', dimension: 1024 },
    })
  })

  it('deletes_a_platform_model_on_the_super_route', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await deleteModelSuper('300')
    const call = fetchMock.mock.calls[0]
    expect(String(call[0])).toBe('/v1/models/platform/300')
    expect(init(call).method).toBe('DELETE')
  })
})
