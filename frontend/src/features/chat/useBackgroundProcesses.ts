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
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const result = await api.getBackgroundProcesses(sessionId, agentId)
        if (!disposed) setSnapshot({ key, items: result.items, error: null })
      } catch (error) {
        if (!disposed) setSnapshot({ key, items: [], error: error instanceof Error ? error.message : String(error) })
      } finally {
        if (!disposed) timer = setTimeout(() => void poll(), 3000)
      }
    }
    void poll()
    return () => { disposed = true; clearTimeout(timer) }
  }, [key, enabled, sessionId, agentId])
  return enabled && snapshot.key === key ? snapshot : { items: [], error: null }
}
