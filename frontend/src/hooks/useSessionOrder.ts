import { useEffect, useState } from 'react'
import type { Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
const eventName = 'astrorder:user-message-submitted'
export const nativeActivityEvent = 'astrorder:native-user-activity'
export type UserActivity = { agent_id: string; id: string; last_user_at: string; live?: boolean }
export function notifySessionSubmitted(session: Session) { window.dispatchEvent(new CustomEvent(eventName, { detail: scopeKey(session.agent_id, session.id) })) }
function newerStamp(left?: string, right?: string): string | undefined {
  if (!left) return right
  if (!right) return left
  return left > right ? left : right
}
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
function overlayActivity(sessions: Session[], activity: Record<string, string>, live: Record<string, boolean> | null): Session[] {
  return sessions.map(s => {
    const key = scopeKey(s.agent_id, s.id)
    const stamp = newerStamp(activity[key], newerStamp(s.last_user_at, s.updated_at)) || s.updated_at
    const liveFlag = live ? Boolean(live[key]) : Boolean(s.live)
    return stamp !== s.updated_at || liveFlag !== Boolean(s.live) ? { ...s, updated_at: stamp, live: liveFlag } : s
  })
}
export function useSessionOrder(sessions: Session[]): Session[] {
  const [activity, setActivity] = useState<Record<string, string>>({})
  const [live, setLive] = useState<Record<string, boolean> | null>(null)
  const rows = overlayActivity(sessions, activity, live)
  const [state, setState] = useState(() => ({ input: sessions, activity, live, keys: reconcileSessionOrder([], rows, true) }))
  let keys = state.keys
  if (state.input !== sessions || state.activity !== activity || state.live !== live) {
    const sort = state.activity !== activity || state.live !== live || state.keys.length === 0
    keys = reconcileSessionOrder(state.keys, rows, sort)
    setState({ input: sessions, activity, live, keys })
  }
  useEffect(() => {
    const onNative = (event: Event) => {
      const detail = (event as CustomEvent<{ items?: UserActivity[]; live?: UserActivity[] } | UserActivity[]>).detail
      const rows = Array.isArray(detail) ? detail : detail?.items || []
      const liveRows = Array.isArray(detail) ? undefined : detail?.live
      setActivity(previous => mergeUserActivity(previous, rows))
      if (liveRows) {
        const next: Record<string, boolean> = {}
        for (const row of liveRows) next[scopeKey(row.agent_id, row.id)] = true
        setLive(next)
      }
    }
    window.addEventListener(nativeActivityEvent, onNative)
    return () => window.removeEventListener(nativeActivityEvent, onNative)
  }, [])
  useEffect(() => {
    const onSubmit = (event: Event) => {
      const submitted = (event as CustomEvent<string>).detail
      const sorted = reconcileSessionOrder([], rows, true)
      setState({ input: sessions, activity, live, keys: sorted.includes(submitted) ? [submitted, ...sorted.filter(key => key !== submitted)] : sorted })
    }
    window.addEventListener(eventName, onSubmit)
    return () => window.removeEventListener(eventName, onSubmit)
  }, [sessions, activity, live])
  const byKey = new Map(rows.map(s => [scopeKey(s.agent_id, s.id), s]))
  return keys.flatMap(key => byKey.has(key) ? [byKey.get(key)!] : [])
}
