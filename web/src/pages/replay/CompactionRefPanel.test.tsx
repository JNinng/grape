import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CompactionRef } from './compaction'
import { CompactionRefPanel } from './CompactionRefPanel'

// 组件最少集：压缩占位符徽标与原文对照展开（A3 的核心交互件）。

vi.mock('../../api/agentEventWindow', () => ({
  fetchEventWindow: vi.fn(),
}))

import { fetchEventWindow } from '../../api/agentEventWindow'

const mockFetchWindow = vi.mocked(fetchEventWindow)

beforeEach(() => {
  mockFetchWindow.mockResolvedValue({
    sessionId: 'sess-1',
    anchorEventId: 'evt-1',
    events: [{ eventId: 'evt-1', createdAt: '2026-01-01T00:00:00Z', role: 'tool', content: 'ORIGINAL-RAW-RESULT' }],
  })
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

const placeholderRef: CompactionRef = {
  kind: 'placeholder',
  eventId: 'evt-1',
  toolCallId: 'call-1',
  toolName: 'echo',
  reason: 'history_compaction',
}

describe('CompactionRefPanel', () => {
  it('renders_the_compaction_badge_for_a_recoverable_placeholder', () => {
    render(<CompactionRefPanel compactionRef={placeholderRef} threadId="sess-1" />)
    expect(screen.getByRole('note', { name: '压缩标记' })).toHaveTextContent('已压缩')
    expect(screen.getByText('对照原文')).toBeInTheDocument()
  })

  it('fetches_and_renders_the_original_text_when_expanded', async () => {
    const user = userEvent.setup()
    render(<CompactionRefPanel compactionRef={placeholderRef} threadId="sess-1" />)
    await user.click(screen.getByText('对照原文'))
    await waitFor(() => {
      expect(mockFetchWindow).toHaveBeenCalledWith('sess-1', 'evt-1')
    })
    expect(await screen.findByText('ORIGINAL-RAW-RESULT')).toBeInTheDocument()
  })

  it('fetches_once_across_repeated_toggles', async () => {
    const user = userEvent.setup()
    render(<CompactionRefPanel compactionRef={placeholderRef} threadId="sess-1" />)
    await user.click(screen.getByText('对照原文'))
    await screen.findByText('ORIGINAL-RAW-RESULT')
    await user.click(screen.getByText('对照原文'))
    await user.click(screen.getByText('对照原文'))
    expect(mockFetchWindow).toHaveBeenCalledTimes(1)
  })

  it('shows_the_error_with_retry_when_the_original_lookup_fails', async () => {
    mockFetchWindow.mockRejectedValue(new Error('anchor event not found: evt-1'))
    const user = userEvent.setup()
    render(<CompactionRefPanel compactionRef={placeholderRef} threadId="sess-1" />)
    await user.click(screen.getByText('对照原文'))
    expect(await screen.findByRole('alert')).toHaveTextContent('原文加载失败：anchor event not found: evt-1')
    mockFetchWindow.mockResolvedValue({
      sessionId: 'sess-1',
      anchorEventId: 'evt-1',
      events: [{ eventId: 'evt-1', createdAt: '2026-01-01T00:00:00Z', content: 'RECOVERED' }],
    })
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('RECOVERED')).toBeInTheDocument()
  })

  it('marks_an_unrecoverable_placeholder_without_an_expand', () => {
    render(<CompactionRefPanel compactionRef={{ kind: 'policy' }} threadId="sess-1" />)
    expect(screen.getByRole('note', { name: '压缩标记' })).toHaveTextContent('已压缩')
    expect(screen.getByText('（无恢复引用，原文不可反查）')).toBeInTheDocument()
    expect(screen.queryByText('对照原文')).not.toBeInTheDocument()
    expect(mockFetchWindow).not.toHaveBeenCalled()
  })
})
