// 压缩占位符识别（纯函数，A3 复现视图）：在回放文本中识别上下文压缩
// 产生的占位符形状，提取 event_id 恢复引用供「占位 ↔ 原文」对照。
//
// 占位符形状与 trpc-agent-go internal/flow/processor/context_compact.go
// 一致（B2 集成测试同样按这些形状断言）：
//  1. 整体替换占位符（Pass 0/1 可恢复）：`[elided] Previous tool call
//     succeeded …` 头部 + 尾部行式恢复引用（`event_id: <id>` /
//     `tool_call_id: <id>` / `tool_name: <name>` / `reason: <reason>`）；
//  2. 超大截断标记（Pass 2）：`[... N characters truncated from
//     tool result; event_id=X; tool_call_id=Y; ...]` 方括号内联引用（含
//     无字符数说明的紧凑变体 `[... event_id=X; tool_call_id=Y; session_load]`）；
//     恢复引用缺失时框架退化为裸标记 `[... N characters truncated ...]`
//     （不可恢复，只标注）；
//  3. 不可恢复占位符：无引用行的 elided 块与 `Tool result omitted by
//     context compaction policy.`（只能标注、无法反查原文）。
//
// 会话事件流保留全部原文（压缩只作用于请求投影副本），占位符只会随
// 模型回复被引用或出现在模型可见投影的回显里；识别以形状特征为准，
// 不假设必然出现。

export type CompactionRefKind = 'placeholder' | 'truncated' | 'elided' | 'policy'

// CompactionRef 是一个被识别出的压缩占位符引用：eventId 存在时可通过
// 事件窗口端点取回原文；缺失时只能标注「已压缩」。
export interface CompactionRef {
  kind: CompactionRefKind
  eventId?: string
  toolCallId?: string
  toolName?: string
  reason?: string
}

// 与框架 processor 的字面形状一字对齐（前缀匹配即可，instruction 尾部
// 随 SessionLoadRecoveryEnabled 变化）。
const ELIDED_PREFIX = '[elided] Previous tool call succeeded'
const POLICY_PLACEHOLDER = 'Tool result omitted by context compaction policy.'

// 行式恢复引用：占位符尾部的 `event_id: <id>` 等行（值取到行尾）。
const REF_LINE = /^(event_id|tool_call_id|tool_name|reason):[ \t]*(.+)$/gm

// 方括号截断标记：`[... ...]`（ASCII 三点，宽松收尾——冗长变体以
// `...]` 结束、紧凑变体以 `]` 结束），内含 `event_id=<id>` 内联引用。
// 标记内部不含方括号（标记由框架拼装，不嵌套）。
const TRUNCATION_MARKER = /\[\.\.\.[^[\]]*\]/g
// 内联引用键：冗长变体的键由 `; ` 引导，紧凑变体的 event_id 直接跟在
// `[... ` 之后。
const MARKER_EVENT_ID = /(?:^|;[ \t]*|\[\.\.\.[ \t]*)event_id=([^;\]]+)/
const MARKER_TOOL_CALL_ID = /(?:^|;[ \t]*)tool_call_id=([^;\]]+)/
const MARKER_TOOL_NAME = /(?:^|;[ \t]*)tool_name=([^;\]]+)/
// 裸截断标记（恢复引用缺失时框架的退化形态）：字符数说明是形状特征，
// 普通方括号省略文不匹配，避免把行文里的 `[... see docs]` 误报为压缩。
const BARE_TRUNCATION_MARKER = /\[\.\.\.[ \t]*\d+ characters truncated/

// detectCompactionRefs 识别文本中的全部压缩占位符引用：畸形输入（非字
// 符串、空串）返回空数组；同类引用去重。识别约束：行式引用只在 elided
// 块内生效（避免工具结果正文里出现形似文本时误报）。
export function detectCompactionRefs(text: unknown): CompactionRef[] {
  if (typeof text !== 'string' || text === '') {
    return []
  }
  const refs: CompactionRef[] = []
  const seen = new Set<string>()
  const push = (ref: CompactionRef) => {
    const key = ref.kind + '\u0000' + (ref.eventId ?? '') + '\u0000' + (ref.toolCallId ?? '')
    if (seen.has(key)) {
      return
    }
    seen.add(key)
    refs.push(ref)
  }

  // 1) 策略占位符（恒不可恢复）。
  if (text.includes(POLICY_PLACEHOLDER)) {
    push({ kind: 'policy' })
  }

  // 2) elided 整体替换块：逐块扫描（块界 = 下一个 elided 前缀或文本尾），
  // 块内有行式 event_id 引用即可恢复；无引用行标注为不可恢复占位符。
  let offset = text.indexOf(ELIDED_PREFIX)
  while (offset !== -1) {
    const next = text.indexOf(ELIDED_PREFIX, offset + ELIDED_PREFIX.length)
    const block = text.slice(offset, next === -1 ? undefined : next)
    const ref = refsFromRefLines(block)
    if (ref === null) {
      push({ kind: 'elided' })
    } else {
      push({ kind: 'placeholder', ...ref })
    }
    offset = next
  }

  // 3) 方括号截断标记（含紧凑变体）：标记内带 event_id= 即可恢复；缺
  // 引用但形状确为截断标记（裸形态）标注为不可恢复截断。
  for (const match of text.matchAll(TRUNCATION_MARKER)) {
    const marker = match[0]
    const eventId = firstMatch(MARKER_EVENT_ID, marker)
    if (eventId === null) {
      if (BARE_TRUNCATION_MARKER.test(marker)) {
        push({ kind: 'truncated' })
      }
      continue
    }
    push({
      kind: 'truncated',
      eventId: normalizeRefValue(eventId),
      toolCallId: optionalRefValue(MARKER_TOOL_CALL_ID, marker),
      toolName: optionalRefValue(MARKER_TOOL_NAME, marker),
    })
  }
  return refs
}

// refsFromRefLines 从 elided 块内提取行式恢复引用；无 event_id 行返回
// null（不可恢复）。
function refsFromRefLines(block: string): Omit<CompactionRef, 'kind'> | null {
  let eventId: string | null = null
  let toolCallId: string | undefined
  let toolName: string | undefined
  let reason: string | undefined
  for (const match of block.matchAll(REF_LINE)) {
    const name = match[1]
    const value = normalizeRefValue(match[2])
    if (value === '') {
      continue
    }
    if (name === 'event_id') {
      eventId = value
    } else if (name === 'tool_call_id') {
      toolCallId = value
    } else if (name === 'tool_name') {
      toolName = value
    } else {
      reason = value
    }
  }
  if (eventId === null) {
    return null
  }
  return { eventId, toolCallId, toolName, reason }
}

// optionalRefValue 取可选内联引用；缺失返回 undefined。
function optionalRefValue(pattern: RegExp, marker: string): string | undefined {
  const value = firstMatch(pattern, marker)
  return value === null ? undefined : normalizeRefValue(value)
}

function firstMatch(pattern: RegExp, text: string): string | null {
  const match = pattern.exec(text)
  return match === null ? null : match[1]
}

// normalizeRefValue 去引用值首尾空白（行式引用取到行尾，可能带 \r）。
function normalizeRefValue(value: string): string {
  return value.trim()
}
