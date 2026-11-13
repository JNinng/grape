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
    // 折叠内容默认不挂载，展开后参数与结果可见。
    expect(screen.queryByText('{"text":"ping"}')).not.toBeInTheDocument()
    await user.click(summary)
    expect(summary.closest('[aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('参数')).toBeInTheDocument()
    expect(screen.getByText('结果')).toBeInTheDocument()
  })

  it('renders_settled_reasoning_collapsed_and_reveals_its_text_on_expand', async () => {
    const user = userEvent.setup()
    const items: ChatItem[] = [
      { kind: 'reasoning', id: 'r-1', content: '先比对上下文', streaming: false },
    ]
    render(<MessageList items={items} />)

    const summary = screen.getByText('思考内容')
    expect(screen.queryByText('先比对上下文')).not.toBeInTheDocument()
    await user.click(summary)
    expect(summary.closest('[aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('先比对上下文')).toBeInTheDocument()
  })

  it('places_the_reasoning_block_under_the_assistant_turn', () => {
    const items: ChatItem[] = [
      { kind: 'message', id: 'u-1', role: 'user', content: '问' },
      { kind: 'reasoning', id: 'r-1', content: '先想一想', streaming: false },
      { kind: 'message', id: 'a-1', role: 'assistant', content: '答' },
    ]
    render(<MessageList items={items} />)

    const article = screen.getByText('思考内容').closest('article')
    expect(article).toHaveAttribute('data-role', 'assistant')
    expect(article).toHaveTextContent('助手')
    expect(article).toHaveTextContent('答')
    expect(screen.getByText('你').closest('article')).not.toBe(article)
  })

  it('renders_streaming_reasoning_as_a_thinking_indicator', () => {
    const items: ChatItem[] = [{ kind: 'reasoning', id: 'r-1', content: '思考中', streaming: true }]
    render(<MessageList items={items} />)

    const log = screen.getByRole('log')
    expect(log.querySelector('[data-reasoning-state="streaming"]')).not.toBeNull()
    expect(log).toHaveTextContent('正在思考…')
  })

  it('renders_a_trailing_thinking_indicator_while_running_without_streaming_items', () => {
    const items: ChatItem[] = [{ kind: 'message', id: 'u-1', role: 'user', content: '问' }]
    render(<MessageList items={items} running />)

    expect(screen.getByRole('status')).toHaveTextContent('正在思考…')
  })

  it('omits_the_trailing_indicator_while_a_text_message_streams', () => {
    const items: ChatItem[] = [
      { kind: 'message', id: 'a-1', role: 'assistant', content: 'partial', streaming: true },
    ]
    render(<MessageList items={items} running />)

    expect(screen.queryByText('正在思考…')).not.toBeInTheDocument()
    expect(screen.getByRole('log').querySelector('[data-streaming="true"]')).not.toBeNull()
  })

  it('omits_the_trailing_indicator_while_a_tool_call_runs', () => {
    const items: ChatItem[] = [
      { kind: 'tool', toolCallId: 'call-1', name: 'echo', argsText: '', result: null, done: false },
    ]
    render(<MessageList items={items} running />)

    expect(screen.getByText(/工具 echo · 运行中/)).toBeInTheDocument()
    expect(screen.queryByText('正在思考…')).not.toBeInTheDocument()
  })
})
