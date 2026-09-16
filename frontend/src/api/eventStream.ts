import type { EventEnvelope } from '../domain/types'

export type EventStreamStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface EventStreamOptions {
  after: number
  onEvent: (event: EventEnvelope) => void
  onStatus: (status: EventStreamStatus) => void
  reconnectMs?: number
}

export function buildEventStreamUrl(after: number): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/ws/v1/events?after=${encodeURIComponent(String(after))}`
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
  reconnectMs = 1200,
}: EventStreamOptions): () => void {
  let stopped = false
  let socket: WebSocket | null = null
  let reconnectTimer: number | undefined
  let cursor = after

  const connect = () => {
    if (stopped) return
    onStatus('connecting')
    try {
      socket = new WebSocket(buildEventStreamUrl(cursor))
    } catch {
      onStatus('error')
      reconnectTimer = window.setTimeout(connect, reconnectMs)
      return
    }
    socket.onopen = () => onStatus('connected')
    socket.onmessage = (message) => {
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
    socket.onerror = () => onStatus('error')
    socket.onclose = () => {
      socket = null
      if (stopped) return
      onStatus('disconnected')
      reconnectTimer = window.setTimeout(connect, reconnectMs)
    }
  }

  connect()

  const onWake = () => {
    if (stopped) return
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer)
      socket?.close()
      socket = null
      connect()
    }
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
    socket?.close()
    socket = null
  }
}
