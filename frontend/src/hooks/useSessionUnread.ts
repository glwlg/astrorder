import { useCallback, useEffect, useMemo, useState } from 'react'
import { scopeKey } from '../domain/semantics'
import type { EventNotification } from '../domain/types'
import { useAstrorderStore } from '../state/store'

const READ_KEY = 'astrorder:sessions-read-notice'
const READ_EVENT = 'astrorder:session-read'

function readMarkers(): Record<string, string> {
  try { return JSON.parse(localStorage.getItem(READ_KEY) || '{}') } catch { return {} }
}

export function useSessionUnread(activeKey?: string) {
  const notices = useAstrorderStore(state => state.notifications)
  const [read, setRead] = useState(readMarkers)
  const latest = useMemo(() => {
    const result: Record<string, EventNotification> = {}
    for (const notice of Object.values(notices)) {
      const key = scopeKey(notice.agent_id, notice.session_id)
      if (!result[key] || (notice.cursor || 0) > (result[key].cursor || 0)) result[key] = notice
    }
    return result
  }, [notices])
  const markRead = useCallback((key: string) => {
    const notice = latest[key]
    if (!notice) return
    const current = readMarkers()
    if (current[key] === notice.key) return
    const next = { ...current, [key]: notice.key }
    try { localStorage.setItem(READ_KEY, JSON.stringify(next)) } catch {}
    setRead(next)
    window.dispatchEvent(new Event(READ_EVENT))
  }, [latest])
  useEffect(() => {
    const refresh = () => setRead(readMarkers())
    window.addEventListener('storage', refresh)
    window.addEventListener(READ_EVENT, refresh)
    return () => {
      window.removeEventListener('storage', refresh)
      window.removeEventListener(READ_EVENT, refresh)
    }
  }, [])
  useEffect(() => {
    const acknowledge = () => {
      if (activeKey && document.visibilityState === 'visible' && document.hasFocus()) markRead(activeKey)
    }
    acknowledge()
    window.addEventListener('focus', acknowledge)
    document.addEventListener('visibilitychange', acknowledge)
    return () => {
      window.removeEventListener('focus', acknowledge)
      document.removeEventListener('visibilitychange', acknowledge)
    }
  }, [activeKey, markRead])
  const isUnread = useCallback((key: string) => Boolean(latest[key] && read[key] !== latest[key].key), [latest, read])
  return { isUnread, markRead }
}
