// 复现视图数据组装与 trace_id 关联（纯函数，A3）：把对话回放、工具调
// 用审计、授权确认审计、检索留痕四面证据按 trace_id / toolCallId 关联。
// 全部为不可变纯函数，便于对「任一回答可反查全部召回证据」的验收做单
// 元级断言。

import type { ConfirmationAudit, ToolCallAudit } from '../../api/agentAudit'
import type { RetrievalLog } from '../../api/agentRetrievalLogs'
import type { ToolCallItem } from '../../agui/events'

// TraceFacets 是带链路事实的三个证据面（对话回放自身不带 trace_id，其
// 与证据面的关联经 toolCallId 桥接）。
export interface TraceFacets {
  toolCalls: ToolCallAudit[]
  confirmations: ConfirmationAudit[]
  retrievals: RetrievalLog[]
}

// collectTraceIds 汇总三个证据面的全部非空 trace_id：按出现次数降序、
// 同频按字典序，运维侧同一次交互的证据簇排在一起。
export function collectTraceIds(facets: TraceFacets): string[] {
  const counts = new Map<string, number>()
  const bump = (traceId: string | undefined) => {
    if (typeof traceId !== 'string' || traceId === '') {
      return
    }
    counts.set(traceId, (counts.get(traceId) ?? 0) + 1)
  }
  for (const row of facets.toolCalls) {
    bump(row.traceId)
  }
  for (const row of facets.confirmations) {
    bump(row.traceId)
  }
  for (const row of facets.retrievals) {
    bump(row.traceId)
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))
    .map(([traceId]) => traceId)
}

// filterByTraceId 按链路过滤证据行：null/空串表示不过滤（原样返回），
// 非 null 时只保留同链路行（无 trace_id 的行被滤除——链路未知不冒充）。
export function filterByTraceId<T extends { traceId?: string }>(rows: T[], traceId: string | null): T[] {
  if (traceId === null || traceId === '') {
    return rows
  }
  return rows.filter((row) => row.traceId === traceId)
}

// highlightedToolCallIds 计算回放侧需高亮的工具调用 ID 集合：选中链路
// 时为该链路审计行涉及的全部 toolCallId；未选中（null）返回空集（不高
// 亮）。回放工具项与审计行以 toolCallId 同值桥接——同一次工具调用在
// 对话流与证据面之间的关联锚点。
export function highlightedToolCallIds(toolCalls: ToolCallAudit[], traceId: string | null): Set<string> {
  if (traceId === null || traceId === '') {
    return new Set()
  }
  const ids = new Set<string>()
  for (const row of toolCalls) {
    if (row.traceId === traceId && row.toolCallId !== '') {
      ids.add(row.toolCallId)
    }
  }
  return ids
}

// replayItemMatchesHighlight 判定一个回放工具项是否命中高亮集合。
export function replayItemMatchesHighlight(item: ToolCallItem, highlight: Set<string>): boolean {
  return highlight.has(item.toolCallId)
}

// RetrievalHit 是留痕命中行的前端形态（与 biz.RetrievalHit 的 jsonb 键
// snake_case 对齐后归一）。
export interface RetrievalHit {
  documentId: string
  score: number
  anchor?: string
}

// parseRetrievalHits 安全解析留痕行的 hits JSON：畸形输入（非数组、元
// 素字段缺失/类型不符）逐项丢弃而非抛错——证据面渲染绝不因落库形态漂
// 移中断；documentId 非字符串或 score 非有限数字的元素视为损坏。
export function parseRetrievalHits(raw: unknown): RetrievalHit[] {
  if (!Array.isArray(raw)) {
    return []
  }
  const hits: RetrievalHit[] = []
  for (const item of raw) {
    if (typeof item !== 'object' || item === null) {
      continue
    }
    const record = item as Record<string, unknown>
    if (typeof record.document_id !== 'string' || record.document_id === '') {
      continue
    }
    if (typeof record.score !== 'number' || !Number.isFinite(record.score)) {
      continue
    }
    hits.push({
      documentId: record.document_id,
      score: record.score,
      anchor: typeof record.anchor === 'string' && record.anchor !== '' ? record.anchor : undefined,
    })
  }
  return hits
}

// toolCallAuditsFor 按回放工具调用 ID 取审计行（保持端点返回序）：回放
// 侧点击一次工具调用时，联动高亮它的审计与确认证据。
export function toolCallAuditsFor(toolCalls: ToolCallAudit[], toolCallId: string): ToolCallAudit[] {
  return toolCalls.filter((row) => row.toolCallId === toolCallId)
}

// confirmationsForToolCall 按工具调用 ID 取授权确认行。
export function confirmationsForToolCall(confirmations: ConfirmationAudit[], toolCallId: string): ConfirmationAudit[] {
  return confirmations.filter((row) => row.toolCallId === toolCallId)
}
