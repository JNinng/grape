import { describe, expect, it } from 'vitest'
import type { Event } from '@ag-ui/client'
import {
  applyChatEvent,
  contentToText,
  initialChatState,
  isToolConfirmationPayload,
  messagesToItems,
} from './events'

function textEvent(partial: {
  type: 'TEXT_MESSAGE_START' | 'TEXT_MESSAGE_CONTENT' | 'TEXT_MESSAGE_END'
  messageId: string
  delta?: string
  role?: 'user' | 'assistant'
}): Event {
  return { ...partial, role: partial.role } as unknown as Event
}

describe('applyChatEvent text message accumulation', () => {
  it('accumulates_text_deltas_into_a_single_assistant_message', () => {
    let state = initialChatState()
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_START', messageId: 'm-1' }))
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_CONTENT', messageId: 'm-1', delta: 'Hello' }))
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_CONTENT', messageId: 'm-1', delta: ' world' }))
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_END', messageId: 'm-1' }))

    expect(state.items).toHaveLength(1)
    const item = state.items[0]
    expect(item.kind).toBe('message')
    expect(item).toMatchObject({ id: 'm-1', role: 'assistant', content: 'Hello world' })
  })

  it('ignores_duplicate_start_events_for_the_same_message_id', () => {
    let state = initialChatState()
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_START', messageId: 'm-1' }))
    state = applyChatEvent(state, textEvent({ type: 'TEXT_MESSAGE_START', messageId: 'm-1' }))

    expect(state.items).toHaveLength(1)
  })
})

describe('applyChatEvent tool call lifecycle', () => {
  it('opens_accumulates_closes_and_attaches_result_for_a_tool_call', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'TOOL_CALL_START', toolCallId: 'call-1', toolCallName: 'echo' } as unknown as Event)
    state = applyChatEvent(state, { type: 'TOOL_CALL_ARGS', toolCallId: 'call-1', delta: '{"text":' } as unknown as Event)
    state = applyChatEvent(state, { type: 'TOOL_CALL_ARGS', toolCallId: 'call-1', delta: '"ping"}' } as unknown as Event)
    state = applyChatEvent(state, { type: 'TOOL_CALL_END', toolCallId: 'call-1' } as unknown as Event)

    const closed = state.items[0]
    expect(closed).toMatchObject({ kind: 'tool', toolCallId: 'call-1', name: 'echo', argsText: '{"text":"ping"}', result: null })
    expect(closed.kind === 'tool' && closed.done).toBe(true)

    state = applyChatEvent(state, { type: 'TOOL_CALL_RESULT', toolCallId: 'call-1', messageId: 'm-9', content: 'ping' } as unknown as Event)
    const withResult = state.items[0]
    expect(withResult.kind === 'tool' && withResult.result).toBe('ping')
  })

  it('appends_an_orphan_tool_item_when_result_arrives_without_start', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'TOOL_CALL_RESULT', toolCallId: 'call-x', messageId: 'm-9', content: 'late result' } as unknown as Event)

    expect(state.items).toHaveLength(1)
    expect(state.items[0]).toMatchObject({ kind: 'tool', toolCallId: 'call-x', result: 'late result', done: true })
  })
})

describe('applyChatEvent run lifecycle', () => {
  it('marks_running_and_clears_error_on_start_then_records_error_on_run_error', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'RUN_ERROR', message: 'older failure' } as unknown as Event)
    expect(state).toMatchObject({ running: false, error: 'older failure' })

    state = applyChatEvent(state, { type: 'RUN_STARTED', threadId: 't', runId: 'r' } as unknown as Event)
    expect(state).toMatchObject({ running: true, error: null })

    state = applyChatEvent(state, { type: 'RUN_ERROR', message: 'model exploded' } as unknown as Event)
    expect(state).toMatchObject({ running: false, error: 'model exploded' })
  })

  it('marks_not_running_on_run_finished', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'RUN_STARTED', threadId: 't', runId: 'r' } as unknown as Event)
    state = applyChatEvent(state, { type: 'RUN_FINISHED', threadId: 't', runId: 'r' } as unknown as Event)

    expect(state.running).toBe(false)
    expect(state.error).toBeNull()
  })

  it('toggles_step_finished_flag_for_the_latest_matching_started_step', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'STEP_STARTED', stepName: 'plan' } as unknown as Event)
    state = applyChatEvent(state, { type: 'STEP_STARTED', stepName: 'act' } as unknown as Event)
    state = applyChatEvent(state, { type: 'STEP_FINISHED', stepName: 'act' } as unknown as Event)

    expect(state.steps).toEqual([
      { name: 'plan', finished: false },
      { name: 'act', finished: true },
    ])
  })
})

describe('applyChatEvent tool_confirmation custom events', () => {
  const pendingPayload = {
    confirmationId: 'c-1',
    toolCallId: 'call-1',
    toolName: 'create_order',
    sessionId: 'thread-1',
    status: 'pending',
    createdAt: 1_700_000_000_000,
  }

  it('adds_pending_confirmation_and_dedupes_by_confirmation_id', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'CUSTOM', name: 'tool_confirmation', value: pendingPayload } as unknown as Event)
    state = applyChatEvent(state, {
      type: 'CUSTOM',
      name: 'tool_confirmation',
      value: { ...pendingPayload, toolName: 'renamed' },
    } as unknown as Event)

    expect(state.confirmations).toHaveLength(1)
    expect(state.confirmations[0].toolName).toBe('renamed')
  })

  it('removes_the_confirmation_when_a_terminal_status_event_arrives', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'CUSTOM', name: 'tool_confirmation', value: pendingPayload } as unknown as Event)
    state = applyChatEvent(state, {
      type: 'CUSTOM',
      name: 'tool_confirmation',
      value: { ...pendingPayload, status: 'denied' },
    } as unknown as Event)

    expect(state.confirmations).toHaveLength(0)
  })

  it('ignores_custom_events_with_other_names_or_malformed_payloads', () => {
    let state = initialChatState()
    state = applyChatEvent(state, { type: 'CUSTOM', name: 'other_event', value: pendingPayload } as unknown as Event)
    state = applyChatEvent(state, { type: 'CUSTOM', name: 'tool_confirmation', value: { broken: true } } as unknown as Event)

    expect(state.confirmations).toHaveLength(0)
  })

  it('does_not_treat_arbitrary_objects_as_confirmations', () => {
    expect(isToolConfirmationPayload(null)).toBe(false)
    expect(isToolConfirmationPayload({ confirmationId: 'c-1' })).toBe(false)
    expect(isToolConfirmationPayload({ ...pendingPayload, status: 'unknown' })).toBe(false)
    expect(isToolConfirmationPayload(pendingPayload)).toBe(true)
  })
})

describe('contentToText', () => {
  it('normalizes_strings_parts_and_missing_content_to_plain_text', () => {
    expect(contentToText('plain')).toBe('plain')
    expect(contentToText(undefined)).toBe('')
    expect(contentToText(null)).toBe('')
    expect(
      contentToText([
        { type: 'text', text: 'a' },
        { type: 'image', url: 'x' } as never,
        { type: 'text', text: 'b' },
      ]),
    ).toBe('a[image]b')
  })
})

describe('messagesToItems history snapshot mapping', () => {
  it('maps_user_assistant_and_tool_messages_with_result_merge', () => {
    const items = messagesToItems([
      { id: 'u-1', role: 'user', content: 'ping please' },
      {
        id: 'a-1',
        role: 'assistant',
        content: 'calling echo',
        toolCalls: [{ id: 'call-1', type: 'function', function: { name: 'echo', arguments: '{"text":"ping"}' } }],
      },
      { id: 't-1', role: 'tool', content: 'ping', toolCallId: 'call-1' },
    ] as never[])

    expect(items).toEqual([
      { kind: 'message', id: 'u-1', role: 'user', content: 'ping please' },
      { kind: 'message', id: 'a-1', role: 'assistant', content: 'calling echo' },
      { kind: 'tool', toolCallId: 'call-1', name: 'echo', argsText: '{"text":"ping"}', result: 'ping', done: true },
    ])
  })

  it('keeps_an_orphan_tool_result_as_its_own_item', () => {
    const items = messagesToItems([{ id: 't-9', role: 'tool', content: 'orphan', toolCallId: 'call-9' } as never])

    expect(items).toEqual([
      { kind: 'tool', toolCallId: 'call-9', name: '', argsText: '', result: 'orphan', done: true },
    ])
  })
})
