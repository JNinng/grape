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
vi.mock('../../api/agentSessionUsage', () => ({
  fetchSessionUsage: vi.fn(),
}))
vi.mock('./detailCache', () => ({
  loadChunkDetail: vi.fn(),
  loadDocumentDetail: vi.fn(),
  forgetChunkDetail: vi.fn(),
  forgetDocumentDetail: vi.fn(),
  clearReplayDetailCache: vi.fn(),
}))

import { fetchAgentSessions } from '../../api/agentSessions'
import { fetchConfirmationAudits, fetchToolCallAudits } from '../../api/agentAudit'
import { fetchRetrievalLogs } from '../../api/agentRetrievalLogs'
import { fetchSessionUsage } from '../../api/agentSessionUsage'
import type { DocumentDTO } from '../../api/knowledge'
import { ApiError } from '../../api/toApiError'
import { clearReplayDetailCache, loadChunkDetail, loadDocumentDetail } from './detailCache'
import { fetchHistorySnapshot } from '../../agui/history'
import { ThemeProvider } from '../../components/theme-provider'
import { ReplayPage } from './ReplayPage'

const mockFetchSessions = vi.mocked(fetchAgentSessions)
const mockFetchHistory = vi.mocked(fetchHistorySnapshot)
const mockFetchToolCalls = vi.mocked(fetchToolCallAudits)
const mockFetchConfirmations = vi.mocked(fetchConfirmationAudits)
const mockFetchRetrievals = vi.mocked(fetchRetrievalLogs)
const mockFetchSessionUsage = vi.mocked(fetchSessionUsage)
const mockLoadChunkDetail = vi.mocked(loadChunkDetail)
const mockLoadDocumentDetail = vi.mocked(loadDocumentDetail)
const mockClearReplayDetailCache = vi.mocked(clearReplayDetailCache)

// stubClipboard 安装可断言的剪贴板替身；用例结束调用 restoreClipboard。
function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  return writeText
}

function restoreClipboard() {
  Reflect.deleteProperty(navigator, 'clipboard')
}

function documentDTO(overrides: Partial<DocumentDTO> = {}): DocumentDTO {
  return {
    id: '2',
    libraryId: '1',
    kind: 'text',
    title: '员工手册',
    status: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: '7', tenantId: 't-1', name: 'alice', nickname: 'Alice' },
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
      parentMessageId: 'chatcmpl-tool',
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
  mockFetchSessionUsage.mockResolvedValue([
    {
      messageId: 'chatcmpl-tool',
      invocationId: 'inv-1',
      model: 'qwen3:8b',
      usage: { prompt: 1973, completion: 145, total: 2118, cached: 1972, reasoning: 60 },
      reasoningMs: 2648,
      firstTokenMs: 143,
      createdAt: '2026-01-01T00:00:01Z',
    },
    {
      messageId: 'm-2',
      invocationId: 'inv-1',
      model: 'qwen3:8b',
      usage: { prompt: 2066, completion: 287, total: 2353 },
      reasoningMs: 2648,
      firstTokenMs: 143,
      createdAt: '2026-01-01T00:00:02Z',
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
      stages: [
        { stage: 'vector', query_text: 'grape', limit: 10, candidates: [{ document_id: 'doc-1', score: 0.9 }], duration_ms: 12 },
        { stage: 'lexical', query_text: 'grape', limit: 10, candidates: [], duration_ms: 3 },
        { stage: 'fusion', limit: 10, candidates: [{ document_id: 'doc-1', score: 0.03 }], duration_ms: 1 },
        {
          stage: 'rerank',
          model: 'bge-reranker@v1',
          limit: 5,
          candidates: [{ document_id: 'doc-1', score: 0.97 }],
          error: 'rerank failed: endpoint down',
          duration_ms: 30,
        },
        {
          stage: 'pack',
          limit: 4096,
          units: [
            { mode: 'window', source_id: 'doc', document_ids: ['doc-1'], tokens: 120, admitted: true },
            { mode: 'chunk', document_ids: ['doc-2'], tokens: 80, admitted: false, reason: 'truncated' },
          ],
          duration_ms: 2,
        },
      ],
      durationMs: 20,
      createdAt: '2026-01-01T00:00:03Z',
    },
  ])
  mockLoadDocumentDetail.mockResolvedValue(documentDTO())
  mockLoadChunkDetail.mockResolvedValue({
    chunk: {
      index: 0,
      chunkId: 'doc-1',
      anchor: 'chars 0-64',
      content: '分片原文内容',
      embedder: 'space@v1',
    },
    document: documentDTO(),
    supersededAt: '',
  })
})

afterEach(() => {
  cleanup()
  clearSession()
  vi.resetAllMocks()
})

function renderReplay() {
  return render(
    <MemoryRouter>
      <ThemeProvider>
        <ReplayPage />
      </ThemeProvider>
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

// retrievalRow 取指定链路的检索留痕行（收起态）。
function retrievalRow(traceId: string): HTMLElement {
  const region = screen.getByRole('region', { name: '检索留痕' })
  const row = region.querySelector(`li[data-trace-id="${traceId}"]`)
  if (!(row instanceof HTMLElement)) {
    throw new Error(`retrieval row ${traceId} not found`)
  }
  return row
}

// expandRetrievalRow 点开留痕行头部（展开后阶段列表与命中才挂载）。
async function expandRetrievalRow(user: ReturnType<typeof userEvent.setup>, traceId = 'trace-a') {
  await user.click(within(retrievalRow(traceId)).getByRole('button', { expanded: false }))
}

// expandStage 点开时间线第 index 个阶段（0 起），展开后候选/单元表挂载。
async function expandStage(user: ReturnType<typeof userEvent.setup>, index: number) {
  const timeline = screen.getByRole('list', { name: '检索阶段' })
  const items = within(timeline).getAllByRole('listitem')
  await user.click(within(items[index]).getByRole('button', { expanded: false }))
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
    // 框架不暴露工具重试（审计恒 0），审计表不渲染重试列。
    expect(within(auditRegion).queryByRole('columnheader', { name: '重试' })).not.toBeInTheDocument()
    const retrievalRegion = screen.getByRole('region', { name: '检索留痕' })
    // 留痕行默认收起：点开头部后命中与阶段列表才挂载。
    const user = userEvent.setup()
    await expandRetrievalRow(user)
    expect(await within(retrievalRegion).findByText('doc-1')).toBeInTheDocument()
    // 未选中链路：证据行中性呈现，不预高亮也不灰显。
    expect(within(auditRegion).getByText('echo').closest('tr')).not.toHaveClass('bg-brand-soft', 'opacity-40')
    expect(retrievalRegion.querySelector('li[data-trace-id="trace-a"]')).not.toHaveClass('bg-brand-soft', 'opacity-40')
  })

  it('renders_reasoning_content_collapsed_and_reveals_it_on_expand', async () => {
    mockFetchHistory.mockResolvedValue([
      { kind: 'message', id: 'u-1', role: 'user', content: '问' },
      { kind: 'reasoning', id: 'r-1', content: '先比对上下文再作答', streaming: false },
      { kind: 'message', id: 'a-1', role: 'assistant', content: '答' },
    ])
    const user = userEvent.setup()
    renderReplay()
    const region = await screen.findByRole('region', { name: '对话流回放' })
    const summary = await within(region).findByText('思考内容')
    // 思考块与回答同属助手回合：共用角色标签，不再是独立卡片。
    const article = summary.closest('article')
    expect(article).toHaveAttribute('data-role', 'assistant')
    expect(article).toHaveTextContent('assistant')
    expect(article).toHaveTextContent('答')
    // 默认收起：思考原文不挂载，展开后以纯文本呈现。
    expect(within(region).queryByText('先比对上下文再作答')).not.toBeInTheDocument()
    await user.click(summary)
    expect(summary.closest('[aria-expanded]')).toHaveAttribute('aria-expanded', 'true')
    expect(within(region).getByText('先比对上下文再作答')).toBeInTheDocument()
  })

  it('renders_distinct_vector_and_lexical_stage_labels_after_expanding_the_row', async () => {
    // 编排层写入的留痕行按阶段时间线渲染（向量召回 / 词法召回 → 融合 →
    // 重排 → 回包）：两路召回必须可区分；阶段名、查询变体、重排模型、
    // 候选/单元分因计数可见，阶段失败的错误摘要同样可见。行与阶段默认
    // 收起，需逐层点开。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    const timeline = await screen.findByRole('list', { name: '检索阶段' })
    const items = within(timeline).getAllByRole('listitem')
    expect(items).toHaveLength(5)
    expect(items[0]).toHaveTextContent('1. 向量召回')
    expect(items[0]).toHaveTextContent('「grape」')
    expect(items[0]).toHaveTextContent('候选 1 条')
    expect(items[1]).toHaveTextContent('2. 词法召回')
    expect(items[2]).toHaveTextContent('3. 融合')
    expect(items[3]).toHaveTextContent('4. 重排')
    expect(items[3]).toHaveTextContent('bge-reranker@v1')
    expect(items[3]).toHaveTextContent('rerank failed: endpoint down')
    expect(items[4]).toHaveTextContent('5. 回包')
    expect(items[4]).toHaveTextContent('单元 入包 1 · 截断 1')
    expect(items[4]).toHaveTextContent('2 ms')
  })

  it('renders_zero_duration_stage_instead_of_hiding_it', async () => {
    // 融合/回包常为 0ms：耗时字段恒显示（0 ms），不再因 0 被隐藏。
    mockFetchRetrievals.mockResolvedValue([
      {
        id: 12,
        source: 'console',
        status: 'success',
        queryText: '套餐',
        traceId: 'trace-a',
        filterSummary: {},
        requestedTopK: null,
        effectiveTopK: null,
        hits: [],
        stages: [
          { stage: 'vector', query_text: '套餐', limit: 10, candidates: [], duration_ms: 5 },
          { stage: 'fusion', limit: 10, candidates: [], duration_ms: 0 },
          { stage: 'pack', limit: 4096, units: [], duration_ms: 0 },
        ],
        durationMs: 5,
        createdAt: '2026-01-01T00:00:03Z',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    const timeline = await screen.findByRole('list', { name: '检索阶段' })
    const items = within(timeline).getAllByRole('listitem')
    expect(items[1]).toHaveTextContent('0 ms')
    expect(items[2]).toHaveTextContent('0 ms')
  })

  it('renders_merged_and_redundant_reasons_without_calling_them_truncated', async () => {
    // 未入包原因按 reason 展示：同源收编与冗余不得写成截断；摘要行按原因
    // 分组计数。
    mockFetchRetrievals.mockResolvedValue([
      {
        id: 13,
        source: 'tool',
        status: 'success',
        queryText: '套餐',
        traceId: 'trace-a',
        filterSummary: {},
        requestedTopK: null,
        effectiveTopK: null,
        hits: [],
        stages: [
          {
            stage: 'pack',
            limit: 4096,
            units: [
              { mode: 'document', source_id: '2', document_ids: ['2#0#a1b2c3d4'], tokens: 100, admitted: true },
              { mode: 'chunk', source_id: '2', document_ids: ['2#1#a1b2c3d4'], tokens: 40, admitted: false, reason: 'merged' },
              { mode: 'chunk', source_id: '2', document_ids: ['2#2#a1b2c3d4'], tokens: 30, admitted: false, reason: 'redundant' },
              { mode: 'chunk', source_id: '3', document_ids: ['3#0#a1b2c3d4'], tokens: 30, admitted: false, reason: 'truncated' },
            ],
            duration_ms: 1,
          },
        ],
        durationMs: 1,
        createdAt: '2026-01-01T00:00:03Z',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    const timeline = await screen.findByRole('list', { name: '检索阶段' })
    expect(within(timeline).getByRole('listitem')).toHaveTextContent('单元 入包 1 · 截断 1 · 收编 1 · 冗余 1')
    await expandStage(user, 0)
    expect(screen.getByText('同源收编')).toBeInTheDocument()
    expect(screen.getByText('冗余')).toBeInTheDocument()
    expect(screen.getByText('截断')).toBeInTheDocument()
    expect(screen.getAllByText('入包')).toHaveLength(1)
  })

  it('expands_stage_to_show_candidate_and_unit_detail_tables', async () => {
    // 阶段行可点开：候选表展示名次/分片 ID/分数/锚点，单元表展示聚合粒
    // 度、文档 ID、成员分片、章节、token 与结果。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    await expandStage(user, 4) // pack：单元表
    expect(screen.getByRole('columnheader', { name: '聚合粒度' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: '成员分片' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'token' })).toBeInTheDocument()
    expect(screen.getByText('window')).toBeInTheDocument()
    expect(screen.getByText('120')).toBeInTheDocument()
    // 向量召回阶段：候选表。
    await expandStage(user, 0)
    expect(screen.getByRole('columnheader', { name: '名次' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: '锚点' })).toBeInTheDocument()
    expect(screen.getAllByText('0.9000').length).toBeGreaterThanOrEqual(1)
  })

  it('renders_start_time_and_turn_latency_for_replay_items', async () => {
    // 回放项带起始时间时摘要行呈现「始于 时间」，工具与回复另给相对本轮
    // 用户消息的用时，回答「响应慢」时先看工具与回复何时开始。
    mockFetchHistory.mockResolvedValue([
      { kind: 'message', id: 'm-1', role: 'user', content: 'hello', startedAt: 1767225600000 },
      {
        kind: 'tool',
        toolCallId: 'call-1',
        name: 'search_internal_docs',
        argsText: '{"query":"grape"}',
        result: '{"document_id":"doc-1"}',
        done: true,
        startedAt: 1767225603000,
      },
      { kind: 'message', id: 'm-2', role: 'assistant', content: 'answer', startedAt: 1767225609000 },
    ])
    renderReplay()
    expect(await screen.findByText('answer')).toBeInTheDocument()

    const toolItem = screen.getByLabelText('工具调用 search_internal_docs')
    expect(within(toolItem).getByText('用时 3.0s')).toBeInTheDocument()
    const assistantArticle = screen.getByText('answer').closest('article')
    expect(assistantArticle?.textContent).toContain('用时 9.0s')
    // 用户消息是段首，不以自己为锚：只给起始时间，不显示 0.0s 用时。
    const userArticle = screen.getByText('hello').closest('article')
    expect(userArticle?.textContent).toContain('始于')
    expect(userArticle?.textContent).not.toContain('用时')
  })

  it('renders_model_tokens_and_timing_facts_for_assistant_and_tool_items', async () => {
    // 模型调用事实按 messageId 桥接：助手回复取自身 ID，工具调用取发起它
    // 的助手消息 ID（决策轮通常没有正文）。工具卡片的事实放在折叠区之外，
    // 收起时即可见。
    renderReplay()
    expect(await screen.findByText('answer')).toBeInTheDocument()

    const toolItem = screen.getByLabelText('工具调用 search_internal_docs')
    // 折叠区未展开（参数等正文未挂载），token 事实仍可见。
    expect(within(toolItem).queryByText('参数')).not.toBeInTheDocument()
    expect(within(toolItem).getByText('模型 qwen3:8b')).toBeInTheDocument()
    expect(within(toolItem).getByText('tokens 2118（输入 1973 / 输出 145 · 缓存 1972（99.95%） · 思考 60）')).toBeInTheDocument()
    // 首字与思考是流程级事实，只在本轮首条调用上呈现。
    expect(within(toolItem).getByText('本轮思考 2.6s')).toBeInTheDocument()
    expect(within(toolItem).getByText('本轮首字 143ms')).toBeInTheDocument()

    const assistantArticle = screen.getByText('answer').closest('article')
    expect(assistantArticle?.textContent).toContain('模型 qwen3:8b')
    expect(assistantArticle?.textContent).toContain('tokens 2353（输入 2066 / 输出 287）')
    // 同一轮的第二条调用不再重复同一数值（框架把它们复制到每条调用）。
    expect(assistantArticle?.textContent).not.toContain('本轮首字')
    expect(assistantArticle?.textContent).not.toContain('本轮思考')
  })

  it('omits_model_and_token_facts_when_usage_is_unavailable', async () => {
    // 用量端点缺失（旧会话或会话不可见）时页面照常回放，不呈现伪 token。
    mockFetchSessionUsage.mockResolvedValue([])
    renderReplay()
    expect(await screen.findByText('answer')).toBeInTheDocument()
    const assistantArticle = screen.getByText('answer').closest('article')
    expect(assistantArticle?.textContent).not.toContain('tokens')
    expect(assistantArticle?.textContent).not.toContain('模型 qwen3:8b')
  })

  it('renders_without_crashing_when_a_message_item_lacks_started_at', async () => {
    // 组件从 item 自身派生展示值，缺起始时间是正常降级路径，不能因
    // 缺字段抛错（历史会话与索引缺失都走这里）。
    mockFetchHistory.mockResolvedValue([
      { kind: 'message', id: 'm-1', role: 'user', content: 'hello' },
      { kind: 'message', id: 'm-2', role: 'assistant', content: 'answer' },
    ])
    renderReplay()
    expect(await screen.findByText('answer')).toBeInTheDocument()
    expect(screen.getByText('hello')).toBeInTheDocument()
  })

  it('shows_start_placeholder_without_latency_when_timestamps_are_absent', async () => {
    // 快照来源索引缺失（历史会话或后端未开启源元数据）时只给占位，
    // 不伪造时间，也不计算无锚点的用时。
    renderReplay()
    expect(await screen.findByText('answer')).toBeInTheDocument()
    const toolItem = screen.getByLabelText('工具调用 search_internal_docs')
    expect(within(toolItem).getByText(/始于/)).toBeInTheDocument()
    expect(within(toolItem).queryByText(/用时/)).not.toBeInTheDocument()
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
    expect(echoRow).toHaveClass('bg-brand-soft')
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
    expect(within(auditRegion).getByText('search_internal_docs').closest('tr')).toHaveClass('bg-brand-soft')
    const confirmRegion = screen.getByRole('region', { name: '授权确认记录' })
    // trace-a 只命中 approved 行（trace-a），其余行按未命中呈现。
    expect(within(confirmRegion).getByText('approved').closest('tr')).toHaveClass('bg-brand-soft')
    expect(within(confirmRegion).getByText('auto_deny').closest('tr')).not.toHaveClass('bg-brand-soft')
    const retrievalRegion = screen.getByRole('region', { name: '检索留痕' })
    expect(retrievalRegion.querySelector('li[data-trace-id="trace-a"]')).toHaveClass('bg-brand-soft')
  })

  it('filters_dropdown_options_by_frontend_search_before_selection', async () => {
    // 可搜索下拉：输入子串即时前端过滤候选（含清除项恒在），无匹配给占
    // 位提示；从过滤结果中选中即生效。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')

    const input = screen.getByRole('combobox', { name: 'trace 过滤' })
    await user.click(input)
    // 候选列表（Popover 内的 cmdk Command）经其根 label（aria-labelledby）命名。
    expect(screen.getByRole('listbox', { name: 'trace 候选' })).toBeInTheDocument()
    await user.type(input, 'b')

    const listbox = screen.getByRole('listbox', { name: 'trace 候选' })
    expect(within(listbox).getByRole('option', { name: 'trace-b' })).toBeInTheDocument()
    expect(within(listbox).queryByRole('option', { name: 'trace-a' })).not.toBeInTheDocument()

    await user.click(within(listbox).getByRole('option', { name: 'trace-b' }))
    await waitFor(() => {
      expect(within(screen.getByRole('region', { name: '工具调用审计' })).getByText('echo').closest('tr')).toHaveClass(
        'bg-brand-soft',
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
      expect(within(confirmRegion).getByText('approved').closest('tr')).toHaveClass('bg-brand-soft')
    })

    // 反选清除：再次点击同一按钮回到全链路视图。
    await user.click(within(confirmRegion).getByRole('button', { name: '追踪 trace-a' }))
    expect(within(confirmRegion).getByText('approved').closest('tr')).not.toHaveClass('bg-brand-soft')
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
        .filter((el) => el.getAttribute('data-slot') === 'badge')
      expect(chips).toHaveLength(1)
      return chips[0]
    }
    expect(chip('approved')).toHaveClass('bg-success-soft')
    expect(chip('auto')).toHaveClass('bg-success-soft')
    expect(chip('risk_auto')).toHaveClass('bg-success-soft')
    expect(chip('denied')).toHaveClass('bg-danger-soft')
    expect(chip('auto_deny')).toHaveClass('bg-danger-soft')
    expect(chip('risk_deny')).toHaveClass('bg-danger-soft')
    expect(chip('legacy_approve')).toHaveClass('bg-danger-soft')

    // 回放工具项内嵌的授权确认行同规则（approved 行挂在 call-1 上）：
    // 折叠项默认收起，展开后内容才挂载。
    const user = userEvent.setup()
    const callRow = screen.getByLabelText('工具调用 search_internal_docs')
    await user.click(within(callRow).getByRole('button'))
    expect(within(callRow).getByText('approved')).toHaveClass('bg-success-soft')
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
    // 按钮的可访问名含列表项内的更新时间，按前缀正则匹配。
    await user.click(screen.getByRole('button', { name: /会话 thread-2/ }))
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
    await screen.findByRole('button', { name: /会话 thread-1/ })
    expect(screen.queryByText('thread-2 answer')).toBeNull()
    await user.click(screen.getByRole('button', { name: /会话 thread-2/ }))
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

  it('rounds_hit_scores_and_labels_the_fusion_score_source', async () => {
    // hits 原始分数是长浮点：展示修约到 4 位；有 fusion 阶段无 rerank 时
    // 标注「融合分」。
    mockFetchRetrievals.mockResolvedValue([
      {
        id: 41,
        source: 'console',
        status: 'success',
        queryText: '套餐',
        traceId: 'trace-a',
        filterSummary: {},
        requestedTopK: null,
        effectiveTopK: null,
        hits: [{ document_id: 'doc-1', score: 0.032018442622950824 }],
        stages: [
          { stage: 'vector', query_text: '套餐', limit: 10, candidates: [{ document_id: 'doc-1', score: 0.9 }], duration_ms: 1 },
          { stage: 'fusion', limit: 10, candidates: [{ document_id: 'doc-1', score: 0.032018442622950824 }], duration_ms: 1 },
        ],
        durationMs: 5,
        createdAt: '2026-01-01T00:00:03Z',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    expect(await screen.findByText('命中 1 条 · 分数来源：融合分')).toBeInTheDocument()
    expect(screen.getAllByText('0.0320').length).toBeGreaterThanOrEqual(1)
  })

  it('labels_similarity_score_source_when_the_row_has_no_stages', async () => {
    // 单路直调行无 stages：分数来源标注为「相似度」。
    mockFetchRetrievals.mockResolvedValue([
      {
        id: 42,
        source: 'tool',
        status: 'success',
        queryText: '套餐',
        traceId: 'trace-a',
        filterSummary: {},
        requestedTopK: null,
        effectiveTopK: null,
        hits: [{ document_id: 'doc-1', score: 0.5 }],
        durationMs: 5,
        createdAt: '2026-01-01T00:00:03Z',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    expect(await screen.findByText('命中 1 条 · 分数来源：相似度')).toBeInTheDocument()
    expect(screen.getByText('0.5000')).toBeInTheDocument()
  })

  it('opens_chunk_detail_popover_with_content_and_superseded_warning', async () => {
    // 点分片 ID 取分片 + 文档：展示原文、序号、锚点、嵌入空间，退役行给
    // 醒目提示。
    mockLoadChunkDetail.mockResolvedValue({
      chunk: {
        index: 3,
        chunkId: 'doc-1',
        anchor: 'chars 64-128',
        content: '分片原文内容',
        embedder: 'bge-m3@v1',
      },
      document: documentDTO({ title: '员工手册' }),
      supersededAt: '2026-02-03T04:05:06Z',
    })
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    await user.click(within(retrievalRow('trace-a')).getAllByRole('button', { name: '分片 doc-1' })[0])
    expect(await screen.findByText('分片原文内容')).toBeInTheDocument()
    expect(mockLoadChunkDetail).toHaveBeenCalledWith('doc-1')
    expect(screen.getByText(/已重摄淘汰，保留期内可反查/)).toBeInTheDocument()
    expect(screen.getByText('bge-m3@v1')).toBeInTheDocument()
    expect(screen.getByText('员工手册')).toBeInTheDocument()
  })

  it('opens_document_only_popover_from_a_unit_source_id', async () => {
    // 点单元文档 ID 只取文档摘要（不请求分片）。
    mockFetchRetrievals.mockResolvedValue([
      {
        id: 43,
        source: 'tool',
        status: 'success',
        queryText: '套餐',
        traceId: 'trace-a',
        filterSummary: {},
        requestedTopK: null,
        effectiveTopK: null,
        hits: [],
        stages: [
          {
            stage: 'pack',
            limit: 4096,
            units: [{ mode: 'document', source_id: '2', document_ids: ['2#0#a1b2c3d4'], tokens: 10, admitted: true }],
            duration_ms: 1,
          },
        ],
        durationMs: 1,
        createdAt: '2026-01-01T00:00:03Z',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    await expandStage(user, 0)
    await user.click(screen.getByRole('button', { name: '文档 2' }))
    expect(await screen.findByText('文档详情')).toBeInTheDocument()
    expect(screen.getByText('员工手册')).toBeInTheDocument()
    expect(mockLoadDocumentDetail).toHaveBeenCalledWith('2')
    expect(mockLoadChunkDetail).not.toHaveBeenCalled()
  })

  it('clears_detail_cache_on_snapshot_refresh_and_thread_switch', async () => {
    // 缓存清空挂点：初次装载、刷新快照与切换会话各清一次。
    mockFetchSessions.mockResolvedValue([
      { id: 'thread-1', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T02:00:00Z' },
      { id: 'thread-2', createdAt: '2026-01-02T00:00:00Z', updatedAt: '2026-01-02T02:00:00Z' },
    ])
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    expect(mockClearReplayDetailCache).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole('button', { name: '刷新快照' }))
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledTimes(2)
    })
    expect(mockClearReplayDetailCache).toHaveBeenCalledTimes(2)

    await user.click(screen.getByRole('button', { name: /会话 thread-2/ }))
    await waitFor(() => {
      expect(mockFetchHistory).toHaveBeenCalledTimes(3)
    })
    expect(mockClearReplayDetailCache).toHaveBeenCalledTimes(3)
  })

  it('shows_a_retry_action_when_chunk_detail_fails_and_recovers_on_retry', async () => {
    // 浮层加载失败给出可读文案与重试；重试命中成功响应后渲染详情。
    mockLoadChunkDetail.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce({
      chunk: {
        index: 0,
        chunkId: 'doc-1',
        anchor: 'chars 0-64',
        content: '重试后的原文',
        embedder: 'space@v1',
      },
      document: documentDTO(),
      supersededAt: '',
    })
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    await user.click(within(retrievalRow('trace-a')).getAllByRole('button', { name: '分片 doc-1' })[0])
    expect(await screen.findByText('加载失败：boom')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('重试后的原文')).toBeInTheDocument()
    expect(mockLoadChunkDetail).toHaveBeenCalledTimes(2)
  })

  it('shows_a_permission_message_for_the_backend_denied_reason', async () => {
    // 后端权限拒绝 reason 为 PERM_ACCESS_DENIED：浮层给友好「无权限」文案，
    // 不透出原始英文 reason。
    mockLoadChunkDetail.mockRejectedValue(new ApiError('permission denied', 'PERM_ACCESS_DENIED'))
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await expandRetrievalRow(user)
    await user.click(within(retrievalRow('trace-a')).getAllByRole('button', { name: '分片 doc-1' })[0])
    expect(await screen.findByText(/无权限查看该内容/)).toBeInTheDocument()
  })

  it('clamps_long_answers_to_five_lines_and_toggles_expansion', async () => {
    mockFetchHistory.mockResolvedValue([
      { kind: 'message', id: 'm-1', role: 'user', content: '问' },
      {
        kind: 'message',
        id: 'm-2',
        role: 'assistant',
        content: '第一行\n第二行\n第三行\n第四行\n第五行\n第六行',
      },
    ])
    const user = userEvent.setup()
    renderReplay()
    const text = await screen.findByTestId('replay-answer-text')

    expect(text.className).toContain('line-clamp-5')
    await user.click(screen.getByRole('button', { name: '展开全文' }))
    expect(text.className).not.toContain('line-clamp-5')
    await user.click(screen.getByRole('button', { name: '收起' }))
    expect(text.className).toContain('line-clamp-5')
  })

  it('copies_the_answer_from_the_hover_action', async () => {
    mockFetchHistory.mockResolvedValue([
      { kind: 'message', id: 'm-1', role: 'user', content: '问' },
      { kind: 'message', id: 'm-2', role: 'assistant', content: 'answer' },
    ])
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      renderReplay()
      const article = (await screen.findByText('answer')).closest('article')
      await user.click(within(article as HTMLElement).getByRole('button', { name: '复制回答' }))
      await waitFor(() => expect(writeText).toHaveBeenCalledWith('answer'))
    } finally {
      restoreClipboard()
    }
  })

  it('copies_tool_arguments_and_results_from_the_expanded_card', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    try {
      renderReplay()
      await screen.findByText('answer')
      await user.click(screen.getByText(/工具 search_internal_docs/))
      const card = screen.getByText(/工具 search_internal_docs/).closest('[aria-label]')
      await user.click(within(card as HTMLElement).getByRole('button', { name: '复制参数' }))
      await user.click(within(card as HTMLElement).getByRole('button', { name: '复制结果' }))
      await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2))
      expect(writeText).toHaveBeenNthCalledWith(1, '{"query":"grape"}')
      expect(writeText).toHaveBeenNthCalledWith(2, '{"document_id":"doc-1","content":"excerpt"}')
    } finally {
      restoreClipboard()
    }
  })

  it('does_not_expand_the_row_when_the_trace_button_is_clicked', async () => {
    // 追踪按钮与展开触发器平级：点追踪只切链路，不触发展开。
    const user = userEvent.setup()
    renderReplay()
    await screen.findByText('answer')
    await user.click(within(retrievalRow('trace-a')).getByRole('button', { name: '追踪 trace-a' }))
    expect(screen.queryByRole('list', { name: '检索阶段' })).not.toBeInTheDocument()
    await waitFor(() => {
      expect(retrievalRow('trace-a')).toHaveClass('bg-brand-soft')
    })
  })
})
