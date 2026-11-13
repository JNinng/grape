import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ChatItem } from '../../agui/events'
import { QuestionNavigator, userQuestionsOf } from './QuestionNavigator'

afterEach(cleanup)

function historyItems(): ChatItem[] {
  return [
    { kind: 'message', id: 'u-1', role: 'user', content: '第一个问题' },
    { kind: 'message', id: 'a-1', role: 'assistant', content: '回答' },
    { kind: 'message', id: 'u-2', role: 'user', content: '第二个问题' },
  ]
}

describe('userQuestionsOf', () => {
  it('extracts_only_user_messages_in_stream_order', () => {
    expect(userQuestionsOf(historyItems())).toEqual([
      { id: 'u-1', text: '第一个问题' },
      { id: 'u-2', text: '第二个问题' },
    ])
  })

  it('drops_empty_user_messages', () => {
    const items: ChatItem[] = [{ kind: 'message', id: 'u-0', role: 'user', content: '' }]
    expect(userQuestionsOf(items)).toEqual([])
  })
})

describe('QuestionNavigator', () => {
  it('renders_nothing_without_user_questions', () => {
    const { container } = render(
      <QuestionNavigator
        items={[{ kind: 'message', id: 'a-1', role: 'assistant', content: '回答' }]}
        onJump={vi.fn()}
      />,
    )

    expect(container).toBeEmptyDOMElement()
  })

  it('renders_one_dot_per_question', () => {
    render(<QuestionNavigator items={historyItems()} onJump={vi.fn()} />)

    expect(screen.getAllByRole('button', { name: /跳转到提问/ })).toHaveLength(2)
  })

  it('lists_questions_on_hover_and_jumps_from_a_row', async () => {
    const user = userEvent.setup()
    const onJump = vi.fn()
    render(<QuestionNavigator items={historyItems()} onJump={onJump} />)

    await user.hover(screen.getByRole('button', { name: '跳转到提问 1' }))
    await user.click(await screen.findByRole('button', { name: '第二个问题' }))

    expect(onJump).toHaveBeenCalledWith('u-2')
    expect(screen.queryByRole('button', { name: '第二个问题' })).not.toBeInTheDocument()
  })

  it('jumps_from_a_dot', async () => {
    const user = userEvent.setup()
    const onJump = vi.fn()
    render(<QuestionNavigator items={historyItems()} onJump={onJump} />)

    await user.click(screen.getByRole('button', { name: '跳转到提问 2' }))

    expect(onJump).toHaveBeenCalledWith('u-2')
  })
})
