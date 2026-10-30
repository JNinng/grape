import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
  decideConfirmation: vi.fn(),
}))
vi.mock('../agui/agent', () => ({
  createChatAgent: vi.fn(),
}))
vi.mock('../agui/cancel', () => ({
  cancelRun: vi.fn(),
}))
vi.mock('../api/session', () => ({
  logout: vi.fn(),
}))

import { fetchAgentSessions } from '../api/agentSessions'
import { logout } from '../api/session'
import { fetchHistorySnapshot } from '../agui/history'
import { AlreadyDecidedError, decideConfirmation, fetchPendingConfirmations } from '../agui/confirmation'
import { cancelRun } from '../agui/cancel'
import { createChatAgent } from '../agui/agent'
import { ThemeProvider } from '../components/theme-provider'
import { ChatPage } from './ChatPage'
import type { ChatItem, ToolConfirmationPayload } from '../agui/events'

const mockFetchSessions = vi.mocked(fetchAgentSessions)
const mockFetchHistory = vi.mocked(fetchHistorySnapshot)
const mockFetchPending = vi.mocked(fetchPendingConfirmations)
const mockDecide = vi.mocked(decideConfirmation)
const mockCancelRun = vi.mocked(cancelRun)
const mockCreateAgent = vi.mocked(createChatAgent)
const mockLogout = vi.mocked(logout)

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
      <ThemeProvider>
        <ChatPage />
      </ThemeProvider>
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

describe('ChatPage logout confirmation', () => {
  it('asks_for_confirmation_before_logging_out_and_only_logs_out_on_confirm', async () => {
    const user = userEvent.setup()
    renderChat()
    await screen.findByLabelText('会话列表')

    // 点击登出图标：只弹确认框，不触发登出。
    await user.click(screen.getByRole('button', { name: '登出' }))
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toHaveTextContent('确认登出？')
    expect(mockLogout).not.toHaveBeenCalled()

    // 取消：关框且不登出。
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockLogout).not.toHaveBeenCalled()

    // 再次打开并确认：此时才登出。
    await user.click(screen.getByRole('button', { name: '登出' }))
    await user.click(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: '登出' }),
    )
    await waitFor(() => expect(mockLogout).toHaveBeenCalledTimes(1))
  })
})

describe('ChatPage history convergence after decision', () => {
  // 说明：本组用例在假定时器下运行，交互用 fireEvent（同步派发）而非
  // userEvent——后者经 RTL asyncWrapper 包裹，其收尾的微任务冲刷依赖
  // jest 专属 API（jest.advanceTimersByTime），vitest 环境下会永久挂起。
  // 所有异步链（挂载拉取、决定提交、轮询）一律用假时间推进显式冲刷。

  function pendingConfirmation(): ToolConfirmationPayload {
    return {
      confirmationId: 'conf-1',
      toolCallId: 'call-1',
      toolName: 'echo',
      arguments: { text: 'ping' },
      sessionId: 'thread-pending',
      status: 'pending',
      createdAt: 1,
      live: true,
    }
  }

  it('repolls_history_after_a_decision_until_adjacent_snapshots_match', async () => {
    vi.useFakeTimers()
    try {
      // 决定后服务端待确认列表不再含该项：首查返回 pending，其后为空，
      // 否则兜底查询会把已移除的卡片加回来。
      mockFetchPending.mockResolvedValueOnce([pendingConfirmation()]).mockResolvedValue([])
      mockDecide.mockResolvedValue(undefined)
      const settled: ChatItem[] = [{ kind: 'message', id: 'a-1', role: 'assistant', content: '决定后的回答' }]
      mockFetchHistory
        .mockResolvedValueOnce([]) // 挂载快照（空会话）
        .mockResolvedValueOnce(settled) // 决定后第 1 轮（1s）
        .mockResolvedValue(settled) // 决定后第 2 轮（2s）与上轮一致 → 收敛
      renderChat()
      await vi.advanceTimersByTimeAsync(0)
      await vi.advanceTimersByTimeAsync(0)
      fireEvent.click(within(screen.getByTestId('confirmation-card')).getByRole('button', { name: '批准' }))
      await vi.advanceTimersByTimeAsync(0)
      // 节奏 1s/2s/2s/3s/3s：第 2 轮与第 1 轮一致即停，其后推进不再拉取。
      await vi.advanceTimersByTimeAsync(1_000)
      await vi.advanceTimersByTimeAsync(2_000)
      await vi.advanceTimersByTimeAsync(8_000)

      expect(mockFetchHistory).toHaveBeenCalledTimes(3)
      const mountedThread = mockFetchHistory.mock.calls[0][0]
      expect(mockFetchHistory).toHaveBeenLastCalledWith(mountedThread)
      // 整表替换生效：快照内容上屏。
      expect(screen.getByRole('log')).toHaveTextContent('决定后的回答')
    } finally {
      vi.useRealTimers()
    }
  })

  it('repolls_history_after_the_already_decided_404_convergence_without_an_error', async () => {
    vi.useFakeTimers()
    try {
      mockFetchPending.mockResolvedValueOnce([pendingConfirmation()]).mockResolvedValue([])
      // 404（并发已决定）在确认卡内收敛为 onDecided 共用回调：同样触发
      // 历史重拉，且不报错打断用户。
      mockDecide.mockRejectedValue(new AlreadyDecidedError())
      mockFetchHistory.mockResolvedValue([])
      renderChat()
      await vi.advanceTimersByTimeAsync(0)
      await vi.advanceTimersByTimeAsync(0)
      fireEvent.click(within(screen.getByTestId('confirmation-card')).getByRole('button', { name: '拒绝' }))
      await vi.advanceTimersByTimeAsync(0)
      await vi.advanceTimersByTimeAsync(1_000)

      expect(mockFetchHistory).toHaveBeenCalledTimes(2)
      expect(screen.queryByRole('alert')).toBeNull()
      expect(screen.queryByTestId('confirmation-card')).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })
})
