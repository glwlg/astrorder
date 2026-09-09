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
