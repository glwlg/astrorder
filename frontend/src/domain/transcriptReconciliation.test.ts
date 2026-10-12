import { expect, it } from 'vitest'
import { mergeMessagesById } from './semantics'
import { visibleTranscript } from './visibleTranscript'
import type { Message } from './types'
const m = (id: string, role: Message['role'], seconds: number, command_id: string | null = null): Message => ({ id, role, text: '你好', command_id, kind: 'message', session_id: 's', agent_id: 'a', attachments: [], tool: null, created_at: `2026-09-09T02:56:${String(seconds).padStart(2, '0')}.000Z` })
it('uses command identity rather than text to replace an optimistic message', () => {
  const first = m('optimistic-first', 'user', 10, 'first')
  const second = m('optimistic-second', 'user', 12, 'second')
  expect(mergeMessagesById([first, second], [m('server-second', 'user', 12, 'second')]).map(x => x.id)).toEqual(['optimistic-first', 'server-second'])
})
it('reconciles native history with a late post-LLM user echo and orders user before reply', () => {
  const user = m('history-user', 'user', 14)
  const reply = m('history-reply', 'assistant', 20)
  const stored = [reply, m('hermes-user-submit', 'user', 13, 'command'), m('hermes-user-post', 'user', 21), user]
  expect(visibleTranscript(stored, [user, reply], new Set()).map(x => x.id)).toEqual(['history-user', 'history-reply'])
})
it('preserves a subsequent identical message not yet covered by the native page', () => {
  const user = m('history-user', 'user', 14), reply = m('history-reply', 'assistant', 20)
  const later = m('optimistic-next', 'user', 22, 'next')
  expect(visibleTranscript([reply, user, later], [user, reply], new Set()).map(x => x.id)).toEqual(['history-user', 'history-reply', 'optimistic-next'])
})
it('collapses an optimistic message when a history message with matching role and text arrives at or after it', () => {
  const optimistic = m('optimistic-cmd123', 'user', 10, 'cmd123')
  const history = m('history-message-sha', 'user', 10, null)
  expect(visibleTranscript([optimistic, history], [history], new Set()).map(x => x.id)).toEqual(['history-message-sha'])
})

it('replaces a live Hermes assistant message with its final native row using the exact command', () => {
  const live = m('hermes-stream-first', 'assistant', 10, 'first')
  const other = m('hermes-stream-second', 'assistant', 12, 'second')
  const final = m('history-message-final', 'assistant', 14, 'first')
  expect(mergeMessagesById([live, other], [final]).map(x => x.id)).toEqual(['history-message-final', 'hermes-stream-second'])
  expect(mergeMessagesById([final], [live]).map(x => x.id)).toEqual(['history-message-final'])
})

it('replaces an optimistic user message with an incoming server user message matching text without command_id', () => {
  const optimistic = m('optimistic-cmd999', 'user', 10, 'cmd999')
  const incomingServerUser = m('item-codex-user-1', 'user', 15, null)
  expect(mergeMessagesById([optimistic], [incomingServerUser]).map(x => x.id)).toEqual(['item-codex-user-1'])
})
