import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ChatItem } from '../../agui/events'
import { MessageList } from './MessageList'

afterEach(cleanup)

// stubClipboard 安装可断言的剪贴板替身；用例结束调用 restoreClipboard。
function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  return writeText
}

function restoreClipboard() {
  Reflect.deleteProperty(navigator, 'clipboard')
}

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

  it('renders_model_markdown_as_escaped_nodes_never_as_raw_html', () => {
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

  it('copies_the_user_question_from_the_hover_action', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      const items: ChatItem[] = [{ kind: 'message', id: 'u-1', role: 'user', content: '帮我重复 ping' }]
      render(<MessageList items={items} />)

      const article = screen.getByText('帮我重复 ping').closest('article')
      await user.click(within(article as HTMLElement).getByRole('button', { name: '复制提问' }))
      expect(writeText).toHaveBeenCalledWith('帮我重复 ping')
    } finally {
      restoreClipboard()
    }
  })

  it('renders_assistant_markdown_and_copies_the_source', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      const items: ChatItem[] = [
        { kind: 'message', id: 'a-1', role: 'assistant', content: '**加粗** 与 `code`' },
      ]
      render(<MessageList items={items} />)

      expect(screen.getByRole('log').querySelector('[data-streamdown="strong"]')).toHaveTextContent('加粗')
      // 复制选项默认收在图标弹框里，悬浮图标后展开。
      expect(screen.queryByRole('button', { name: '复制原文' })).toBeNull()
      await user.hover(screen.getByRole('button', { name: '复制回答' }))
      await user.click(await screen.findByRole('button', { name: '复制原文' }))
      expect(writeText).toHaveBeenCalledWith('**加粗** 与 `code`')
    } finally {
      restoreClipboard()
    }
  })

  it('copies_the_whole_rendered_answer_from_the_bottom_toolbar', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      const items: ChatItem[] = [
        { kind: 'message', id: 'a-1', role: 'assistant', content: '**加粗** 与 `code`' },
      ]
      render(<MessageList items={items} />)

      const toolbar = screen.getByRole('toolbar', { name: '回答工具' })
      await user.hover(within(toolbar).getByRole('button', { name: '复制回答' }))
      await user.click(await screen.findByRole('button', { name: '复制全文' }))
      expect(writeText).toHaveBeenCalledWith('加粗 与 code')
    } finally {
      restoreClipboard()
    }
  })

  it('copies_reasoning_source_from_the_fold_header', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      const items: ChatItem[] = [
        { kind: 'reasoning', id: 'r-1', content: '先想 **一下**', streaming: false },
      ]
      render(<MessageList items={items} />)

      await user.click(screen.getByRole('button', { name: '复制原文' }))
      expect(writeText).toHaveBeenCalledWith('先想 **一下**')
    } finally {
      restoreClipboard()
    }
  })

  it('exposes_streamdown_copy_controls_for_code_blocks', async () => {
    const items: ChatItem[] = [
      { kind: 'message', id: 'a-1', role: 'assistant', content: '```js\nconst a = 1\n```' },
    ]
    render(<MessageList items={items} />)

    expect(await screen.findByRole('button', { name: '复制代码' })).toBeInTheDocument()
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

  it('copies_tool_arguments_and_results_from_the_expanded_card', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      const items: ChatItem[] = [
        { kind: 'tool', toolCallId: 'call-1', name: 'echo', argsText: '{"text":"ping"}', result: 'ping', done: true },
      ]
      render(<MessageList items={items} />)

      await user.click(screen.getByText(/工具 echo/))
      await user.click(screen.getByRole('button', { name: '复制参数' }))
      await user.click(screen.getByRole('button', { name: '复制结果' }))

      expect(writeText).toHaveBeenNthCalledWith(1, '{"text":"ping"}')
      expect(writeText).toHaveBeenNthCalledWith(2, 'ping')
    } finally {
      restoreClipboard()
    }
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
