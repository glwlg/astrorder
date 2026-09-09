import { scopeKey } from './semantics'
import type { Session } from './types'

/** Read obsolete preference keys only; never use aliases as session identities. */
export async function migrateSessionPins(sessions: Session[], storage: Pick<Storage, 'getItem' | 'setItem'>): Promise<void> {
  const raw = storage.getItem('astrorder_pinned_sessions')
  if (!raw) return
  const pins: Record<string, boolean> = JSON.parse(raw)
  if (!pins || typeof pins !== 'object' || Array.isArray(pins)) return
  let changed = false
  for (const session of sessions) {
    if (!session.source_id || session.id !== session.source_session_id) continue
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(`${session.source_id}\0${session.id}`))
    const oldId = 'history-' + Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('').slice(0, 48)
    const oldKey = scopeKey(session.agent_id, oldId)
    const newKey = scopeKey(session.agent_id, session.id)
    if (oldKey in pins && !(newKey in pins)) {
      pins[newKey] = pins[oldKey]
      changed = true
    }
  }
  if (changed) storage.setItem('astrorder_pinned_sessions', JSON.stringify(pins))
}
