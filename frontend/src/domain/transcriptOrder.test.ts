import { expect, it } from 'vitest'
import type { Message } from './types'
import { visibleTranscript } from './visibleTranscript'

const row = (id: string, role: Message['role'], seconds: number): Message => ({
  id, role, kind: role === 'tool' ? 'tool' : 'message', text: id,
  agent_id: 'a', session_id: 's', attachments: [], command_id: null,
  tool: role === 'tool' ? { name: 'commandExecution', status: 'completed' } : null,
  created_at: `2026-10-15T01:00:${String(seconds).padStart(2, '0')}Z`,
})

it('keeps tools between their native commentary even when completion timestamps are later', () => {
  const native = [row('user', 'user', 0), row('intro', 'assistant', 1), row('read', 'tool', 20), row('update', 'assistant', 3), row('edit', 'tool', 21), row('answer', 'assistant', 5)]
  expect(visibleTranscript(native, native, new Set()).map(m => m.id)).toEqual(native.map(m => m.id))
})
