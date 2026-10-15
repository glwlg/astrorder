import { expect, it } from 'vitest'
import { mergeMessagesById } from './semantics'
import { visibleTranscript } from './visibleTranscript'
import type { Message } from './types'
const m = (id: string, role: Message['role'], seconds: number, command_id: string | null = null): Message => ({ id, role, text: '你好', command_id, kind: 'message', session_id: 's', agent_id: 'a', attachments: [], tool: null, created_at: `2026-09-09T02:56:${String(seconds).padStart(2, '0')}.000Z` })
it('retires earlier local inputs when native history arrives while keeping later guidance', () => {
  const first = m('optimistic-first', 'user', 10, 'first')
  const second = m('optimistic-second', 'user', 14, 'second')
  expect(mergeMessagesById([first, second], [m('server-first', 'user', 12)]).map(x => x.id)).toEqual(['optimistic-second', 'server-first'])
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

it.each(['codex', 'hermes', 'grok'])('keeps %s user input when only its reply has reached native history', agent_id => {
  const optimistic = { ...m('optimistic-send', 'user', 10, 'send'), agent_id }
  const reply = { ...m('reply', 'assistant', 20, 'send'), agent_id }
  expect(visibleTranscript([optimistic, reply], [reply], new Set([optimistic.id])).map(x => x.id)).toEqual([optimistic.id, reply.id])
  expect(visibleTranscript([optimistic, reply], [{ ...reply, command_id: null }], new Set()).map(x => x.id)).toEqual([optimistic.id, reply.id])
})

it('does not replace a pending send with an older identical user message', () => {
  const optimistic = m('optimistic-next', 'user', 20, 'next')
  const older = m('old-user', 'user', 10)
  expect(mergeMessagesById([optimistic], [older]).map(x => x.id)).toEqual([optimistic.id, older.id])
})

it('retires local input without comparing command ids when native user history arrives', () => {
  const optimistic = m('optimistic-next', 'user', 20, 'next')
  const other = m('other-user', 'user', 21, 'other')
  expect(mergeMessagesById([optimistic], [other]).map(x => x.id)).toEqual([other.id])
  expect(visibleTranscript([optimistic, other], [other], new Set()).map(x => x.id)).toEqual([other.id])
})

it('keeps attachment-only input until the native user row includes its attachment', () => {
  const attachment: Message['attachments'][number] = { id: 'image', name: 'image.png', media_type: 'image/png', url: '/image.png' }
  const optimistic: Message = { ...m('optimistic-image', 'user', 10, 'image'), text: '', attachments: [attachment] }
  const empty = { ...m('native-image', 'user', 11, 'image'), text: '' }
  expect(mergeMessagesById([optimistic], [empty]).map(x => x.id)).toEqual([optimistic.id, empty.id])
  expect(visibleTranscript([optimistic, empty], [empty], new Set()).map(x => x.id)).toEqual([optimistic.id, empty.id])
  const complete = { ...empty, attachments: [attachment] }
  expect(mergeMessagesById([optimistic], [complete]).map(x => x.id)).toEqual([complete.id])
  expect(mergeMessagesById([optimistic, empty], [complete]).map(x => x.id)).toEqual([complete.id])
})

it('keeps pending input when an incomplete native row arrived first', () => {
  const optimistic = m('optimistic-send', 'user', 10, 'send')
  const empty = { ...m('native-user', 'user', 11, 'send'), text: '' }
  expect(mergeMessagesById([empty], [optimistic]).map(x => x.id)).toEqual([empty.id, optimistic.id])
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

it('retires temporary input when Hermes adds image markers without a command id', () => {
  const input: Message = { ...m('optimistic-image', 'user', 10, 'send'), text: '请看图片', attachments: [{ id: 'upload', name: 'image.png', media_type: 'image/png', url: '/image.png' }] }
  const native: Message = { ...m('native-image', 'user', 11), text: '请看图片\n\n[Image attached at: C:\\images\\upload.png]\n[screenshot]', created_at: '2026-09-09 02:56:11.000000' }
  expect(mergeMessagesById([input], [native])).toEqual([native])
  expect(mergeMessagesById([native], [input])).toEqual([native])
  expect(visibleTranscript([input, native], [native], new Set())).toEqual([native])
})

it('keeps native user history entries separate even if they share a command id', () => {
  const first = m('native-first', 'user', 10, 'send')
  const second = m('native-second', 'user', 11, 'send')
  expect(mergeMessagesById([first], [second])).toEqual([first, second])
})
