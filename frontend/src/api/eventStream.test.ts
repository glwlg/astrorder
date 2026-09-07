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

  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  close() {
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
})
