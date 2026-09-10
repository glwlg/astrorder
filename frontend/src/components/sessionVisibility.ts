import type { Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'

export function isSessionOpen(session: Session): boolean {
  if (session.live === true) return true
  if (session.status === 'running' || session.status === 'waiting_approval') return true
  if (typeof session.is_open === 'boolean') return session.is_open
  return false
}

export function adjacentOpenSession(sessions: Session[], current: Session | null, direction: number): Session | undefined {
  if (!current) return undefined
  const index = sessions.findIndex(s => scopeKey(s.agent_id, s.id) === scopeKey(current.agent_id, current.id))
  if (index < 0) return undefined
  const step = direction < 0 ? -1 : 1
  for (let i = index + step; i >= 0 && i < sessions.length; i += step) if (isSessionOpen(sessions[i])) return sessions[i]
  return undefined
}
