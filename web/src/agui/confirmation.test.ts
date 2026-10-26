import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import {
  AlreadyDecidedError,
  buildDecisionBody,
  decideConfirmation,
  fetchPendingConfirmations,
} from './confirmation'

// 确认卡契约测试走真实 authFetch（只 stub 全局 fetch），断言请求方法、
// 路径与请求体字段与 ackhttp decideRequest / PendingItem 一字对齐。
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

describe('buildDecisionBody remember scope state machine', () => {
  it('omits_remember_when_scope_is_none', () => {
    expect(buildDecisionBody('thread-1', 'c-1', true, 'none')).toEqual({
      threadId: 'thread-1',
      confirmationId: 'c-1',
      approved: true,
    })
  })

  it('carries_session_scope_for_approved_decisions', () => {
    expect(buildDecisionBody('thread-1', 'c-1', true, 'session')).toEqual({
      threadId: 'thread-1',
      confirmationId: 'c-1',
      approved: true,
      remember: 'session',
    })
  })

  it('carries_global_scope_and_false_approved_for_denied_decisions', () => {
    expect(buildDecisionBody('thread-1', 'c-1', false, 'global')).toEqual({
      threadId: 'thread-1',
      confirmationId: 'c-1',
      approved: false,
      remember: 'global',
    })
  })
})

describe('decideConfirmation request contract', () => {
  it('posts_the_decision_body_to_the_ack_confirmation_endpoint', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { path: 'live' }))

    await decideConfirmation({ threadId: 'thread-1', confirmationId: 'c-1', approved: true, remember: 'session' })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/agui/ack/confirmation')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({
      threadId: 'thread-1',
      confirmationId: 'c-1',
      approved: true,
      remember: 'session',
    })
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer access-1')
  })

  it('maps_http_404_to_the_already_decided_error', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { error: 'not found' }))

    const promise = decideConfirmation({ threadId: 'thread-1', confirmationId: 'c-1', approved: false, remember: 'none' })

    await expect(promise).rejects.toBeInstanceOf(AlreadyDecidedError)
  })

  it('throws_a_readable_error_on_other_http_failures', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { message: 'gateway broken' }))

    await expect(
      decideConfirmation({ threadId: 'thread-1', confirmationId: 'c-1', approved: true, remember: 'none' }),
    ).rejects.toThrow('confirmation failed: HTTP 500')
  })
})

describe('fetchPendingConfirmations polling contract', () => {
  it('queries_the_thread_and_keeps_pending_items_only', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, [
        { confirmationId: 'c-1', toolCallId: 'call-1', toolName: 'a', sessionId: 't', status: 'pending', createdAt: 1 },
        { confirmationId: 'c-2', toolCallId: 'call-2', toolName: 'b', sessionId: 't', status: 'denied', createdAt: 2 },
      ]),
    )

    const items = await fetchPendingConfirmations('thread one')

    expect(fetchMock.mock.calls[0][0]).toBe('/agui/ack/confirmations?threadId=thread%20one')
    expect(items).toHaveLength(1)
    expect(items[0].confirmationId).toBe('c-1')
  })

  it('returns_an_empty_list_when_the_backend_sends_a_non_array_body', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, null))

    const items = await fetchPendingConfirmations('thread-1')

    expect(items).toEqual([])
  })
})
