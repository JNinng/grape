import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../../api/types'
import { clearSession, setSession } from '../../auth/store'
import type { ChatItem } from '../../agui/events'

vi.mock('../../api/agentSessions', () => ({
  fetchAgentSessions: vi.fn(),
}))
vi.mock('../../agui/history', () => ({
  fetchHistorySnapshot: vi.fn(),
}))
vi.mock('../../api/agentAudit', () => ({
  fetchToolCallAudits: vi.fn(),
  fetchConfirmationAudits: vi.fn(),
}))
vi.mock('../../api/agentRetrievalLogs', () => ({
  fetchRetrievalLogs: vi.fn(),
}))

import { fetchAgentSessions } from '../../api/agentSessions'
import { fetchConfirmationAudits, fetchToolCallAudits } from '../../api/agentAudit'
import { fetchRetrievalLogs } from '../../api/agentRetrievalLogs'
import { fetchHistorySnapshot } from '../../agui/history'
import { ReplayPage } from './ReplayPage'

const mockFetchSessions = vi.mocked(fetchAgentSessions)
const mockFetchHistory = vi.mocked(fetchHistorySnapshot)
const mockFetchToolCalls = vi.mocked(fetchToolCallAudits)
const mockFetchConfirmations = vi.mocked(fetchConfirmationAudits)
const mockFetchRetrievals = vi.mocked(fetchRetrievalLogs)

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

beforeEach(() => {
  setSession(loginReply())
  mockFetchSessions.mockResolvedValue([
    { id: 'thread-1', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T02:00:00Z' },
  ])
  const items: ChatItem[] = [
    { kind: 'message', id: 'm-1', role: 'user', content: 'hello' },
    {
      kind: 'tool',
      toolCallId: 'call-1',
      name: 'search_internal_docs',
      argsText: '{"query":"grape"}',
      result: '{"document_id":"doc-1","content":"excerpt"}',
      done: true,
    },
    { kind: 'message', id: 'm-2', role: 'assistant', content: 'answer' },
  ]
  mockFetchHistory.mockResolvedValue(items)
  mockFetchToolCalls.mockResolvedValue([
    {
      id: 1,
      toolCallId: 'call-1',
      toolName: 'search_internal_docs',
      sessionId: 'thread-1',
      traceId: 'trace-a',
      argumentsSummary: '{"query":"grape"}',
      resultSummary: '{"document_id":"doc-1"}',
      retryCount: 0,
      durationMs: 42,
      status: 'success',
      calledAt: '2026-01-01T00:00:01Z',
    },
    {
      id: 2,
      toolCallId: 'call-2',
      toolName: 'echo',
      sessionId: 'thread-1',
      traceId: 'trace-b',
      retryCount: 0,
      durationMs: 3,
      status: 'success',
      calledAt: '2026-01-01T00:00:02Z',
    },
  ])
  mockFetchConfirmations.mockResolvedValue([
    {
      id: 21,
      confirmationId: 'conf-1',
      sessionId: 'thread-1',
      toolCallId: 'call-1',
      toolName: 'update_my_nickname',
      decision: 'approved',
      remember: 'session',
      path: 'live',
      decidedBy: 't-1:7',
      traceId: 'trace-a',
      decidedAt: '2026-01-01T00:00:04Z',
    },
    {
      id: 22,
      confirmationId: 'conf-2',
      sessionId: 'thread-1',
      toolCallId: 'call-2',
      toolName: 'update_my_nickname',
      decision: 'auto',
      path: 'auto',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:05Z',
    },
    {
      id: 23,
      confirmationId: 'conf-3',
      sessionId: 'thread-1',
      toolName: 'update_my_nickname',
      decision: 'auto_deny',
      path: 'auto',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:06Z',
    },
    {
      id: 24,
      confirmationId: 'conf-4',
      sessionId: 'thread-1',
      toolName: 'update_my_nickname',
      decision: 'risk_deny',
      path: 'auto',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:07Z',
    },
    {
      id: 25,
      confirmationId: 'conf-5',
      sessionId: 'thread-1',
      toolName: 'update_my_nickname',
      decision: 'legacy_approve',
      path: 'live',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:08Z',
    },
    {
      id: 26,
      confirmationId: 'conf-6',
      sessionId: 'thread-1',
      toolName: 'update_my_nickname',
      decision: 'risk_auto',
      path: 'auto',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:09Z',
    },
    {
      id: 27,
      confirmationId: 'conf-7',
      sessionId: 'thread-1',
      toolName: 'update_my_nickname',
      decision: 'denied',
      path: 'live',
      decidedBy: 't-1:7',
      decidedAt: '2026-01-01T00:00:10Z',
    },
  ])
  mockFetchRetrievals.mockResolvedValue([
    {
      id: 11,
      source: 'tool',
      status: 'success',
      queryText: 'grape',
      sessionId: 'thread-1',
      traceId: 'trace-a',
      requestedTopK: 5,
      effectiveTopK: 5,
      filterSummary: {},
      hits: [{ document_id: 'doc-1', score: 0.9, anchor: 'chars 0-64' }],
      durationMs: 20,
      createdAt: '2026-01-01T00:00:03Z',
    },
  ])
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.resetAllMocks()
})

function renderReplay() {
  return render(
    <MemoryRouter>
      <ReplayPage />
    </MemoryRouter>,
  )
}

// pickTraceOption 经可搜索下拉选中一个链路：聚焦打开候选列表后点选目标
// 选项（等价于 selectOptions 在原生 select 上的交互）。
async function pickTraceOption(user: ReturnType<typeof userEvent.setup>, traceId: string) {
  const input = screen.getByRole('combobox', { name: 'trace 过滤' })
  await user.click(input)
  await user.click(screen.getByRole('option', { name: traceId }))
}

describe('ReplayPage four-facet evidence view', () => {
  it('degrades_history_failure_to_empty_state_and_keeps_other_facets', async () => {
    // 无 track 事件会话：/history 快照上游 500（RunStartedEvent validation
    // failed）。回放面降级为空态提示而非整页错误横幅，审计与留痕照常装载。
    mockFetchHistory.mockRejectedValue(new Error('history failed: HTTP 500'))
    renderReplay()
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledWith('thread-1')
    })
    expect(await screen.findByText('该会话无可回放快照（可能没有持久化的事件），其余证据面不受影响。')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    const auditRegion = screen.getByRole('region', { name: '工具调用审计' })
    expect(within(auditRegion).getByText('search_internal_docs')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '检索留痕' })).toBeInTheDocument()
  })

  it('renders_all_four_sections_for_the_default_thread', async () => {
    renderReplay()
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledWith('thread-1')
    })
    expect(screen.getByRole('region', { name: '对话流回放' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '工具调用审计' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '授权确认记录' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '检索留痕' })).toBeInTheDocument()
    // 回放渲染快照消息；审计与留痕行渲染脱敏摘要与命中。
    expect(await screen.findByText('answer')).toBeInTheDocument()
    const auditRegion = screen.getByRole('region', { name: '工具调用审计' })
    expect(within(auditRegion).getByText('search_internal_docs')).toBeInTheDocument()
    expect(within(auditRegion).getAllByText('0（框架未暴露）').length).toBeGreaterThan(0)
    const retrievalRegion = screen.getByRole('region', { name: '检索留痕' })
    expect(within(retrievalRegion).getByText('doc-1')).toBeInTheDocument()
    // 未选中链路：证据行中性呈现，不预高亮也不灰显。
    expect(within(auditRegion).getByText('echo').closest('tr')).not.toHaveClass('bg-violet-50', 'opacity-40')
    expect(retrievalRegion.querySelector('li[data-trace-id="trace-a"]')).not.toHaveClass('bg-violet-50', 'opacity-40')
  })

  it('keeps_all_evidence_rows_visible_and_dims_misses_when_a_trace_is_selected', async () => {
    // 链路选中只做高亮对比，不隐藏数据：命中行（trace-b 的 echo）品牌紫
    // 高亮，未命中行（trace-a 的工具审计 / 授权确认 / 检索留痕）灰显但仍
    // 在文档中，保住证据间的上下文。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await pickTraceOption(user, 'trace-b')

    const auditRegion = screen.getByRole('region', { name: '工具调用审计' })
    const echoRow = within(auditRegion).getByText('echo').closest('tr')
    expect(echoRow).toHaveClass('bg-violet-50')
    expect(echoRow).not.toHaveClass('opacity-40')
    expect(within(auditRegion).getByText('search_internal_docs').closest('tr')).toHaveClass('opacity-40')

    const confirmRegion = screen.getByRole('region', { name: '授权确认记录' })
    const confirmRows = within(confirmRegion)
      .getAllByText('update_my_nickname')
      .map((el) => el.closest('tr'))
    expect(confirmRows).toHaveLength(7)
    for (const row of confirmRows) {
      expect(row).toHaveClass('opacity-40')
    }

    const retrievalRegion = screen.getByRole('region', { name: '检索留痕' })
    expect(retrievalRegion.querySelector('li[data-trace-id="trace-a"]')).toHaveClass('opacity-40')

    // 回放工具项按 toolCallId 桥接高亮：trace-b 的审计行（call-2）不在回
    // 放流内 → 无高亮，也不降透明度（无命中项时整流保持原样）。
    expect(screen.queryByRole('group', { name: /当前链路/ })).not.toBeInTheDocument()
  })

  it('highlights_hit_rows_across_all_evidence_facets_for_the_selected_trace', async () => {
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await pickTraceOption(user, 'trace-a')

    const auditRegion = screen.getByRole('region', { name: '工具调用审计' })
    expect(within(auditRegion).getByText('search_internal_docs').closest('tr')).toHaveClass('bg-violet-50')
    const confirmRegion = screen.getByRole('region', { name: '授权确认记录' })
    // trace-a 只命中 approved 行（trace-a），其余行按未命中呈现。
    expect(within(confirmRegion).getByText('approved').closest('tr')).toHaveClass('bg-violet-50')
    expect(within(confirmRegion).getByText('auto_deny').closest('tr')).not.toHaveClass('bg-violet-50')
    const retrievalRegion = screen.getByRole('region', { name: '检索留痕' })
    expect(retrievalRegion.querySelector('li[data-trace-id="trace-a"]')).toHaveClass('bg-violet-50')
  })

  it('filters_dropdown_options_by_frontend_search_before_selection', async () => {
    // 可搜索下拉：输入子串即时前端过滤候选（含清除项恒在），无匹配给占
    // 位提示；从过滤结果中选中即生效。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')

    const input = screen.getByRole('combobox', { name: 'trace 过滤' })
    await user.click(input)
    expect(screen.getByRole('listbox', { name: 'trace 候选' })).toBeInTheDocument()
    await user.type(input, 'b')

    const listbox = screen.getByRole('listbox', { name: 'trace 候选' })
    expect(within(listbox).getByRole('option', { name: 'trace-b' })).toBeInTheDocument()
    expect(within(listbox).queryByRole('option', { name: 'trace-a' })).not.toBeInTheDocument()

    await user.click(within(listbox).getByRole('option', { name: 'trace-b' }))
    await waitFor(() => {
      expect(within(screen.getByRole('region', { name: '工具调用审计' })).getByText('echo').closest('tr')).toHaveClass(
        'bg-violet-50',
      )
    })

    // 重新聚焦回显当前链路；搜索无匹配给占位提示，选「全部链路」清除。
    await user.click(screen.getByRole('combobox', { name: 'trace 过滤' }))
    await user.type(screen.getByRole('combobox', { name: 'trace 过滤' }), 'zzz')
    expect(screen.getByText('无匹配链路')).toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: '全部链路' }))
    expect(screen.queryByRole('group', { name: /当前链路/ })).not.toBeInTheDocument()
  })

  it('picks_the_trace_filter_from_track_buttons_on_evidence_rows', async () => {
    // 追踪按钮：凡携带 trace_id 的证据行提供一键选中该链路（再点同值经
    // 反选清除）——授权确认表的「追踪 trace-a」选中后命中行高亮。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')

    const confirmRegion = screen.getByRole('region', { name: '授权确认记录' })
    await user.click(within(confirmRegion).getByRole('button', { name: '追踪 trace-a' }))
    await waitFor(() => {
      expect(within(confirmRegion).getByText('approved').closest('tr')).toHaveClass('bg-violet-50')
    })

    // 反选清除：再次点击同一按钮回到全链路视图。
    await user.click(within(confirmRegion).getByRole('button', { name: '追踪 trace-a' }))
    expect(within(confirmRegion).getByText('approved').closest('tr')).not.toHaveClass('bg-violet-50')
  })

  it('colors_confirmation_chips_by_decision_direction_with_fail_closed_red', async () => {
    // 决定方向配色：approved / auto / `*_auto` 绿（放行），denied /
    // auto_deny / `*_deny` 红（拒绝）；词表外取值 fail-closed 显红。
    // 「auto」同时出现在决定列与路径列，按 chip 类名收窄到决定徽标。
    renderReplay()
    await screen.findByText('answer')

    const confirmRegion = screen.getByRole('region', { name: '授权确认记录' })
    const chip = (decision: string) => {
      const chips = within(confirmRegion)
        .getAllByText(decision)
        .filter((el) => el.classList.contains('chip'))
      expect(chips).toHaveLength(1)
      return chips[0]
    }
    expect(chip('approved')).toHaveClass('bg-emerald-50')
    expect(chip('auto')).toHaveClass('bg-emerald-50')
    expect(chip('risk_auto')).toHaveClass('bg-emerald-50')
    expect(chip('denied')).toHaveClass('bg-red-50')
    expect(chip('auto_deny')).toHaveClass('bg-red-50')
    expect(chip('risk_deny')).toHaveClass('bg-red-50')
    expect(chip('legacy_approve')).toHaveClass('bg-red-50')

    // 回放工具项内嵌的授权确认行同规则（approved 行挂在 call-1 上）。
    const callRow = screen.getByLabelText('工具调用 search_internal_docs')
    expect(within(callRow).getByText('approved')).toHaveClass('bg-emerald-50')
  })

  it('highlights_the_replay_tool_call_linked_to_the_selected_trace', async () => {
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await pickTraceOption(user, 'trace-a')
    await waitFor(() => {
      expect(screen.getByLabelText('工具调用 search_internal_docs（当前链路）')).toBeInTheDocument()
    })
  })

  it('switches_threads_from_the_session_sidebar', async () => {
    mockFetchSessions.mockResolvedValue([
      { id: 'thread-1', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T02:00:00Z' },
      { id: 'thread-2', createdAt: '2026-01-02T00:00:00Z', updatedAt: '2026-01-02T02:00:00Z' },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await user.click(screen.getByRole('button', { name: '会话 thread-2' }))
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledWith('thread-2')
    })
  })

  it('discards_stale_history_response_when_a_newer_load_finished_first', async () => {
    // loadSeq 守卫：两次重叠装载，thread-1 的慢响应在 thread-2 的快响应
    // 之后才到达——过期响应必须整体丢弃（不落 items、不落错误、不关
    // loading 之外的任何状态回写）。
    mockFetchSessions.mockResolvedValue([
      { id: 'thread-1', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T02:00:00Z' },
      { id: 'thread-2', createdAt: '2026-01-02T00:00:00Z', updatedAt: '2026-01-02T02:00:00Z' },
    ])
    let releaseSlow: (items: ChatItem[]) => void = () => {}
    mockFetchHistory.mockImplementation((tid: string) => {
      if (tid === 'thread-1') {
        return new Promise<ChatItem[]>((resolve) => {
          releaseSlow = resolve
        })
      }
      return Promise.resolve([
        { kind: 'message', id: 'm-2', role: 'assistant', content: 'thread-2 answer' },
      ])
    })
    const user = userEvent.setup()
    renderReplay()
    // 初始装载选中 thread-1，其 history 挂起：页面处于 loading，无内容。
    await screen.findByRole('button', { name: '会话 thread-1' })
    expect(screen.queryByText('thread-2 answer')).toBeNull()
    await user.click(screen.getByRole('button', { name: '会话 thread-2' }))
    await screen.findByText('thread-2 answer')
    // 释放 thread-1 的慢响应：守卫应丢弃，页面仍显示 thread-2 内容。
    releaseSlow([
      { kind: 'message', id: 'm-stale', role: 'assistant', content: 'stale content' },
    ])
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledTimes(2)
    })
    expect(screen.queryByText('stale content')).toBeNull()
    expect(screen.getByText('thread-2 answer')).toBeInTheDocument()
  })
})
