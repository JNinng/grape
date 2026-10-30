import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ToolConfirmationPayload } from '../../agui/events'
import { AlreadyDecidedError, decideConfirmation } from '../../agui/confirmation'
import { ConfirmationCard } from './ConfirmationCard'

vi.mock('../../agui/confirmation', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../agui/confirmation')>()),
  decideConfirmation: vi.fn(),
}))

const mockDecide = vi.mocked(decideConfirmation)

afterEach(cleanup)

beforeEach(() => {
  mockDecide.mockReset()
})

const confirmation: ToolConfirmationPayload = {
  confirmationId: 'c-1',
  toolCallId: 'call-1',
  toolName: 'create_order',
  arguments: { item: 'apple', count: 2 },
  sessionId: 'thread-1',
  status: 'pending',
  createdAt: 1_700_000_000_000,
}

function renderCard(overrides: Partial<Parameters<typeof ConfirmationCard>[0]> = {}) {
  const onDecided = vi.fn()
  const onFailed = vi.fn()
  render(
    <ConfirmationCard
      confirmation={confirmation}
      threadId="thread-1"
      onDecided={onDecided}
      onFailed={onFailed}
      {...overrides}
    />,
  )
  return { onDecided, onFailed }
}

describe('ConfirmationCard decision state machine', () => {
  it('submits_approved_true_with_the_selected_session_scope_on_approve', async () => {
    const user = userEvent.setup()
    mockDecide.mockResolvedValue()
    const { onDecided, onFailed } = renderCard()

    await user.click(screen.getByRole('combobox', { name: '记住决定范围' }))
    await user.click(await screen.findByRole('option', { name: '仅本次会话' }))
    await user.click(screen.getByRole('button', { name: '批准' }))

    await waitFor(() =>
      expect(mockDecide).toHaveBeenCalledWith({
        threadId: 'thread-1',
        confirmationId: 'c-1',
        approved: true,
        remember: 'session',
      }),
    )
    expect(onDecided).toHaveBeenCalledWith('c-1')
    expect(onFailed).not.toHaveBeenCalled()
  })

  it('submits_approved_false_with_the_selected_global_scope_on_deny', async () => {
    const user = userEvent.setup()
    mockDecide.mockResolvedValue()
    const { onDecided, onFailed } = renderCard()

    await user.click(screen.getByRole('combobox', { name: '记住决定范围' }))
    await user.click(await screen.findByRole('option', { name: '全局记住' }))
    await user.click(screen.getByRole('button', { name: '拒绝' }))

    await waitFor(() =>
      expect(mockDecide).toHaveBeenCalledWith({
        threadId: 'thread-1',
        confirmationId: 'c-1',
        approved: false,
        remember: 'global',
      }),
    )
    expect(onDecided).toHaveBeenCalledWith('c-1')
    expect(onFailed).not.toHaveBeenCalled()
  })

  it('treats_an_already_decided_conflict_as_handled_not_failed', async () => {
    const user = userEvent.setup()
    mockDecide.mockRejectedValue(new AlreadyDecidedError())
    const { onDecided, onFailed } = renderCard()

    await user.click(screen.getByRole('button', { name: '批准' }))

    await waitFor(() => expect(onDecided).toHaveBeenCalledWith('c-1'))
    expect(onFailed).not.toHaveBeenCalled()
  })

  it('reports_a_readable_failure_when_the_decision_request_fails', async () => {
    const user = userEvent.setup()
    mockDecide.mockRejectedValue(new Error('gateway broken'))
    const { onDecided, onFailed } = renderCard()

    await user.click(screen.getByRole('button', { name: '拒绝' }))

    await waitFor(() => expect(onFailed).toHaveBeenCalledWith('c-1', 'gateway broken'))
    expect(onDecided).not.toHaveBeenCalled()
  })

  it('renders_the_tool_name_and_arguments_as_plain_text', () => {
    renderCard()

    expect(screen.getByRole('group', { name: '工具确认：create_order' })).toBeInTheDocument()
    expect(screen.getByText('create_order')).toBeInTheDocument()
    // 参数以纯文本渲染，绝不解释为 HTML。
    expect(screen.getByText('{ "item": "apple", "count": 2 }')).toBeInTheDocument()
    expect(screen.getByRole('group').querySelector('img,script')).toBeNull()
  })
})
