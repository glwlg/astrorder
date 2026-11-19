import { useCallback, useEffect, useRef, useState } from 'react'
import type { BrowserSnapshot } from '../../../../api/client'
import { browserDesign, type BrowserDesignCommand, type BrowserDesignResponse, type BrowserDesignState, type DesignProperty } from './browserDesign'

export function useBrowserDesigner(agentId: string, sessionId: string, targetId: string, onSnapshot: (snapshot: BrowserSnapshot) => void) {
  const [state, setState] = useState<BrowserDesignState | null>(null)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(0)
  const [staged, setStaged] = useState<Partial<Record<DesignProperty, string>>>({})
  const latest = useRef<BrowserDesignState | null>(null)
  const queue = useRef<Promise<unknown>>(Promise.resolve())
  const controller = useRef<AbortController | null>(null)
  const scheduled = useRef(new Map<DesignProperty, BrowserDesignCommand>())
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pendingCount = useRef(0)

  const run = useCallback((command: BrowserDesignCommand): Promise<BrowserDesignResponse | null> => {
    const signal = controller.current?.signal
    const documentId = command.document_id || latest.current?.document_id
    pendingCount.current += 1
    setPending(pendingCount.current)
    const operation = queue.current.then(async () => {
      try {
        if (!signal || signal.aborted) return null
        const response = await browserDesign(agentId, sessionId, targetId, { ...command, document_id: documentId }, signal)
        if (signal.aborted) return null
        latest.current = response.state
        setState(response.state)
        setError('')
        onSnapshot(response.snapshot)
        return response
      } catch (value) {
        if (!signal?.aborted) setError(value instanceof Error ? value.message : '界面调整失败')
        return null
      } finally {
        pendingCount.current -= 1
        if (!controller.current?.signal.aborted) setPending(pendingCount.current)
      }
    })
    queue.current = operation
    return operation
  }, [agentId, sessionId, targetId, onSnapshot])

  const flush = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = null
    const commands = [...scheduled.current.values()]
    scheduled.current.clear()
    return Promise.all(commands.map(command => run(command))).then(results => {
      if (!controller.current?.signal.aborted) {
        setStaged(current => {
          const next = { ...current }
          for (const command of commands) {
            if (command.property && next[command.property] === command.value) delete next[command.property]
          }
          return next
        })
      }
      return results
    })
  }, [run])

  const change = useCallback((property: DesignProperty, value: string) => {
    const selected = latest.current?.selected
    if (!selected) return
    setStaged(current => ({ ...current, [property]: value }))
    scheduled.current.set(property, {
      action: 'apply', document_id: latest.current!.document_id,
      element_id: selected.id, property, value,
    })
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => { void flush() }, 240)
  }, [flush])

  useEffect(() => {
    const current = new AbortController()
    const pendingCommands = scheduled.current
    controller.current = current
    queueMicrotask(() => { if (!current.signal.aborted) void run({ action: 'state' }) })
    return () => {
      current.abort()
      if (timer.current) clearTimeout(timer.current)
      pendingCommands.clear()
    }
  }, [run])

  const action = useCallback(async (command: BrowserDesignCommand) => {
    const results = await flush()
    if (results.some(result => result === null)) return null
    return run(command)
  }, [flush, run])

  return { state, error, busy: pending > 0 || Object.keys(staged).length > 0, staged, change, action }
}
