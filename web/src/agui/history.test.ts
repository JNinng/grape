import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'
import { fetchHistorySnapshot } from './history'

// 历史快照走真实 authFetch（只 stub 全局 fetch）：以 SSE 文本流应答，
// 断言 POST /agui/history 的请求形状与 MESSAGES_SNAPSHOT 的折叠结果。
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

function sseResponse(body: string): Response {
  return new Response(body, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })
}

describe('fetchHistorySnapshot snapshot replay', () => {
  it('posts_run_agent_input_shape_and_folds_the_messages_snapshot', async () => {
    fetchMock.mockResolvedValue(
      sseResponse(
        [
          'data: {"type":"RUN_STARTED","threadId":"thread-1","runId":"snap"}',
          '',
          'data: {"type":"MESSAGES_SNAPSHOT","messages":[' +
            '{"id":"u-1","role":"user","content":"hi"},' +
            '{"id":"a-1","role":"assistant","content":"hello"}]}',
          '',
          'data: {"type":"RUN_FINISHED","threadId":"thread-1","runId":"snap"}',
          '',
        ].join('\n'),
      ),
    )

    const items = await fetchHistorySnapshot('thread-1')

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/agui/history')
    expect(init?.method).toBe('POST')
    const body = JSON.parse(String(init?.body)) as Record<string, unknown>
    expect(body).toMatchObject({ threadId: 'thread-1', messages: [], tools: [], context: [] })
    expect(typeof body.runId).toBe('string')

    expect(items).toEqual([
      { kind: 'message', id: 'u-1', role: 'user', content: 'hi' },
      { kind: 'message', id: 'a-1', role: 'assistant', content: 'hello' },
    ])
  })

  it('returns_empty_items_when_the_stream_has_no_snapshot', async () => {
    fetchMock.mockResolvedValue(sseResponse('data: {"type":"RUN_STARTED","threadId":"t","runId":"r"}\n\n'))

    const items = await fetchHistorySnapshot('thread-1')

    expect(items).toEqual([])
  })

  it('cancels_the_stream_and_returns_on_the_first_snapshot_without_waiting_for_run_end', async () => {
    // 快照即返：首份 MESSAGES_SNAPSHOT 到手即 reader.cancel()，不等待流
    // 关闭（快照运行其后还要走完 run 生命周期才结束）。流构造为发出快
    // 照后永不关闭，若实现仍读到底部，本用例会挂起超时而非通过。
    let streamCancelled = false
    const encoder = new TextEncoder()
    const neverEnding = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          encoder.encode(
            'data: {"type":"MESSAGES_SNAPSHOT","messages":[' +
              '{"id":"u-1","role":"user","content":"hi"}]}\n\n',
          ),
        )
      },
      cancel() {
        streamCancelled = true
      },
    })
    fetchMock.mockResolvedValue(
      new Response(neverEnding, { status: 200, headers: { 'Content-Type': 'text/event-stream' } }),
    )

    const items = await fetchHistorySnapshot('thread-1')

    expect(items).toEqual([{ kind: 'message', id: 'u-1', role: 'user', content: 'hi' }])
    expect(streamCancelled).toBe(true)
  })

  it('throws_on_http_failure', async () => {
    fetchMock.mockResolvedValue(new Response('gone', { status: 404 }))

    await expect(fetchHistorySnapshot('thread-1')).rejects.toThrow('history failed: HTTP 404')
  })
})
