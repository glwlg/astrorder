import type { EventEnvelope } from '../domain/types'
import { getBackendWsHost, getWsProtocol } from '../desktop/adapter'

export type EventStreamStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface EventStreamOptions {
  after: number
  onEvent: (event: EventEnvelope) => void
  onStatus: (status: EventStreamStatus) => void
  onResume?: () => void
  reconnectMs?: number
}

export function buildEventStreamUrl(after: number): string {
  const protocol = getWsProtocol()
  let localToken: string | null = null
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
      localToken = localStorage.getItem('astrorder:token')
    }
  } catch {}
  const tokenParam = localToken ? `&token=${encodeURIComponent(localToken)}` : ''
  return `${protocol}//${getBackendWsHost()}/ws/v1/events?after=${encodeURIComponent(String(after))}${tokenParam}`
}

function isEventEnvelope(value: unknown): value is EventEnvelope {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<EventEnvelope>
  return typeof candidate.id === 'string' && typeof candidate.cursor === 'number' && typeof candidate.type === 'string'
}

/** Browser events are read-only. Commands never travel through this socket. */
export function connectEventStream({
  after,
  onEvent,
  onStatus,
  onResume,
  reconnectMs = 1200,
}: EventStreamOptions): () => void {
  let stopped = false
  let socket: WebSocket | null = null
  let reconnectTimer: number | undefined
  let cursor = after
  let connectedBefore = false

  const closeSocket = () => {
    const previous = socket
    socket = null
    if (!previous) return
    previous.onopen = null
    previous.onmessage = null
    previous.onerror = null
    previous.onclose = null
    previous.close()
  }

  const connect = () => {
    if (stopped || socket?.readyState === WebSocket.CONNECTING || socket?.readyState === WebSocket.OPEN) return
    if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
    closeSocket()
    onStatus('connecting')
    let current: WebSocket
    try {
      current = new WebSocket(buildEventStreamUrl(cursor))
      socket = current
    } catch {
      onStatus('error')
      reconnectTimer = window.setTimeout(connect, reconnectMs)
      return
    }
    const isCurrent = () => !stopped && socket === current
    current.onopen = () => {
      if (!isCurrent()) return
      onStatus('connected')
      if (connectedBefore) onResume?.()
      connectedBefore = true
    }
    current.onmessage = (message) => {
      if (!isCurrent()) return
      let parsed: unknown
      try {
        parsed = JSON.parse(String(message.data))
      } catch {
        return
      }
      if (!isEventEnvelope(parsed)) return
      cursor = Math.max(cursor, parsed.cursor)
      onEvent(parsed)
    }
    current.onerror = () => { if (isCurrent()) onStatus('error') }
    current.onclose = () => {
      if (!isCurrent()) return
      socket = null
      onStatus('disconnected')
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      reconnectTimer = window.setTimeout(connect, reconnectMs)
    }
  }

  connect()

  const onWake = () => {
    if (stopped) return
    if (socket?.readyState === WebSocket.CONNECTING) return
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      connect()
      return
    }
    onResume?.()
  }
  const onVisibilityChange = () => {
    if (document.visibilityState === 'visible') onWake()
  }
  window.addEventListener('online', onWake)
  document.addEventListener('visibilitychange', onVisibilityChange)

  return () => {
    stopped = true
    if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
    window.removeEventListener('online', onWake)
    document.removeEventListener('visibilitychange', onVisibilityChange)
    closeSocket()
  }
}
