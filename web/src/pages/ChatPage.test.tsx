import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, setSession } from '../auth/store'

vi.mock('../api/agentSessions', () => ({
  fetchAgentSessions: vi.fn(),
}))
vi.mock('../agui/history', () => ({
  fetchHistorySnapshot: vi.fn(),
}))
vi.mock('../agui/confirmation', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../agui/confirmation')>()),
  fetchPendingConfirmations: vi.fn(),
}))
vi.mock('../agui/agent', () => ({
  createChatAgent: vi.fn(),
}))
vi.mock('../agui/cancel', () => ({
  cancelRun: vi.fn(),
}))

import { fetchAgentSessions } from '../api/agentSessions'
import { fetchHistorySnapshot } from '../agui/history'
import { fetchPendingConfirmations } from '../agui/confirmation'
import { cancelRun } from '../agui/cancel'
import { createChatAgent } from '../agui/agent'
import { ChatPage } from './ChatPage'
import type { ChatItem } from '../agui/events'

const mockFetchSessions = vi.mocked(fetchAgentSessions)
const mockFetchHistory = vi.mocked(fetchHistorySnapshot)
const mockFetchPending = vi.mocked(fetchPendingConfirmations)
const mockCancelRun = vi.mocked(cancelRun)
const mockCreateAgent = vi.mocked(createChatAgent)

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

// AgentSubscriberLike 是 runAgent 订阅器参数的最小测试镜像：页面只依赖
// onEvent / onRunFailed 回调。
type AgentSubscriberLike = {
  onEvent?: (params: { event: unknown }) => void
  onRunFailed?: (params: { error: Error }) => void
}

function agentStub() {
  return {
    addMessage: vi.fn(),
    runAgent: vi.fn<(input?: unknown, subscriber?: AgentSubscriberLike) => Promise<unknown>>(
      async () => ({}),
    ),
    abortRun: vi.fn(),
  }
}

beforeEach(() => {
  setSession(loginReply())
  mockFetchSessions.mockResolvedValue([
    { id: 'thread-aaaa-1', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T02:00:00Z' },
    { id: 'thread-bbbb-2', createdAt: '2026-01-02T00:00:00Z', updatedAt: '2026-01-02T02:00:00Z' },
  ])
  mockFetchHistory.mockResolvedValue([])
  mockFetchPending.mockResolvedValue([])
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.resetAllMocks()
})

function renderChat() {
  return render(
    <MemoryRouter>
      <ChatPage />
    </MemoryRouter>,
  )
}

describe('ChatPage session list', () => {
  it('loads_and_renders_owned_sessions_with_metadata_on_mount', async () => {
    renderChat()

    const list = await screen.findByLabelText('会话列表')
    expect(mockFetchSessions).toHaveBeenCalledTimes(1)
    expect(list).toHaveTextContent('会话 thread-a')
    expect(list).toHaveTextContent('会话 thread-b')
  })

  it('shows_a_readable_alert_when_the_session_list_fails', async () => {
    mockFetchSessions.mockRejectedValue(new Error('backend down'))
    renderChat()

    expect(await screen.findByRole('alert')).toHaveTextContent('会话列表加载失败：backend down')
  })
})

describe('ChatPage thread switching', () => {
  it('switches_thread_and_reloads_history_when_a_session_is_selected', async () => {
    const user = userEvent.setup()
    const items: ChatItem[] = [{ kind: 'message', id: 'u-1', role: 'user', content: '旧会话消息' }]
    mockFetchHistory.mockImplementation(async (threadId) => (threadId === 'thread-bbbb-2' ? items : []))
    renderChat()

    await screen.findByLabelText('会话列表')
    // 初始会话不在列表内：点击列表中的第二个会话切换 thread。
    await user.click(screen.getByRole('button', { name: /会话 thread-b/ }))

    await waitFor(() => expect(mockFetchHistory).toHaveBeenLastCalledWith('thread-bbbb-2'))
    await waitFor(() => expect(screen.getByRole('button', { name: /会话 thread-b/ })).toHaveAttribute(
      'aria-current',
      'true',
    ))
    await screen.findByText('旧会话消息')
  })

  it('starts_a_new_thread_when_the_new_session_button_is_clicked', async () => {
    const user = userEvent.setup()
    renderChat()

    await screen.findByLabelText('会话列表')
    const historyCallsBefore = mockFetchHistory.mock.calls.length
    await user.click(screen.getByRole('button', { name: '新建会话' }))

    await waitFor(() => expect(mockFetchHistory.mock.calls.length).toBeGreaterThan(historyCallsBefore))
    const latestThreadId = mockFetchHistory.mock.calls.at(-1)?.[0]
    expect(latestThreadId).not.toBe('thread-aaaa-1')
    expect(latestThreadId).not.toBe('thread-bbbb-2')
  })
})

describe('ChatPage streaming run', () => {
  it('creates_the_agent_with_current_thread_and_sends_the_user_message', async () => {
    const user = userEvent.setup()
    const stub = agentStub()
    mockCreateAgent.mockReturnValue(stub as never)
    renderChat()

    await screen.findByLabelText('会话列表')
    await user.type(screen.getByLabelText('输入消息'), 'say ping')
    await user.click(screen.getByRole('button', { name: '发送' }))

    await waitFor(() => expect(stub.runAgent).toHaveBeenCalledTimes(1))
    const [agentInput, subscriber] = stub.runAgent.mock.calls[0]
    expect(agentInput).toMatchObject({ runId: expect.any(String) })
    expect(subscriber).toHaveProperty('onEvent')
    expect(mockCreateAgent).toHaveBeenCalledWith({ threadId: expect.any(String) })
    expect(stub.addMessage).toHaveBeenCalledWith(
      expect.objectContaining({ role: 'user', content: 'say ping' }),
    )
    // 用户消息即时上屏。
    expect(screen.getByRole('log')).toHaveTextContent('say ping')
  })

  it('renders_a_clear_error_banner_when_the_run_fails', async () => {
    const user = userEvent.setup()
    const stub = agentStub()
    stub.runAgent.mockImplementation(async (_input, subscriber) => {
      subscriber?.onEvent?.({
        event: { type: 'RUN_ERROR', message: 'model exploded' },
      })
      return {}
    })
    mockCreateAgent.mockReturnValue(stub as never)
    renderChat()

    await screen.findByLabelText('会话列表')
    await user.type(screen.getByLabelText('输入消息'), 'say ping')
    await user.click(screen.getByRole('button', { name: '发送' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('运行出错：model exploded')
  })

  it('offers_cancel_while_running_and_posts_the_active_run_id', async () => {
    const user = userEvent.setup()
    mockCancelRun.mockResolvedValue('cancelled')
    let releaseRun!: () => void
    const gate = new Promise<void>((resolve) => {
      releaseRun = resolve
    })
    const stub = agentStub()
    stub.runAgent.mockImplementation(async () => {
      await gate
      return {}
    })
    mockCreateAgent.mockReturnValue(stub as never)
    renderChat()

    await screen.findByLabelText('会话列表')
    await user.type(screen.getByLabelText('输入消息'), 'long run')
    await user.click(screen.getByRole('button', { name: '发送' }))

    const cancel = await screen.findByRole('button', { name: '取消' })
    await user.click(cancel)

    await waitFor(() => expect(mockCancelRun).toHaveBeenCalledTimes(1))
    const [threadId, runId] = mockCancelRun.mock.calls[0]
    expect(threadId).toBe(mockFetchHistory.mock.calls[0][0])
    expect(typeof runId).toBe('string')
    releaseRun()
  })
})
