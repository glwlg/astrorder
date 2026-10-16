import { beforeEach, expect, it } from 'vitest'
import { mergeHistoryPages } from './useAstrorderData'
import { selectMessages, useAstrorderStore } from '../state/store'
import { visibleTranscript } from '../domain/visibleTranscript'
import type { Message } from '../domain/types'
const row = (id: string, turn_id: string): Message & { turn_id: string } => ({ id, turn_id, agent_id: 'a', session_id: 's', role: 'assistant', kind: 'message', text: id, attachments: [], tool: null, command_id: null, created_at: '1970-01-01T00:00:00Z' })
beforeEach(() => useAstrorderStore.getState().resetRuntime())
it('keeps a completed live body when an earlier history request returns', () => {
  const old = row('reply', 'turn')
  useAstrorderStore.getState().mergeMessages('a', 's', [old])
  const before = useAstrorderStore.getState().messageRevision
  useAstrorderStore.getState().applyEvent({ id: 'final', cursor: 0, type: 'message.upsert', agent_id: 'a', session_id: 's', data: { ...old, text: 'complete final body' } })
  mergeHistoryPages('a', 's', [{ items: [old], liveRevisionAtRequest: before }])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('complete final body')
  const timed = { ...old, created_at: '2026-10-16T02:09:24.173Z', timestamp_source: 'native' as const }
  mergeHistoryPages('a', 's', [{ items: [timed], liveRevisionAtRequest: before }])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0]).toMatchObject({ text: 'complete final body', created_at: timed.created_at, timestamp_source: 'native' })
  mergeHistoryPages('a', 's', [{ items: [{ ...old, text: 'native final' }], liveRevisionAtRequest: 12 }])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('native final')
})
it('places a delayed previous-turn message before the next user turn', () => {
  const first = { ...row('first-user', 'first'), role: 'user' as const }
  const next = { ...row('next-user', 'next'), role: 'user' as const }
  const reply = row('next-reply', 'next')
  const delayed = { ...row('old-intro', 'first'), created_at: '2026-10-15T06:00:00Z' }
  expect(visibleTranscript([first, next, reply, delayed], [first, next, reply], new Set()).map(m => m.id)).toEqual(['first-user', 'old-intro', 'next-user', 'next-reply'])
})
