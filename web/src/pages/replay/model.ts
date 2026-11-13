// 复现视图数据组装与 trace_id 关联（纯函数）：把对话回放、工具调
// 用审计、授权确认审计、检索留痕四面证据按 trace_id / toolCallId 关联。
// 全部为不可变纯函数，便于对「任一回答可反查全部召回证据」的验收做单
// 元级断言。

import type { ConfirmationAudit, ToolCallAudit } from '../../api/agentAudit'
import type { RetrievalLog } from '../../api/agentRetrievalLogs'
import type { ChatItem } from '../../agui/events'

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

// matchesTraceId 判定一行证据是否命中当前选中的链路：未选中链路
// （null/空串）时一律未命中——高亮与灰显都以「已选中链路」为前提，未
// 选中时证据面中性呈现。链路选中不改变证据行的可见性：命中行高亮、
// 未命中行灰显，调用方以该谓词区分两种呈现。
export function matchesTraceId<T extends { traceId?: string }>(row: T, traceId: string | null): boolean {
  if (traceId === null || traceId === '') {
    return false
  }
  return row.traceId === traceId
}

// relatedToolCallIds 计算链路在回放侧关联的全部 toolCallId：审计行与授
// 权确认行都携带 toolCallId，live 路径两者 trace 不同，取并集保证桥接召
// 回；未选中链路（null/空串）返回空集。回放工具项与证据行以 toolCallId
// 同值桥接——同一次工具调用在对话流与证据面之间的关联锚点。
export function relatedToolCallIds(facets: TraceFacets, traceId: string | null): Set<string> {
  const ids = new Set<string>()
  if (traceId === null || traceId === '') {
    return ids
  }
  for (const row of facets.toolCalls) {
    if (row.traceId === traceId && row.toolCallId !== '') {
      ids.add(row.toolCallId)
    }
  }
  for (const row of facets.confirmations) {
    if (row.traceId === traceId && row.toolCallId) {
      ids.add(row.toolCallId)
    }
  }
  return ids
}

// relatedChatItemIds 把链路命中扩散为回放流内的相关项集合：工具项直接
// 按 toolCallId 判定；文本消息与思考项自身不带 trace_id，按「一段交互
// （以用户消息为界）内存在命中工具项，则整段相关」的邻近口径归属——一段
// 交互的用户消息、工具调用、思考与助手回答是同一链路的完整叙事。无任何
// 命中时返回空集（调用方据此跳过降透明度，避免整流变灰而无所指）。
export function relatedChatItemIds(items: ChatItem[], relatedToolIds: Set<string>): Set<string> {
  const ids = new Set<string>()
  let segment: string[] = []
  let hit = false
  const flush = () => {
    if (hit) {
      for (const id of segment) {
        ids.add(id)
      }
    }
    segment = []
    hit = false
  }
  for (const item of items) {
    if (item.kind === 'message' && item.role === 'user') {
      flush()
    }
    segment.push(item.kind === 'tool' ? item.toolCallId : item.id)
    if (item.kind === 'tool' && relatedToolIds.has(item.toolCallId)) {
      hit = true
    }
  }
  flush()
  return ids
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

// RetrievalStageUnitReason 是未入包原因的封闭词表：merged 同源收编、
// redundant 保底直回单元冗余、truncated 预算截断；入包单元无原因。
export type RetrievalStageUnitReason = 'merged' | 'redundant' | 'truncated'

// RetrievalStageUnit 是留痕阶段时间线里 pack 单元的前端形态（与后端
// retrievalPackUnit 的 jsonb 键 snake_case 对齐后归一）。
export interface RetrievalStageUnit {
  mode: string
  sourceId?: string
  documentIds: string[]
  headingPath?: string
  anchor?: string
  tokens: number
  admitted: boolean
  // reason 仅在 admitted=false 时由后端写入；未知取值丢弃（形态漂移不
  // 猜测原因，前端按「未入包」中性呈现）。
  reason?: RetrievalStageUnitReason
}

// RetrievalStage 是留痕阶段时间线的单个阶段：数组顺序即执行序（召回 →
// 融合 → 重排 → 回包）。queryText 仅召回阶段承载（记变体文本），model
// 仅重排阶段承载；error 是阶段失败摘要（降级路径照常继续）。
export interface RetrievalStage {
  stage: string
  queryText?: string
  limit?: number
  model?: string
  candidates: RetrievalHit[]
  units: RetrievalStageUnit[]
  error?: string
  durationMs: number
}

// parseRetrievalStageUnits 安全解析 pack 阶段的回包单元：document_ids 逐
// 项取字符串，tokens 非有限数字按 0，admitted 非布尔按 false——损坏单元
// 不抛错，保留可解析的部分事实。
function parseRetrievalStageUnits(raw: unknown): RetrievalStageUnit[] {
  if (!Array.isArray(raw)) {
    return []
  }
  const units: RetrievalStageUnit[] = []
  for (const item of raw) {
    if (typeof item !== 'object' || item === null) {
      continue
    }
    const record = item as Record<string, unknown>
    const ids = Array.isArray(record.document_ids)
      ? record.document_ids.filter((id): id is string => typeof id === 'string' && id !== '')
      : []
    units.push({
      mode: typeof record.mode === 'string' && record.mode !== '' ? record.mode : 'chunk',
      sourceId: typeof record.source_id === 'string' && record.source_id !== '' ? record.source_id : undefined,
      documentIds: ids,
      headingPath: typeof record.heading_path === 'string' && record.heading_path !== '' ? record.heading_path : undefined,
      anchor: typeof record.anchor === 'string' && record.anchor !== '' ? record.anchor : undefined,
      tokens: typeof record.tokens === 'number' && Number.isFinite(record.tokens) ? record.tokens : 0,
      admitted: record.admitted === true,
      reason:
        record.reason === 'merged' || record.reason === 'redundant' || record.reason === 'truncated'
          ? record.reason
          : undefined,
    })
  }
  return units
}

// parseRetrievalStages 安全解析留痕行的 stages JSON（编排层写入的阶段数
// 组）：畸形输入（非数组、stage 键缺失）逐项丢弃而非抛错；单路直调行的
// stages 缺省返回空数组（时间线不渲染，不伪造空阶段）。候选复用 hits 的
// 解析口径（同形态同纪律）。
export function parseRetrievalStages(raw: unknown): RetrievalStage[] {
  if (!Array.isArray(raw)) {
    return []
  }
  const stages: RetrievalStage[] = []
  for (const item of raw) {
    if (typeof item !== 'object' || item === null) {
      continue
    }
    const record = item as Record<string, unknown>
    if (typeof record.stage !== 'string' || record.stage === '') {
      continue
    }
    stages.push({
      stage: record.stage,
      queryText: typeof record.query_text === 'string' && record.query_text !== '' ? record.query_text : undefined,
      limit: typeof record.limit === 'number' && Number.isFinite(record.limit) ? record.limit : undefined,
      model: typeof record.model === 'string' && record.model !== '' ? record.model : undefined,
      candidates: parseRetrievalHits(record.candidates),
      units: parseRetrievalStageUnits(record.units),
      error: typeof record.error === 'string' && record.error !== '' ? record.error : undefined,
      durationMs: typeof record.duration_ms === 'number' && Number.isFinite(record.duration_ms) ? record.duration_ms : 0,
    })
  }
  return stages
}

// retrievalScoreSource 推导留痕 hits 列表的分数来源：rerank 阶段成功
//（无 error）才取重排分——重排失败降级时后端仍追加带 error 的 rerank
// 阶段但保留融合序分数，不能标成重排分；否则有 fusion 阶段取融合分；
// 否则（单路直调无 stages）取相似度。分数按阶段自解释、跨阶段不可比，
// 标注来源避免把 RRF 分读成相似度或把降级分读成重排分。
export function retrievalScoreSource(stages: RetrievalStage[]): string {
  if (stages.some((stage) => stage.stage === 'rerank' && !stage.error)) {
    return '重排分'
  }
  if (stages.some((stage) => stage.stage === 'fusion')) {
    return '融合分'
  }
  return '相似度'
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
