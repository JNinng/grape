import { describe, expect, it } from 'vitest'
import type { ToolCallItem } from '../../agui/events'
import type { ConfirmationAudit, ToolCallAudit } from '../../api/agentAudit'
import type { RetrievalLog } from '../../api/agentRetrievalLogs'
import {
  collectTraceIds,
  confirmationsForToolCall,
  filterByTraceId,
  highlightedToolCallIds,
  parseRetrievalHits,
  replayItemMatchesHighlight,
  toolCallAuditsFor,
} from './model'

// 四联视图的数据组装与 trace_id 关联逻辑：验收「任一回答可反查全部召回
// 证据」在此做单元级断言。

function toolCallRow(id: number, toolCallId: string, traceId?: string): ToolCallAudit {
  return {
    id,
    toolCallId,
    toolName: 'echo',
    sessionId: 'sess-1',
    traceId,
    retryCount: 0,
    durationMs: 12,
    status: 'success',
    calledAt: '2026-01-01T00:00:0' + id + 'Z',
  }
}

function confirmationRow(id: number, toolCallId: string, traceId?: string): ConfirmationAudit {
  return {
    id,
    confirmationId: 'conf-' + id,
    sessionId: 'sess-1',
    toolCallId,
    toolName: 'update_my_nickname',
    decision: 'approved',
    path: 'live',
    decidedBy: 't-1:7',
    traceId,
    decidedAt: '2026-01-01T00:00:0' + id + 'Z',
  }
}

function retrievalRow(id: number, traceId?: string): RetrievalLog {
  return {
    id,
    source: 'tool',
    status: 'success',
    queryText: 'grape',
    sessionId: 'sess-1',
    traceId,
    requestedTopK: 5,
    effectiveTopK: 5,
    filterSummary: {},
    hits: [{ document_id: 'doc-' + id, score: 0.9 }],
    durationMs: 30,
    createdAt: '2026-01-01T00:00:0' + id + 'Z',
  }
}

function toolItem(toolCallId: string, result: string | null): ToolCallItem {
  return { kind: 'tool', toolCallId, name: 'echo', argsText: '', result, done: true }
}

describe('collectTraceIds', () => {
  it('collects_unique_trace_ids_across_three_facets_ordered_by_frequency', () => {
    const ids = collectTraceIds({
      toolCalls: [toolCallRow(1, 'c-1', 'trace-a'), toolCallRow(2, 'c-2', 'trace-a'), toolCallRow(3, 'c-3', 'trace-b')],
      confirmations: [confirmationRow(1, 'c-2', 'trace-a')],
      retrievals: [retrievalRow(1, 'trace-c'), retrievalRow(2, 'trace-b')],
    })
    expect(ids).toEqual(['trace-a', 'trace-b', 'trace-c'])
  })

  it('ignores_rows_without_trace_id_and_deduplicates', () => {
    const ids = collectTraceIds({
      toolCalls: [toolCallRow(1, 'c-1'), toolCallRow(2, 'c-2', 'trace-a')],
      confirmations: [],
      retrievals: [retrievalRow(1, 'trace-a')],
    })
    expect(ids).toEqual(['trace-a'])
  })
})

describe('filterByTraceId', () => {
  const rows = [toolCallRow(1, 'c-1', 'trace-a'), toolCallRow(2, 'c-2', 'trace-b'), toolCallRow(3, 'c-3')]

  it('returns_all_rows_when_filter_is_null_or_empty', () => {
    expect(filterByTraceId(rows, null)).toHaveLength(3)
    expect(filterByTraceId(rows, '')).toHaveLength(3)
  })

  it('keeps_only_matching_trace_rows_and_drops_untraced_rows', () => {
    const filtered = filterByTraceId(rows, 'trace-a')
    expect(filtered.map((r) => r.id)).toEqual([1])
  })
})

describe('highlightedToolCallIds and replay linking', () => {
  it('maps_a_trace_to_the_replay_tool_items_via_tool_call_id', () => {
    const audits = [toolCallRow(1, 'call-1', 'trace-a'), toolCallRow(2, 'call-2', 'trace-b')]
    const highlight = highlightedToolCallIds(audits, 'trace-a')
    expect(highlight).toEqual(new Set(['call-1']))
    expect(replayItemMatchesHighlight(toolItem('call-1', 'ok'), highlight)).toBe(true)
    expect(replayItemMatchesHighlight(toolItem('call-2', 'ok'), highlight)).toBe(false)
  })

  it('returns_an_empty_highlight_without_a_selected_trace', () => {
    expect(highlightedToolCallIds([toolCallRow(1, 'call-1', 'trace-a')], null)).toEqual(new Set())
  })

  it('links_audit_and_confirmation_rows_to_a_replay_tool_call', () => {
    const audits = [toolCallRow(1, 'call-1', 'trace-a'), toolCallRow(2, 'call-1', 'trace-b'), toolCallRow(3, 'call-2')]
    const confirmations = [confirmationRow(1, 'call-1', 'trace-a')]
    expect(toolCallAuditsFor(audits, 'call-1').map((r) => r.id)).toEqual([1, 2])
    expect(confirmationsForToolCall(confirmations, 'call-1')).toHaveLength(1)
    expect(confirmationsForToolCall(confirmations, 'call-x')).toHaveLength(0)
  })
})

describe('parseRetrievalHits', () => {
  it('parses_snake_case_hit_records_into_view_rows', () => {
    const hits = parseRetrievalHits([
      { document_id: 'doc-1', score: 0.87, anchor: 'chars 0-64' },
      { document_id: 'doc-2', score: 0.5 },
    ])
    expect(hits).toEqual([
      { documentId: 'doc-1', score: 0.87, anchor: 'chars 0-64' },
      { documentId: 'doc-2', score: 0.5, anchor: undefined },
    ])
  })

  it('drops_malformed_hit_records_without_throwing', () => {
    const hits = parseRetrievalHits([
      null,
      'text',
      { score: 0.5 },
      { document_id: '', score: 0.5 },
      { document_id: 'doc-3' },
      { document_id: 'doc-4', score: Number.NaN },
      { document_id: 'doc-5', score: 0.9 },
    ])
    expect(hits).toEqual([{ documentId: 'doc-5', score: 0.9, anchor: undefined }])
  })

  it('returns_an_empty_array_for_non_array_payloads', () => {
    expect(parseRetrievalHits(undefined)).toEqual([])
    expect(parseRetrievalHits(null)).toEqual([])
    expect(parseRetrievalHits({ document_id: 'doc-1' })).toEqual([])
  })
})

describe('trace evidence assembly', () => {
  it('chains_one_answer_to_all_retrieval_evidence_via_a_single_trace', () => {
    // 验收口径：一次回答的 trace_id 能同时召回它的工具调用、授权确认与
    // 全部检索留痕。
    const toolCalls = [toolCallRow(1, 'call-search', 'trace-answer'), toolCallRow(2, 'call-echo', 'trace-other')]
    const confirmations = [confirmationRow(1, 'call-update', 'trace-answer')]
    const retrievals = [retrievalRow(1, 'trace-answer'), retrievalRow(2, 'trace-answer'), retrievalRow(3, 'trace-other')]

    expect(filterByTraceId(toolCalls, 'trace-answer').map((r) => r.toolCallId)).toEqual(['call-search'])
    expect(filterByTraceId(confirmations, 'trace-answer')).toHaveLength(1)
    expect(filterByTraceId(retrievals, 'trace-answer')).toHaveLength(2)
    expect(highlightedToolCallIds(toolCalls, 'trace-answer')).toEqual(new Set(['call-search']))
  })

  // 对话回放本身不带 trace_id（框架 track 事件无链路事实），它靠
  // toolCallId 桥接高亮；过滤函数只作用于三证据面（上行断言），结构上
  // 不存在触碰回放 items 的路径，故无「过滤改写回放」的可测行为。
})
