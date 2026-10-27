import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from './types'
import { clearSession, setSession } from '../auth/store'
import { fetchConfirmationAudits, fetchToolCallAudits } from './agentAudit'
import { fetchEventWindow } from './agentEventWindow'
import { fetchRetrievalLogs } from './agentRetrievalLogs'

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

describe('agent replay api request shapes', () => {
  it('tool_call_audits_request_the_session_filtered_page', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: 0 }))

    await fetchToolCallAudits('sess-1', 10, 20)

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/agent/audit/tool-calls?offset=10&limit=20&session_id=sess-1')
  })

  it('confirmation_audits_omit_the_session_filter_when_unset', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: 0 }))

    await fetchConfirmationAudits()

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/agent/audit/confirmations?offset=0&limit=50')
  })

  it('retrieval_logs_append_the_source_filter_only_when_set', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { items: [], total: 0 }))

    await fetchRetrievalLogs('sess-1', 'tool')

    expect(fetchMock.mock.calls[0][0]).toBe('/v1/agent/retrieval-logs?offset=0&limit=50&session_id=sess-1&source=tool')
  })

  it('event_window_requests_the_anchor_with_span_defaults', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        sessionId: 'sess-1',
        anchorEventId: 'evt-1',
        events: [{ eventId: 'evt-1', createdAt: '2026-01-01T00:00:00Z', content: 'raw' }],
      }),
    )

    const window = await fetchEventWindow('sess-1', 'evt-1', 2, 3)

    expect(fetchMock.mock.calls[0][0]).toBe('/agui/event-window?threadId=sess-1&eventId=evt-1&before=2&after=3')
    expect(window.events).toHaveLength(1)
    expect(window.events[0].content).toBe('raw')
  })

  it('event_window_normalizes_a_missing_events_list_to_an_empty_array', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { sessionId: 'sess-1', anchorEventId: 'evt-1' }))

    const window = await fetchEventWindow('sess-1', 'evt-1')

    expect(window.events).toEqual([])
  })

  it('propagates_the_backend_error_message_on_failure', async () => {
    fetchMock.mockResolvedValue(jsonResponse(404, { message: 'anchor event not found: evt-x' }))

    await expect(fetchEventWindow('sess-1', 'evt-x')).rejects.toThrow('anchor event not found: evt-x')
  })
})
