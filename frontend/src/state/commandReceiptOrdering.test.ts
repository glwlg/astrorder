import { expect, it } from 'vitest'
import type { Command } from '../domain/types'
import { selectCommands, useAstrorderStore } from './store'

it('does not regress a completed native event when an HTTP receipt arrives late', () => {
  const command: Command = { id: 'native-command', agent_id: 'codex', session_id: 'native-thread', action: 'send', state: 'completed', text: 'same', attachments: [], created_at: '2026-01-01T00:00:00Z', error: null }
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().mergeCommands([command])
  for (const state of ['received', 'accepted', 'running', 'unknown'] as const) {
    useAstrorderStore.getState().mergeCommands([{ ...command, state }])
    expect(selectCommands(useAstrorderStore.getState(), 'codex', 'native-thread')[0].state).toBe('completed')
  }
})
