/// <reference types="node" />
import { createHash, webcrypto } from 'node:crypto'
import { expect, it, vi } from 'vitest'
import { migrateSessionPins } from './migrateSessionPins'
import { scopeKey } from './semantics'

it('moves legacy pins to native keys without touching project appearance or other sources', async () => {
  vi.stubGlobal('crypto', webcrypto)
  const old = 'history-' + createHash('sha256').update('source\0native').digest('hex').slice(0, 48)
  const values: Record<string, string> = {
    astrorder_pinned_sessions: JSON.stringify({ [scopeKey('agent', old)]: true, other: true }),
    'astrorder:project_appearance': '{"untouched":true}',
  }
  const storage = { getItem: (key: string) => values[key] ?? null, setItem: (key: string, value: string) => { values[key] = value } }
  try {
    await migrateSessionPins([{ id: 'native', source_session_id: 'native', source_id: 'source', agent_id: 'agent', title: '会话', workspace: null, status: 'idle', updated_at: '' }], storage)
    expect(JSON.parse(values.astrorder_pinned_sessions)[scopeKey('agent', 'native')]).toBe(true)
    expect(values['astrorder:project_appearance']).toBe('{"untouched":true}')
    expect(JSON.parse(values.astrorder_pinned_sessions).other).toBe(true)
  } finally { vi.unstubAllGlobals() }
})
