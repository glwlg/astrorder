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
it('shows an ahead-of-stream history snapshot immediately and consumes delayed deltas without duplication', () => {
  const reply = { ...row('reply', 'turn'), text: '前' }
  useAstrorderStore.getState().mergeMessages('a', 's', [reply])
  const before = useAstrorderStore.getState().messageRevision
  const delta = (id: string, text: string) => useAstrorderStore.getState().applyEvent({
    id, cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's',
    data: { ...reply, text },
  })
  delta('first', '段')
  mergeHistoryPages('a', 's', [{ items: [{ ...reply, text: '前段完整' }], liveRevisionAtRequest: before }])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前段完整')
  delta('second', '完')
  delta('third', '整和更多')
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前段完整和更多')
})

it('keeps the live text when a newer delta disagrees with the ahead history snapshot', () => {
  const reply = { ...row('reply', 'turn'), text: '前' }
  useAstrorderStore.getState().mergeMessages('a', 's', [reply])
  const before = useAstrorderStore.getState().messageRevision
  const delta = (id: string, text: string) => useAstrorderStore.getState().applyEvent({
    id, cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: { ...reply, text },
  })
  delta('first', '段')
  mergeHistoryPages('a', 's', [{ items: [{ ...reply, text: '前段旧内容' }], liveRevisionAtRequest: before }])
  delta('second', '新内容')
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前段新内容')
})

it('uses an ahead snapshot even if no delta arrived during the history request', () => {
  const reply = { ...row('reply', 'turn'), text: '前' }
  const store = useAstrorderStore.getState()
  store.applyEvent({ id: 'first', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: reply })
  const before = useAstrorderStore.getState().messageRevision
  mergeHistoryPages('a', 's', [{ items: [{ ...reply, text: '前段完整' }], liveRevisionAtRequest: before }])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前段完整')
  store.applyEvent({ id: 'second', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: { ...reply, text: '段完整' } })
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前段完整')
})

it('preserves native history when a queued empty start and its deltas arrive afterward', () => {
  const reply = { ...row('reply', 'turn'), text: '完整回复' }
  const store = useAstrorderStore.getState()
  store.mergeMessages('a', 's', [reply])
  store.applyEvent({ id: 'start', cursor: 0, type: 'message.upsert', agent_id: 'a', session_id: 's', data: { ...reply, text: '' } })
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('完整回复')
  store.applyMessageDeltas([
    { id: 'delta-1', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: { ...reply, text: '完整' } },
    { id: 'delta-2', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: { ...reply, text: '回复' } },
  ])
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('完整回复')
})

it('does not discard a new delta that happens to match the beginning of history', () => {
  const reply = { ...row('reply', 'turn'), text: '字' }
  const store = useAstrorderStore.getState()
  store.mergeMessages('a', 's', [reply])
  store.applyEvent({ id: 'next', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: reply })
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('字字')
})

it('accepts a changed final upsert even while older stream text is catching up', () => {
  const reply = { ...row('reply', 'turn'), text: '前' }
  const store = useAstrorderStore.getState()
  store.applyEvent({ id: 'first', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: reply })
  mergeHistoryPages('a', 's', [{ items: [{ ...reply, text: '前面的旧答案' }], liveRevisionAtRequest: useAstrorderStore.getState().messageRevision }])
  store.applyEvent({ id: 'final', cursor: 0, type: 'message.upsert', agent_id: 'a', session_id: 's', data: { ...reply, text: '前面' } })
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前面')
})
it('does not mark an idle session active while consuming already displayed deltas', () => {
  const reply = { ...row('reply', 'turn'), text: '前' }
  const store = useAstrorderStore.getState()
  const session = { id: 's', agent_id: 'a', title: '测试', status: 'running', workspace: null, updated_at: '2026-10-16T02:00:00Z' }
  store.applyEvent({ id: 'running', cursor: 1, type: 'session.upsert', agent_id: 'a', session_id: 's', data: session })
  store.applyEvent({ id: 'first', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: reply })
  mergeHistoryPages('a', 's', [{ items: [{ ...reply, text: '前后' }], liveRevisionAtRequest: useAstrorderStore.getState().messageRevision }])
  store.applyEvent({ id: 'idle', cursor: 2, type: 'session.upsert', agent_id: 'a', session_id: 's', data: { ...session, status: 'idle' } })
  store.applyEvent({ id: 'late', cursor: 0, type: 'message.delta', agent_id: 'a', session_id: 's', data: { ...reply, text: '后' } })
  expect(useAstrorderStore.getState().liveActivityAt['a::s']).toBeUndefined()
  expect(selectMessages(useAstrorderStore.getState(), 'a', 's')[0].text).toBe('前后')
})
it('places a delayed previous-turn message before the next user turn', () => {
  const first = { ...row('first-user', 'first'), role: 'user' as const }
  const next = { ...row('next-user', 'next'), role: 'user' as const }
  const reply = row('next-reply', 'next')
  const delayed = { ...row('old-intro', 'first'), created_at: '2026-10-15T06:00:00Z' }
  expect(visibleTranscript([first, next, reply, delayed], [first, next, reply], new Set()).map(m => m.id)).toEqual(['first-user', 'old-intro', 'next-user', 'next-reply'])
})
