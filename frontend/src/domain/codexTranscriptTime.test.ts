import { expect, it } from 'vitest'
import type { Message } from './types'
import { visibleTranscript } from './visibleTranscript'
import { useAstrorderStore, selectMessages } from '../state/store'

const row = (id: string, turn_id: string, created_at: string): Message => ({ id, turn_id, created_at, role: 'assistant', kind: 'message', text: id, agent_id: 'a', session_id: 's', command_id: null, attachments: [], tool: null })

it('does not append an unanchored previous-turn reply with a fresh receive time', () => {
 const current = row('current', 'current-turn', '2026-10-16T02:54:17Z')
 const old = row('old', 'previous-turn', '2026-10-16T02:57:00Z')
 expect(visibleTranscript([current, old], [current], new Set()).map(m => m.id)).toEqual(['current'])
})

it('keeps the native start time when live snapshots update an existing item', () => {
 useAstrorderStore.getState().resetRuntime()
 const native = { ...row('reply', 'turn', '2026-10-16T02:09:24.173Z'), timestamp_source: 'native' as const, completed_at: '2026-10-16T02:09:31.278Z' }
 useAstrorderStore.getState().mergeMessages('a', 's', [native])
 useAstrorderStore.getState().applyEvent({ id: 'late-complete', cursor: 0, type: 'message.upsert', agent_id: 'a', session_id: 's', data: { ...row('reply', 'turn', '2026-10-16T02:57:00Z'), text: 'complete' } })
 const result = selectMessages(useAstrorderStore.getState(), 'a', 's')[0]
 expect(result.text).toBe('complete')
 expect(result.created_at).toBe(native.created_at)
 expect(result).toMatchObject({ timestamp_source: 'native', completed_at: native.completed_at })
})

it('inserts a delayed same-turn item by native start time without reordering native tools', () => {
 const intro = { ...row('intro', 'turn', '2026-10-16T02:54:17Z'), timestamp_source: 'native' as const }
 const progress = { ...row('progress', 'turn', '2026-10-16T02:55:42Z'), timestamp_source: 'native' as const }
 const delayed = { ...row('delayed', 'turn', '2026-10-16T02:54:43Z'), timestamp_source: 'native' as const }
 expect(visibleTranscript([intro, progress, delayed], [intro, progress], new Set()).map(m => m.id)).toEqual(['intro', 'delayed', 'progress'])
})

it('preserves output from a new turn anchored by its live user message', () => {
 const old = row('old', 'old-turn', '2026-10-16T02:09:24Z')
 const user = { ...row('user', 'new-turn', '2026-10-16T02:54:00Z'), role: 'user' as const }
 const reply = row('reply', 'new-turn', '2026-10-16T02:54:17Z')
 expect(visibleTranscript([old, user, reply], [old], new Set()).map(m => m.id)).toEqual(['old', 'user', 'reply'])
})
