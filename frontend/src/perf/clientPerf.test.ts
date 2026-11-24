import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  clientPerf,
  describeElementTarget,
  normalizeApiPath,
  normalizeRoute,
  normalizeScriptUrl,
} from './clientPerf'
import { ensureFetchLater } from './fetchLaterPolyfill'

describe('clientPerf sanitizers and metrics', () => {
  afterEach(() => {
    clientPerf.stop()
    clientPerf.setAuthenticated(false)
    vi.restoreAllMocks()
  })

  it('normalizes sensitive IDs, queries, routes, script URLs, and element targets', () => {
    expect(normalizeApiPath('/sessions/c5f75ce2e7c847daabe75ea7/messages?agent_id=agy&limit=50')).toBe('/sessions/:id/messages')
    expect(normalizeApiPath('/api/v1/sessions/refresh-activity')).toBe('/sessions/refresh-activity')
    expect(normalizeApiPath('/commands/cmd-123/send')).toBe('/commands/:id/send')
    expect(normalizeRoute('/chat/c5f75ce2e7c847daabe75ea7?agent_id=agy')).toBe('/chat/:id')
    expect(normalizeRoute('/chat/group-abc123')).toBe('/chat/group-:id')
    expect(normalizeScriptUrl('http://wails.localhost/assets/index-Dmu_2PyU.js?v=1')).toBe('assets/index-Dmu_2PyU.js')

    const btn = document.createElement('button')
    btn.className = 'session-row is-active extra-class'
    btn.setAttribute('aria-label', '打开会话')
    btn.textContent = 'Sensitive prompt title that must not leak'
    const desc = describeElementTarget(btn)
    expect(desc).toBe('button.session-row.is-active[aria-label="打开会话"]')
    expect(desc).not.toContain('Sensitive')
  })

  it('aggregates API timings, WS pressure, LoAF style/layout fallbacks, and INP subparts', () => {
    clientPerf.setRoute('/chat/abc123')
    clientPerf.recordApiTiming('GET', '/sessions/abc123/messages?agent_id=codex', 200, 320, 4096)
    clientPerf.recordApiTiming('GET', '/sessions/def456/messages?agent_id=codex', 200, 80, 1024)
    clientPerf.recordApiTiming('POST', '/telemetry/client-perf', 202, 12, 11) // ignored

    clientPerf.recordWsDeltaFlush(25, 11.4)
    clientPerf.recordWsEvent('session.upsert', 1, 2.3)

    // LoAF entry without separate styleDuration/layoutDuration exercises combined fallback
    clientPerf.handleLoafEntry({
      name: 'long-animation-frame',
      entryType: 'long-animation-frame',
      startTime: 100,
      duration: 145,
      blockingDuration: 95,
      styleAndLayoutStart: 210,
      paintTime: 245,
      scripts: [
        {
          invokerType: 'event-listener',
          sourceURL: 'http://wails.localhost/assets/index-abc.js',
          sourceFunctionName: 'applyMessageDeltas',
          duration: 78,
          forcedStyleAndLayoutDuration: 19,
        },
      ],
      toJSON: () => ({}),
    })

    // Hover entry (interactionId === 0) must be ignored as non-INP noise.
    clientPerf.handleEventTimingEntry({
      name: 'pointerover',
      entryType: 'event',
      startTime: 480,
      duration: 60,
      interactionId: 0,
      processingStart: 490,
      processingEnd: 491,
      target: document.createElement('div'),
      toJSON: () => ({}),
    })

    // Event timing entry for INP subpart attribution
    clientPerf.handleEventTimingEntry({
      name: 'click',
      entryType: 'event',
      startTime: 500,
      duration: 240,
      interactionId: 42,
      processingStart: 530,
      processingEnd: 650,
      target: document.createElement('button'),
      toJSON: () => ({}),
    })

    const snap = clientPerf.buildSnapshot() as Record<string, any>
    expect(snap.route).toBe('/chat/:id')
    expect(snap.api.routes).toHaveLength(1)
    expect(snap.api.routes[0].route).toBe('GET /sessions/:id/messages')
    expect(snap.api.routes[0].count).toBe(2)
    expect(snap.api.slow_requests).toHaveLength(1)
    expect(snap.api.slow_requests[0].ms).toBe(320)

    expect(snap.ws.events_total).toBe(26)
    expect(snap.ws.delta_flushes).toBe(1)
    expect(snap.ws.delta_batch_max).toBe(25)

    expect(snap.loaf.count).toBe(1)
    expect(snap.loaf.render_style_and_layout_ms).toBe(35)
    expect(snap.loaf.heavy_scripts[0].script).toBe('assets/index-abc.js')
    expect(snap.loaf.heavy_scripts[0].forced_style_layout_ms).toBe(19)

    expect(snap.interactions.slow_count).toBe(1)
    expect(snap.interactions.poor_200ms_count).toBe(1)
    expect(snap.interactions.worst[0]).toMatchObject({
      type: 'click',
      ms: 240,
      inputDelayMs: 30,
      processingMs: 120,
      presentationDelayMs: 90,
    })
  })

  it('delivers snapshots through fetchLater and resets window accumulators after immediate flush', () => {
    const fetchLaterMock = vi.fn().mockReturnValue({ activated: true })
    const prevFetchLater = globalThis.fetchLater
    globalThis.fetchLater = fetchLaterMock
    try {
      clientPerf.setAuthenticated(true)
      clientPerf.recordApiTiming('GET', '/bootstrap', 200, 410, 2048)
      expect(clientPerf.flushNow()).toBe(true)
      expect(fetchLaterMock).toHaveBeenCalledWith(
        '/api/v1/telemetry/client-perf',
        expect.objectContaining({
          method: 'POST',
          credentials: 'include',
          activateAfter: 0,
        }),
      )
      const body = JSON.parse(String(fetchLaterMock.mock.calls[0][1].body))
      expect(body.api.routes.some((r: { route: string }) => r.route === 'GET /bootstrap')).toBe(true)

      // Window accumulators are reset after flush so the next window contains fresh deltas only.
      const after = clientPerf.buildSnapshot() as Record<string, any>
      expect(after.api.routes).toHaveLength(0)
      expect(after.window_seq).toBeGreaterThan(body.window_seq)
    } finally {
      globalThis.fetchLater = prevFetchLater
    }
  })

  it('installs fetchLater polyfill when missing and sends via keepalive on abort-safe timeout', () => {
    vi.useFakeTimers()
    const prevFetchLater = globalThis.fetchLater
    delete globalThis.fetchLater
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 202 }))
    try {
      const fn = ensureFetchLater()
      expect(typeof fn).toBe('function')
      const controller = new AbortController()
      const res = fn!('/api/v1/telemetry/client-perf', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
        signal: controller.signal,
        activateAfter: 500,
      })
      expect(res.activated).toBe(false)
      vi.advanceTimersByTime(500)
      expect(res.activated).toBe(true)
      expect(fetchSpy).toHaveBeenCalledWith(
        '/api/v1/telemetry/client-perf',
        expect.objectContaining({ keepalive: true }),
      )
    } finally {
      globalThis.fetchLater = prevFetchLater
      vi.useRealTimers()
    }
  })

  it('drops pending session switch timers on page hide and filters >60s sleep outliers', () => {
    let visibility: DocumentVisibilityState = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
    let nowMs = 1_000
    vi.spyOn(performance, 'now').mockImplementation(() => nowMs)
    vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation((cb) => {
      cb(nowMs)
      return 1
    })

    // Flush any prior samples from earlier tests in the singleton collector
    const fetchLaterMock = vi.fn().mockReturnValue({ activated: true })
    const prevFetchLater = globalThis.fetchLater
    globalThis.fetchLater = fetchLaterMock
    try {
      clientPerf.setAuthenticated(true)
      clientPerf.flushNow()
      clientPerf.start()

      // 1) Switch interrupted by page hide (sleep/background) is abandoned
      clientPerf.markSessionSwitchStart('agy::sleep-1', true)
      visibility = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      nowMs += 2_300_000
      visibility = 'visible'
      clientPerf.markSessionSwitchReady('agy::sleep-1', 20)

      // 2) Switch exceeding 60s without visibility event is also filtered out
      clientPerf.markSessionSwitchStart('agy::sleep-2', false)
      nowMs += 90_000
      clientPerf.markSessionSwitchReady('agy::sleep-2', 15)

      // 3) Normal switch (42ms) is recorded accurately
      clientPerf.markSessionSwitchStart('agy::fast-1', true)
      nowMs += 42
      clientPerf.markSessionSwitchReady('agy::fast-1', 12)

      const snap = clientPerf.buildSnapshot() as Record<string, any>
      expect(snap.session_switches.count).toBe(1)
      expect(snap.session_switches.max_ms).toBe(42)
    } finally {
      globalThis.fetchLater = prevFetchLater
    }
  })
})
