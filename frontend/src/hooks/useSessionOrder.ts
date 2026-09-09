import { useEffect, useState } from 'react'
import type { Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
const eventName = 'astrorder:user-message-submitted'
export const nativeActivityEvent = 'astrorder:native-user-activity'
export type UserActivity = { agent_id: string; id: string; last_user_at: string }
export function notifySessionSubmitted(session: Session) { window.dispatchEvent(new CustomEvent(eventName, { detail: scopeKey(session.agent_id, session.id) })) }
export function reconcileSessionOrder(previous: string[], sessions: Session[], sort: boolean): string[] {
  const key = (s: Session) => scopeKey(s.agent_id, s.id)
  if (sort || previous.length === 0) return [...sessions].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).map(key)
  const present = new Set(sessions.map(key))
  const retained = previous.filter(id => present.has(id))
  const known = new Set(retained)
  return [...retained, ...sessions.map(key).filter(id => !known.has(id))]
}
export function mergeUserActivity(previous: Record<string, string>, rows: UserActivity[]): Record<string, string> {
  let next = previous
  for (const row of rows) {
    const key = scopeKey(row.agent_id, row.id)
    if (Number.isFinite(Date.parse(row.last_user_at)) && (!previous[key] || row.last_user_at > previous[key])) {
      if (next === previous) next = { ...previous }
      next[key] = row.last_user_at
    }
  }
  return next
}
export function useSessionOrder(sessions: Session[]): Session[] {
  const [activity, setActivity] = useState<Record<string, string>>({})
  const rows = sessions.map(s => activity[scopeKey(s.agent_id, s.id)] ? { ...s, updated_at: activity[scopeKey(s.agent_id, s.id)] } : s)
  const [state, setState] = useState(() => ({ input: sessions, activity, keys: reconcileSessionOrder([], rows, true) }))
  let keys = state.keys
  if (state.input !== sessions || state.activity !== activity) {
    keys = reconcileSessionOrder(state.keys, rows, state.activity !== activity)
    setState({ input: sessions, activity, keys })
  }
  useEffect(() => {
    const onNative = (event: Event) => setActivity(previous => mergeUserActivity(previous, (event as CustomEvent<UserActivity[]>).detail))
    window.addEventListener(nativeActivityEvent, onNative)
    return () => window.removeEventListener(nativeActivityEvent, onNative)
  }, [])
  useEffect(() => {
    const onSubmit = (event: Event) => {
      const submitted = (event as CustomEvent<string>).detail
      const sorted = reconcileSessionOrder([], rows, true)
      setState({ input: sessions, activity, keys: sorted.includes(submitted) ? [submitted, ...sorted.filter(key => key !== submitted)] : sorted })
    }
    window.addEventListener(eventName, onSubmit)
    return () => window.removeEventListener(eventName, onSubmit)
  }, [sessions, activity])
  const byKey = new Map(rows.map(s => [scopeKey(s.agent_id, s.id), s]))
  return keys.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
}
