import { useEffect, useState } from 'react'
import { api } from '../../api/client'
import type { Task } from '../../domain/types'

export function useBackgroundProcesses(session: { id: string; agent_id: string } | null, enabled: boolean) {
  const [snapshot, setSnapshot] = useState<{ key: string; items: Task[]; error: string | null }>({ key: '', items: [], error: null })
  const sessionId = session?.id
  const agentId = session?.agent_id
  const key = sessionId && agentId ? JSON.stringify([agentId, sessionId]) : ''
  useEffect(() => {
    if (!sessionId || !agentId || !enabled) return
    let disposed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const schedule = (delayMs: number) => {
      if (disposed) return
      clearTimeout(timer)
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
        timer = undefined
        return
      }
      timer = setTimeout(() => {
        timer = undefined
        void poll()
      }, delayMs)
    }
    const poll = async () => {
      if (disposed) return
      let nextDelay = 12_000
      try {
        const result = await api.getBackgroundProcesses(sessionId, agentId)
        if (!disposed) {
          setSnapshot({ key, items: result.items, error: null })
          if (result.items.length > 0) nextDelay = 3000
        }
      } catch (error) {
        if (!disposed) setSnapshot({ key, items: [], error: error instanceof Error ? error.message : String(error) })
      } finally {
        schedule(nextDelay)
      }
    }
    const onVisibilityChange = () => {
      if (!disposed && document.visibilityState === 'visible' && timer === undefined) {
        void poll()
      }
    }
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', onVisibilityChange)
    }
    void poll()
    return () => {
      disposed = true
      clearTimeout(timer)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', onVisibilityChange)
      }
    }
  }, [key, enabled, sessionId, agentId])
  return enabled && snapshot.key === key ? snapshot : { items: [], error: null }
}
