import { describe, expect, it } from 'vitest'
import { detectCompactionRefs } from './compaction'

// 压缩占位符识别的形状与 trpc-agent-go context_compact.go 的字面产物一
// 一对应；畸形输入不抛错。

const ELIDED_WITH_REFS = [
  '[elided] Previous tool call succeeded and its result was already consumed by the assistant;',
  'payload has been dropped to save context.',
  'event_id: 3f2b9a58-6f1e-4a2c-9f1a-2b1c0d9e8f7a',
  'tool_call_id: call-1',
  'tool_name: echo',
  'reason: history_compaction',
].join('\n')

describe('detectCompactionRefs placeholder shapes', () => {
  it('detects_the_recoverable_placeholder_and_extracts_all_ref_lines', () => {
    const refs = detectCompactionRefs(`prefix text\n${ELIDED_WITH_REFS}\nsuffix`)
    expect(refs).toHaveLength(1)
    expect(refs[0]).toEqual({
      kind: 'placeholder',
      eventId: '3f2b9a58-6f1e-4a2c-9f1a-2b1c0d9e8f7a',
      toolCallId: 'call-1',
      toolName: 'echo',
      reason: 'history_compaction',
    })
  })

  it('detects_the_truncation_marker_with_inline_refs', () => {
    const text =
      'head kept\n\n[... 512 characters truncated from tool result; event_id=evt-42; tool_call_id=call-7; tool_name=search_internal_docs; use session_load ...]\n\ntail kept'
    const refs = detectCompactionRefs(text)
    expect(refs).toHaveLength(1)
    expect(refs[0]).toEqual({
      kind: 'truncated',
      eventId: 'evt-42',
      toolCallId: 'call-7',
      toolName: 'search_internal_docs',
    })
  })

  it('detects_the_compact_truncation_marker_without_char_count', () => {
    const refs = detectCompactionRefs('head[... event_id=evt-9; tool_call_id=call-2; session_load]tail')
    expect(refs).toEqual([
      { kind: 'truncated', eventId: 'evt-9', toolCallId: 'call-2', toolName: undefined },
    ])
  })

  it('marks_the_bare_truncation_marker_without_refs_as_unrecoverable', () => {
    const refs = detectCompactionRefs('head\n\n[... 512 characters truncated ...]\n\ntail')
    expect(refs).toEqual([{ kind: 'truncated', eventId: undefined, toolCallId: undefined, toolName: undefined }])
  })

  it('does_not_flag_plain_bracket_ellipsis_prose_as_truncation', () => {
    expect(detectCompactionRefs('the list is long [... see the appendix] for details')).toEqual([])
  })

  it('marks_the_bare_elided_block_without_refs_as_unrecoverable', () => {
    const text = '[elided] Previous tool call succeeded and its result was already consumed by the assistant;'
    const refs = detectCompactionRefs(text)
    expect(refs).toEqual([{ kind: 'elided' }])
  })

  it('marks_the_policy_placeholder_as_unrecoverable', () => {
    const refs = detectCompactionRefs('Tool result omitted by context compaction policy.')
    expect(refs).toEqual([{ kind: 'policy' }])
  })

  it('does_not_flag_event_id_like_text_outside_placeholder_blocks', () => {
    const refs = detectCompactionRefs('the record stores event_id: 12345 for auditing')
    expect(refs).toEqual([])
  })

  it('deduplicates_identical_refs_across_repeated_placeholders', () => {
    const refs = detectCompactionRefs(`${ELIDED_WITH_REFS}\n\n${ELIDED_WITH_REFS}`)
    expect(refs).toHaveLength(1)
  })

  it('separates_multiple_distinct_placeholders_in_one_text', () => {
    const text = `${ELIDED_WITH_REFS}\nmid\n[... 8 characters truncated from tool result; event_id=evt-bb; session_load]`
    const refs = detectCompactionRefs(text)
    expect(refs.map((r) => r.eventId)).toEqual(['3f2b9a58-6f1e-4a2c-9f1a-2b1c0d9e8f7a', 'evt-bb'])
  })
})

describe('detectCompactionRefs malformed input safety', () => {
  it('returns_an_empty_array_for_non_string_input', () => {
    expect(detectCompactionRefs(undefined)).toEqual([])
    expect(detectCompactionRefs(null)).toEqual([])
    expect(detectCompactionRefs(42)).toEqual([])
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect(detectCompactionRefs({ evil: '<script>' } as any)).toEqual([])
  })

  it('returns_an_empty_array_for_empty_string', () => {
    expect(detectCompactionRefs('')).toEqual([])
  })

  it('returns_an_empty_array_for_plain_tool_result_text', () => {
    expect(detectCompactionRefs('{"status":"ok","text":"hello"}')).toEqual([])
  })
})
