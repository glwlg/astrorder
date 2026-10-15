import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { buildEventStreamUrl, connectEventStream } from './eventStream'

class FakeSocket {
  static instances: FakeSocket[] = []
  readonly url: string
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  readyState = 0
  deferClose = false

  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  static readonly CONNECTING = 0
  static readonly OPEN = 1

  close() {
    if (this.deferClose) { this.readyState = 2; return }
    this.readyState = 3
    this.onclose?.()
  }

  open() {
    this.readyState = 1
    this.onopen?.()
  }

  sendEvent(value: unknown) {
    this.onmessage?.({ data: JSON.stringify(value) })
  }
}

const originalSocket = globalThis.WebSocket

beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeSocket)
})

afterEach(() => {
  vi.useRealTimers()
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', originalSocket)
})

describe('event stream', () => {
  it('delivers cursorless live events and reconnects with the durable cursor', () => {
    vi.useFakeTimers()
    const onEvent = vi.fn()
    const stop = connectEventStream({ after: 42, onEvent, onStatus: vi.fn(), reconnectMs: 50 })
    const socket = FakeSocket.instances[0]
    socket.open()
    socket.sendEvent({ id: 'delta', cursor: 0, type: 'message.delta', data: { text: 'live' } })
    expect(onEvent).toHaveBeenCalledWith(expect.objectContaining({ type: 'message.delta', cursor: 0 }))
    socket.close()
    vi.advanceTimersByTime(50)
    expect(FakeSocket.instances[1].url).toContain('after=42')
    stop()
  })

  it('puts the durable cursor in the replay URL and ignores heartbeat frames', () => {
    expect(buildEventStreamUrl(42)).toContain('/ws/v1/events?after=42')
    const onEvent = vi.fn()
    const stop = connectEventStream({ after: 42, onEvent, onStatus: vi.fn() })
    const socket = FakeSocket.instances[0]

    socket.sendEvent({ type: 'ping' })
    socket.sendEvent({ id: 'event-1', cursor: 43, type: 'session.upsert', data: {} })

    expect(onEvent).toHaveBeenCalledTimes(1)
    stop()
  })

  it('reconnects using the most recent received cursor', () => {
    vi.useFakeTimers()
    const stop = connectEventStream({ after: 10, onEvent: vi.fn(), onStatus: vi.fn(), reconnectMs: 50 })
    const first = FakeSocket.instances[0]
    first.sendEvent({ id: 'event-1', cursor: 11, type: 'session.upsert', data: {} })
    first.close()
    vi.advanceTimersByTime(50)

    expect(FakeSocket.instances[1].url).toContain('after=11')
    stop()
  })

  it('resyncs resources after a lost connection opens again', () => {
    vi.useFakeTimers()
    const onResume = vi.fn()
    const stop = connectEventStream({ after: 10, onEvent: vi.fn(), onStatus: vi.fn(), onResume, reconnectMs: 50 })
    FakeSocket.instances[0].open()
    expect(onResume).not.toHaveBeenCalled()
    FakeSocket.instances[0].close()
    vi.advanceTimersByTime(50)
    FakeSocket.instances[1].open()
    expect(onResume).toHaveBeenCalledOnce()
    stop()
  })

  it('keeps one pending connection when online and visibility wake events overlap', () => {
    vi.useFakeTimers()
    const onResume = vi.fn()
    const stop = connectEventStream({ after: 10, onEvent: vi.fn(), onStatus: vi.fn(), onResume, reconnectMs: 50 })
    try {
      FakeSocket.instances[0].open()
      FakeSocket.instances[0].sendEvent({ id: 'latest', cursor: 11, type: 'session.upsert', data: {} })
      FakeSocket.instances[0].close()
      window.dispatchEvent(new Event('online'))
      const reconnecting = FakeSocket.instances[1]
      window.dispatchEvent(new Event('online'))
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(100)
      expect(FakeSocket.instances).toHaveLength(2)
      expect(reconnecting.url).toContain('after=11')
      reconnecting.open()
      expect(onResume).toHaveBeenCalledOnce()
    } finally { stop() }
  })

  it('ignores late callbacks from a replaced or stopped socket', () => {
    vi.useFakeTimers()
    const onEvent = vi.fn()
    const onStatus = vi.fn()
    const onResume = vi.fn()
    const stop = connectEventStream({ after: 10, onEvent, onStatus, onResume, reconnectMs: 50 })
    try {
      const first = FakeSocket.instances[0]
      first.open()
      first.deferClose = true
      first.close()
      const lateOpen = first.onopen
      const lateMessage = first.onmessage
      const lateError = first.onerror
      const lateClose = first.onclose
      window.dispatchEvent(new Event('online'))
      const replacement = FakeSocket.instances[1]
      replacement.open()
      const statuses = onStatus.mock.calls.length
      lateOpen?.()
      lateMessage?.({ data: JSON.stringify({ id: 'stale', cursor: 99, type: 'session.upsert', data: {} }) })
      lateError?.()
      lateClose?.()
      vi.advanceTimersByTime(100)
      expect(onEvent).not.toHaveBeenCalled()
      expect(onStatus).toHaveBeenCalledTimes(statuses)
      expect(onResume).toHaveBeenCalledOnce()
      expect(FakeSocket.instances).toHaveLength(2)
      const afterStop = { open: replacement.onopen, message: replacement.onmessage, error: replacement.onerror, close: replacement.onclose }
      stop()
      afterStop.open?.()
      afterStop.message?.({ data: JSON.stringify({ id: 'disposed', cursor: 100, type: 'session.upsert', data: {} }) })
      afterStop.error?.()
      afterStop.close?.()
      vi.advanceTimersByTime(100)
      expect(onEvent).not.toHaveBeenCalled()
      expect(onStatus).toHaveBeenCalledTimes(statuses)
      expect(onResume).toHaveBeenCalledOnce()
      expect(FakeSocket.instances).toHaveLength(2)
    } finally { stop() }
  })

  it('reloads the visible transcript when the page returns without closing the socket', () => {
    const onResume = vi.fn()
    const stop = connectEventStream({ after: 10, onEvent: vi.fn(), onStatus: vi.fn(), onResume })
    FakeSocket.instances[0].open()
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
    document.dispatchEvent(new Event('visibilitychange'))
    expect(onResume).toHaveBeenCalledOnce()
    expect(FakeSocket.instances).toHaveLength(1)
    stop()
  })
})
