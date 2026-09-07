import { describe, expect, it } from 'vitest'
import {
  eventIsNew,
  isDraftSendable,
  isNearBottom,
  mergeMessagesById,
  scopeKey,
} from './semantics'

describe('protocol identity semantics', () => {
  it('scopes identical session ids by agent', () => {
    expect(scopeKey('hermes-a', 'session-1')).not.toBe(scopeKey('codex-a', 'session-1'))
  })

  it('keeps two legitimate identical messages when their ids differ', () => {
    const first = { id: 'message-1', text: '继续', created_at: '2026-01-01T00:00:00Z' }
    const second = { id: 'message-2', text: '继续', created_at: '2026-01-01T00:00:01Z' }

    expect(mergeMessagesById([first], [second])).toEqual([first, second])
  })

  it('updates one canonical message only by its stable id', () => {
    const original = { id: 'message-1', text: '流式中', created_at: '2026-01-01T00:00:00Z' }
    const update = { id: 'message-1', text: '已完成', created_at: '2026-01-01T00:00:00Z' }

    expect(mergeMessagesById([original], [update])).toEqual([update])
  })

  it('does not treat a layout-driven scroll as browsing', () => {
    expect(isNearBottom({ scrollTop: 700, clientHeight: 300, scrollHeight: 1000 })).toBe(true)
    expect(isNearBottom({ scrollTop: 620, clientHeight: 300, scrollHeight: 1000 })).toBe(false)
  })

  it('allows image-only drafts but rejects empty drafts', () => {
    expect(isDraftSendable('', [{ id: 'attachment-1' }])).toBe(true)
    expect(isDraftSendable('   ', [])).toBe(false)
  })

  it('uses event ids to ignore duplicate replay frames', () => {
    expect(eventIsNew('event-1', {})).toBe(true)
    expect(eventIsNew('event-1', { 'event-1': true })).toBe(false)
  })
})
