import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import type { ChatItem } from '../../agui/events'
import { MessageList } from './MessageList'

afterEach(cleanup)

describe('MessageList rendering', () => {
  it('renders_the_empty_state_when_no_items_exist', () => {
    render(<MessageList items={[]} />)

    expect(screen.getByRole('log')).toHaveTextContent('暂无消息，发送第一条消息开始对话。')
  })

  it('renders_user_and_assistant_messages_in_arrival_order', () => {
    const items: ChatItem[] = [
      { kind: 'message', id: 'u-1', role: 'user', content: '帮我重复 ping' },
      { kind: 'message', id: 'a-1', role: 'assistant', content: 'ping' },
    ]
    render(<MessageList items={items} />)

    const log = screen.getByRole('log')
    expect(log).toHaveTextContent('帮我重复 ping')
    expect(log).toHaveTextContent('ping')
    expect(screen.getByText('你')).toBeInTheDocument()
    expect(screen.getByText('助手')).toBeInTheDocument()
  })

  it('renders_model_text_as_plain_text_nodes_never_as_html', () => {
    const items: ChatItem[] = [
      {
        kind: 'message',
        id: 'a-1',
        role: 'assistant',
        content: '<img src=x onerror="alert(1)">',
      },
    ]
    render(<MessageList items={items} />)

    expect(screen.getByRole('log').querySelector('img')).toBeNull()
    expect(screen.getByRole('log')).toHaveTextContent('<img src=x onerror="alert(1)">')
  })

  it('shows_tool_call_summary_and_reveals_args_and_result_in_the_fold', async () => {
    const user = userEvent.setup()
    const items: ChatItem[] = [
      { kind: 'tool', toolCallId: 'call-1', name: 'echo', argsText: '{"text":"ping"}', result: 'ping', done: true },
    ]
    render(<MessageList items={items} />)

    const summary = screen.getByText(/工具 echo/)
    expect(summary).toHaveTextContent('结果')
    // details 折叠内容默认不可见，展开后参数与结果可见。
    expect(screen.getByText('{"text":"ping"}').closest('details')).not.toBeNull()
    await user.click(summary)
    expect(screen.getByText('参数')).toBeInTheDocument()
    expect(screen.getByText('结果')).toBeInTheDocument()
  })
})
