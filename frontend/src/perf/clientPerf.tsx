import { Profiler, type ProfilerOnRenderCallback, type ReactNode } from 'react'
import { useAstrorderStore } from '../state/store'
import { ensureFetchLater, type FetchLaterResult } from './fetchLaterPolyfill'

const TELEMETRY_ENDPOINT = '/api/v1/telemetry/client-perf'
const FLUSH_INTERVAL_MS = 30_000
const BEACON_REFRESH_MS = 10_000
const SLOW_API_THRESHOLD_MS = 250
const SLOW_INTERACTION_THRESHOLD_MS = 40
const MAX_API_SAMPLES_PER_ROUTE = 64
const MAX_SLOW_APIS = 15
const MAX_WORST_LOAFS = 12
const MAX_WORST_INTERACTIONS = 12
const MAX_RECENT_SWITCHES = 10
const MAX_VALID_SWITCH_MS = 60_000
const MAX_PAYLOAD_BYTES = 48 * 1024

interface PerfScriptTiming {
  invokerType?: string
  sourceURL?: string
  sourceFunctionName?: string
  duration: number
  forcedStyleDuration?: number
  forcedLayoutDuration?: number
  forcedStyleAndLayoutDuration?: number
}

interface PerfLoafEntry extends PerformanceEntry {
  blockingDuration?: number
  renderStart?: number
  styleAndLayoutStart?: number
  styleDuration?: number
  layoutDuration?: number
  paintTime?: number
  scripts?: PerfScriptTiming[]
}

interface PerfEventTimingEntry extends PerformanceEntry {
  interactionId?: number
  processingStart: number
  processingEnd: number
  target?: EventTarget | null
}

interface PerfMemoryInfo {
  usedJSHeapSize: number
  totalJSHeapSize: number
  jsHeapSizeLimit: number
}

interface ApiRouteBucket {
  count: number
  errors: number
  durations: number[]
  maxMs: number
  totalBytes: number
  maxBytes: number
}

interface SlowApiSample {
  method: string
  path: string
  status: number
  ms: number
  bytes: number
  at: string
}

interface HeavyScriptBucket {
  script: string
  invoker: string
  fn: string
  count: number
  totalMs: number
  maxMs: number
  forcedStyleMs: number
  forcedLayoutMs: number
  forcedStyleAndLayoutMs: number
}

interface WorstLoafSample {
  ms: number
  blockingMs: number
  styleMs: number
  layoutMs: number
  styleAndLayoutMs: number
  route: string
  topScript: string
  topFn: string
  at: string
}

interface InteractionSample {
  id: number
  type: string
  target: string
  route: string
  ms: number
  inputDelayMs: number
  processingMs: number
  presentationDelayMs: number
  at: string
}

interface ProfilerBucket {
  id: string
  commits: number
  mounts: number
  totalActualMs: number
  maxActualMs: number
  slowCommits16Ms: number
  maxBaseMs: number
}

interface SessionSwitchSample {
  cached: boolean
  messages: number
  ms: number
  route: string
}

export interface QueryCacheStats {
  total: number
  active: number
  fetching: number
}

function round1(value: number): number {
  return Math.round(value * 10) / 10
}

function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0
  const sorted = [...values].sort((a, b) => a - b)
  const idx = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1))
  return round1(sorted[idx])
}

export function normalizeApiPath(rawPath: string): string {
  const withoutPrefix = rawPath.startsWith('/api/v1') ? rawPath.slice('/api/v1'.length) : rawPath
  const pathOnly = withoutPrefix.split('?')[0] || '/'
  const segments = pathOnly.split('/')
  const idParents = new Set([
    'sessions',
    'commands',
    'schedules',
    'groups',
    'integrations',
    'attachments',
    'handoffs',
    'background-tasks',
    'rtk',
    'agents',
    'connections',
    'jobs',
    'backups',
    'runs',
  ])
  const out = segments.map((seg, idx) => {
    if (!seg) return seg
    const prev = idx > 0 ? segments[idx - 1] : ''
    if (idParents.has(prev) && !['refresh-activity', 'batch-delete', 'handoff-curate', 'preview', 'upgrade', 'targets', 'local-plugin', 'qr', 'session'].includes(seg)) {
      return ':id'
    }
    if (/^[0-9a-f-]{16,}$/i.test(seg) || /^\d{6,}$/.test(seg)) {
      return ':id'
    }
    return seg
  })
  return out.join('/') || '/'
}

export function normalizeRoute(pathname: string): string {
  const clean = (pathname || '/').split('?')[0]
  if (clean.startsWith('/chat/group-')) return '/chat/group-:id'
  if (clean.startsWith('/chat/')) return '/chat/:id'
  if (clean.startsWith('/groups/')) return '/groups/:id'
  if (clean.startsWith('/integrations/')) return '/integrations/:id'
  if (clean.startsWith('/mobile/groups/')) return '/mobile/groups/:id'
  if (clean.startsWith('/mobile/')) return '/mobile/:id'
  return clean
}

export function normalizeScriptUrl(url?: string): string {
  if (!url) return 'inline'
  try {
    const parsed = new URL(url, 'http://local')
    const parts = parsed.pathname.split('/').filter(Boolean)
    return parts.slice(-2).join('/') || 'inline'
  } catch {
    const clean = url.split('?')[0].split('#')[0]
    const parts = clean.split(/[\\/]/).filter(Boolean)
    return parts.slice(-2).join('/') || 'inline'
  }
}

export function describeElementTarget(target: EventTarget | null | undefined): string {
  if (!target || typeof Element === 'undefined' || !(target instanceof Element)) return 'unknown'
  const tag = target.tagName.toLowerCase()
  const testId = target.getAttribute('data-testid')
  if (testId) return `${tag}[data-testid="${testId.slice(0, 32)}"]`
  const aria = target.getAttribute('aria-label')
  const classes = typeof target.className === 'string'
    ? target.className.trim().split(/\s+/).filter(Boolean).slice(0, 2).join('.')
    : ''
  let desc = classes ? `${tag}.${classes}` : tag
  if (aria) desc += `[aria-label="${aria.trim().slice(0, 24)}"]`
  return desc.slice(0, 72)
}

function detectSurface(): string {
  if (typeof window === 'undefined') return 'unknown'
  if ('astrorderDesktop' in window || window.location.hostname === 'wails.localhost') {
    return 'desktop-wails'
  }
  const standalone =
    window.matchMedia?.('(display-mode: standalone)').matches ||
    Boolean((navigator as Navigator & { standalone?: boolean }).standalone)
  const mobile =
    window.location.pathname.startsWith('/mobile') ||
    window.innerWidth < 768
  if (mobile) return standalone ? 'mobile-pwa' : 'mobile-web'
  return standalone ? 'web-pwa' : 'web-desktop'
}

function makeClientId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `cli-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

class ClientPerfCollector {
  private readonly clientId = makeClientId()
  private readonly startedAtMs = Date.now()
  private started = false
  private authenticated = false
  private dirty = false
  private windowSeq = 0
  private windowStartedAtMs = Date.now()
  private currentRoute = '/'
  private queryStatsProvider: (() => QueryCacheStats) | null = null

  private observers: PerformanceObserver[] = []
  private tickTimer: ReturnType<typeof setInterval> | null = null
  private pendingBeaconController: AbortController | null = null
  private pendingBeaconResult: FetchLaterResult | null = null

  // Cold start metrics (captured once)
  private bootstrapMs: number | null = null
  private firstSessionReadyMs: number | null = null

  // Windowed metrics (reset after each delivered window)
  private loafSupported = false
  private loafCount = 0
  private loafTotalMs = 0
  private loafMaxMs = 0
  private loafBlockingTotalMs = 0
  private loafBlockingMaxMs = 0
  private renderStyleMs = 0
  private renderLayoutMs = 0
  private renderStyleAndLayoutMs = 0
  private heavyScripts = new Map<string, HeavyScriptBucket>()
  private worstLoafs: WorstLoafSample[] = []

  private longTaskCount = 0
  private longTaskTotalMs = 0
  private longTaskMaxMs = 0

  private interactionById = new Map<number, InteractionSample>()
  private ungroupedInteractions: InteractionSample[] = []
  private slowInteractionCount = 0
  private poorInteractionCount = 0

  private apiRoutes = new Map<string, ApiRouteBucket>()
  private slowApis: SlowApiSample[] = []

  private wsEventsTotal = 0
  private wsEventsByType = new Map<string, number>()
  private wsSecBucket = 0
  private wsSecCount = 0
  private wsPeakEventsPerSec = 0
  private wsDeltaFlushes = 0
  private wsDeltaBatchMax = 0
  private wsApplyTotalMs = 0
  private wsApplyMaxMs = 0
  private wsReconnects = 0
  private wsStatus = 'disconnected'

  private switchPending: { key: string; startMs: number; cached: boolean; route: string } | null = null
  private switchDurations: number[] = []
  private recentSwitches: SessionSwitchSample[] = []

  private profilerStats = new Map<string, ProfilerBucket>()

  private readonly handleVisibilityChange = (): void => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
      // Mobile/desktop sleep suspends timers & rAF; drop any in-flight switch measurement so wakeups don't skew metrics.
      this.switchPending = null
    }
  }

  start(options?: { getQueryStats?: () => QueryCacheStats }): void {
    if (options?.getQueryStats) {
      this.queryStatsProvider = options.getQueryStats
    }
    if (this.started || typeof window === 'undefined') return
    this.started = true
    this.currentRoute = normalizeRoute(window.location.pathname)
    ensureFetchLater()
    this.installObservers()
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', this.handleVisibilityChange)
    }
    this.tickTimer = setInterval(() => this.onTick(), BEACON_REFRESH_MS)
    ;(window as unknown as Record<string, unknown>).__astrorderPerf = {
      snapshot: () => this.buildSnapshot(),
      flushNow: () => this.flushNow(),
    }
  }

  stop(): void {
    if (this.tickTimer) {
      clearInterval(this.tickTimer)
      this.tickTimer = null
    }
    if (typeof document !== 'undefined') {
      document.removeEventListener('visibilitychange', this.handleVisibilityChange)
    }
    for (const obs of this.observers) {
      try {
        obs.disconnect()
      } catch {}
    }
    this.observers = []
    if (this.pendingBeaconController) {
      this.pendingBeaconController.abort()
      this.pendingBeaconController = null
    }
    this.pendingBeaconResult = null
    this.switchPending = null
    this.started = false
  }

  setAuthenticated(authenticated: boolean): void {
    this.authenticated = authenticated
    if (authenticated && this.started) {
      this.dirty = true
      this.refreshDeferredBeacon(false)
    }
  }

  setRoute(pathname: string): void {
    this.currentRoute = normalizeRoute(pathname)
  }

  recordBootstrap(durationMs: number): void {
    if (this.bootstrapMs === null) {
      this.bootstrapMs = round1(durationMs)
      this.dirty = true
    }
  }

  recordApiTiming(method: string, rawPath: string, status: number, durationMs: number, responseBytes = 0): void {
    if (rawPath.includes('/telemetry/')) return
    const normalized = normalizeApiPath(rawPath)
    const upperMethod = (method || 'GET').toUpperCase()
    const key = `${upperMethod} ${normalized}`
    let bucket = this.apiRoutes.get(key)
    if (!bucket) {
      bucket = { count: 0, errors: 0, durations: [], maxMs: 0, totalBytes: 0, maxBytes: 0 }
      this.apiRoutes.set(key, bucket)
    }
    bucket.count++
    if (status === 0 || status >= 400) bucket.errors++
    if (durationMs > bucket.maxMs) bucket.maxMs = durationMs
    if (bucket.durations.length < MAX_API_SAMPLES_PER_ROUTE) {
      bucket.durations.push(durationMs)
    } else {
      bucket.durations[bucket.count % MAX_API_SAMPLES_PER_ROUTE] = durationMs
    }
    if (responseBytes > 0) {
      bucket.totalBytes += responseBytes
      if (responseBytes > bucket.maxBytes) bucket.maxBytes = responseBytes
    }
    if (durationMs >= SLOW_API_THRESHOLD_MS) {
      this.slowApis.push({
        method: upperMethod,
        path: normalized,
        status,
        ms: round1(durationMs),
        bytes: responseBytes,
        at: new Date().toISOString(),
      })
      if (this.slowApis.length > MAX_SLOW_APIS) {
        this.slowApis.sort((a, b) => b.ms - a.ms)
        this.slowApis.length = MAX_SLOW_APIS
      }
    }
    this.dirty = true
  }

  recordWsEvent(type: string, count = 1, applyMs = 0): void {
    this.wsEventsTotal += count
    this.wsEventsByType.set(type, (this.wsEventsByType.get(type) || 0) + count)
    const sec = Math.floor(Date.now() / 1000)
    if (sec === this.wsSecBucket) {
      this.wsSecCount += count
    } else {
      this.wsSecBucket = sec
      this.wsSecCount = count
    }
    if (this.wsSecCount > this.wsPeakEventsPerSec) {
      this.wsPeakEventsPerSec = this.wsSecCount
    }
    if (applyMs > 0) {
      this.wsApplyTotalMs += applyMs
      if (applyMs > this.wsApplyMaxMs) this.wsApplyMaxMs = applyMs
    }
    this.dirty = true
  }

  recordWsDeltaFlush(batchSize: number, applyMs: number): void {
    this.wsDeltaFlushes++
    if (batchSize > this.wsDeltaBatchMax) this.wsDeltaBatchMax = batchSize
    this.recordWsEvent('message.delta.flush', batchSize, applyMs)
  }

  recordWsStatus(status: string): void {
    if (this.wsStatus === 'connected' && (status === 'connecting' || status === 'disconnected' || status === 'error')) {
      this.wsReconnects++
      this.dirty = true
    }
    this.wsStatus = status
  }

  markSessionSwitchStart(key: string, fromCached: boolean): void {
    if (!key) return
    if (this.switchPending?.key === key) return
    this.switchPending = {
      key,
      startMs: typeof performance !== 'undefined' ? performance.now() : Date.now(),
      cached: fromCached,
      route: this.currentRoute,
    }
  }

  markSessionSwitchReady(key: string, messageCount: number): void {
    const pending = this.switchPending
    if (!pending || pending.key !== key) return
    this.switchPending = null
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    const finish = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
      const now = typeof performance !== 'undefined' ? performance.now() : Date.now()
      const elapsed = Math.max(0, round1(now - pending.startMs))
      if (elapsed > MAX_VALID_SWITCH_MS) return
      if (this.firstSessionReadyMs === null) {
        this.firstSessionReadyMs = round1(now)
      }
      this.switchDurations.push(elapsed)
      if (this.switchDurations.length > 64) this.switchDurations.shift()
      this.recentSwitches.push({
        cached: pending.cached,
        messages: messageCount,
        ms: elapsed,
        route: pending.route,
      })
      if (this.recentSwitches.length > MAX_RECENT_SWITCHES) this.recentSwitches.shift()
      this.dirty = true
    }
    if (typeof requestAnimationFrame === 'function') {
      requestAnimationFrame(finish)
    } else {
      finish()
    }
  }

  recordReactCommit(id: string, phase: 'mount' | 'update' | 'nested-update', actualDuration: number, baseDuration: number): void {
    let bucket = this.profilerStats.get(id)
    if (!bucket) {
      bucket = { id, commits: 0, mounts: 0, totalActualMs: 0, maxActualMs: 0, slowCommits16Ms: 0, maxBaseMs: 0 }
      this.profilerStats.set(id, bucket)
    }
    bucket.commits++
    if (phase === 'mount') bucket.mounts++
    bucket.totalActualMs += actualDuration
    if (actualDuration > bucket.maxActualMs) bucket.maxActualMs = actualDuration
    if (actualDuration >= 16) bucket.slowCommits16Ms++
    if (baseDuration > bucket.maxBaseMs) bucket.maxBaseMs = baseDuration
    if (actualDuration >= 8) this.dirty = true
  }

  private installObservers(): void {
    if (typeof PerformanceObserver === 'undefined') return
    const supported = Array.isArray(PerformanceObserver.supportedEntryTypes)
      ? PerformanceObserver.supportedEntryTypes
      : []

    if (supported.includes('long-animation-frame')) {
      this.loafSupported = true
      try {
        const loafObserver = new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            this.handleLoafEntry(entry as PerfLoafEntry)
          }
        })
        loafObserver.observe({ type: 'long-animation-frame', buffered: true })
        this.observers.push(loafObserver)
      } catch {
        this.loafSupported = false
      }
    }

    if (!this.loafSupported && supported.includes('longtask')) {
      try {
        const ltObserver = new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            this.longTaskCount++
            this.longTaskTotalMs += entry.duration
            if (entry.duration > this.longTaskMaxMs) this.longTaskMaxMs = entry.duration
            this.dirty = true
          }
        })
        ltObserver.observe({ type: 'longtask', buffered: true })
        this.observers.push(ltObserver)
      } catch {}
    }

    if (supported.includes('event')) {
      try {
        const eventObserver = new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            this.handleEventTimingEntry(entry as PerfEventTimingEntry)
          }
        })
        eventObserver.observe({
          type: 'event',
          durationThreshold: SLOW_INTERACTION_THRESHOLD_MS,
          buffered: true,
        } as PerformanceObserverInit)
        this.observers.push(eventObserver)
      } catch {}
    }
  }

  // Public for deterministic unit testing
  handleLoafEntry(entry: PerfLoafEntry): void {
    const duration = Number(entry.duration) || 0
    if (duration <= 0) return
    const blocking = Number(entry.blockingDuration) || Math.max(0, duration - 50)
    const styleMs = typeof entry.styleDuration === 'number' ? entry.styleDuration : 0
    const layoutMs = typeof entry.layoutDuration === 'number' ? entry.layoutDuration : 0
    let combinedStyleLayout = styleMs + layoutMs
    if (combinedStyleLayout === 0 && typeof entry.styleAndLayoutStart === 'number' && entry.styleAndLayoutStart > 0) {
      const end = typeof entry.paintTime === 'number' && entry.paintTime > 0
        ? entry.paintTime
        : entry.startTime + duration
      combinedStyleLayout = Math.max(0, end - entry.styleAndLayoutStart)
    }

    this.loafCount++
    this.loafTotalMs += duration
    if (duration > this.loafMaxMs) this.loafMaxMs = duration
    this.loafBlockingTotalMs += blocking
    if (blocking > this.loafBlockingMaxMs) this.loafBlockingMaxMs = blocking
    this.renderStyleMs += styleMs
    this.renderLayoutMs += layoutMs
    this.renderStyleAndLayoutMs += combinedStyleLayout

    let topScript = ''
    let topFn = ''
    let topScriptDuration = 0
    for (const script of entry.scripts || []) {
      const sDur = Number(script.duration) || 0
      const normUrl = normalizeScriptUrl(script.sourceURL)
      const invoker = (script.invokerType || 'unknown').slice(0, 32)
      const fn = (script.sourceFunctionName || '').slice(0, 48)
      if (sDur >= topScriptDuration) {
        topScriptDuration = sDur
        topScript = normUrl
        topFn = fn
      }
      const key = `${normUrl}|${invoker}|${fn}`
      let bucket = this.heavyScripts.get(key)
      if (!bucket) {
        bucket = {
          script: normUrl,
          invoker,
          fn,
          count: 0,
          totalMs: 0,
          maxMs: 0,
          forcedStyleMs: 0,
          forcedLayoutMs: 0,
          forcedStyleAndLayoutMs: 0,
        }
        this.heavyScripts.set(key, bucket)
      }
      bucket.count++
      bucket.totalMs += sDur
      if (sDur > bucket.maxMs) bucket.maxMs = sDur
      const fStyle = typeof script.forcedStyleDuration === 'number' ? script.forcedStyleDuration : 0
      const fLayout = typeof script.forcedLayoutDuration === 'number' ? script.forcedLayoutDuration : 0
      const fCombined = fStyle + fLayout > 0
        ? fStyle + fLayout
        : (typeof script.forcedStyleAndLayoutDuration === 'number' ? script.forcedStyleAndLayoutDuration : 0)
      bucket.forcedStyleMs += fStyle
      bucket.forcedLayoutMs += fLayout
      bucket.forcedStyleAndLayoutMs += fCombined
    }

    this.worstLoafs.push({
      ms: round1(duration),
      blockingMs: round1(blocking),
      styleMs: round1(styleMs),
      layoutMs: round1(layoutMs),
      styleAndLayoutMs: round1(combinedStyleLayout),
      route: this.currentRoute,
      topScript,
      topFn,
      at: new Date().toISOString(),
    })
    if (this.worstLoafs.length > MAX_WORST_LOAFS) {
      this.worstLoafs.sort((a, b) => b.ms - a.ms)
      this.worstLoafs.length = MAX_WORST_LOAFS
    }
    this.dirty = true
  }

  // Public for deterministic unit testing
  handleEventTimingEntry(entry: PerfEventTimingEntry): void {
    const interactionId = Number(entry.interactionId) || 0
    // Ignore hover/pointerover entries (interactionId === 0); INP only tracks discrete interactions.
    if (interactionId <= 0) return
    const duration = Number(entry.duration) || 0
    if (duration < SLOW_INTERACTION_THRESHOLD_MS) return
    const inputDelay = Math.max(0, (Number(entry.processingStart) || entry.startTime) - entry.startTime)
    const processing = Math.max(0, (Number(entry.processingEnd) || entry.processingStart) - (Number(entry.processingStart) || entry.startTime))
    const presentation = Math.max(0, entry.startTime + duration - (Number(entry.processingEnd) || entry.startTime))
    const sample: InteractionSample = {
      id: interactionId,
      type: entry.name || 'event',
      target: describeElementTarget(entry.target),
      route: this.currentRoute,
      ms: round1(duration),
      inputDelayMs: round1(inputDelay),
      processingMs: round1(processing),
      presentationDelayMs: round1(presentation),
      at: new Date().toISOString(),
    }

    const existing = this.interactionById.get(sample.id)
    if (!existing) {
      this.slowInteractionCount++
      if (duration >= 200) this.poorInteractionCount++
      this.interactionById.set(sample.id, sample)
    } else if (sample.ms > existing.ms) {
      if (existing.ms < 200 && duration >= 200) this.poorInteractionCount++
      this.interactionById.set(sample.id, sample)
    }
    this.dirty = true
  }

  private captureMemoryAndStore() {
    const perfWithMem = typeof performance !== 'undefined'
      ? (performance as Performance & { memory?: PerfMemoryInfo })
      : undefined
    const mem = perfWithMem?.memory
    const state = useAstrorderStore.getState()
    const messageBuckets = Object.values(state.messages)
    let cachedMessages = 0
    let cachedChars = 0
    for (const bucket of messageBuckets) {
      for (const msg of Object.values(bucket)) {
        cachedMessages++
        cachedChars += msg.text.length
      }
    }
    const queryStats = this.queryStatsProvider?.()
    const domNodes = typeof document !== 'undefined' ? document.getElementsByTagName('*').length : 0
    let domBreakdown: Record<string, number> | undefined
    if (domNodes > 8000 && typeof document !== 'undefined') {
      const countInside = (selector: string) => document.querySelector(selector)?.getElementsByTagName('*').length ?? 0
      domBreakdown = {
        transcript: countInside('.transcript'),
        sidebar: countInside('.workspace-sidebar'),
        sidecar: countInside('#session-sidecar, .desktop-sidecar, .workspace-changes-panel'),
        filetree_rows: document.querySelectorAll('.file-tree-row').length,
        code_spans: document.querySelectorAll('.markdown-code-block span').length,
        activity_packs: document.querySelectorAll('.activity-pack').length,
        svgs: document.getElementsByTagName('svg').length,
      }
    }
    return {
      js_heap_used_mb: mem ? round1(mem.usedJSHeapSize / (1024 * 1024)) : null,
      js_heap_total_mb: mem ? round1(mem.totalJSHeapSize / (1024 * 1024)) : null,
      js_heap_limit_mb: mem ? round1(mem.jsHeapSizeLimit / (1024 * 1024)) : null,
      dom_nodes: domNodes,
      ...(domBreakdown ? { dom_breakdown: domBreakdown } : {}),
      store: {
        sessions: Object.keys(state.sessions).length,
        agents: Object.keys(state.agents).length,
        cached_message_sessions: messageBuckets.length,
        cached_messages: cachedMessages,
        cached_message_chars_kb: round1(cachedChars / 1024),
        commands: Object.keys(state.commands).length,
        tasks: Object.keys(state.tasks).length,
        approvals: Object.keys(state.approvals).length,
        notifications: Object.keys(state.notifications).length,
        seen_event_ids: Object.keys(state.seenEventIds).length,
      },
      query_cache: queryStats ?? null,
    }
  }

  private captureNavigation() {
    if (typeof performance === 'undefined' || typeof performance.getEntriesByType !== 'function') {
      return { bootstrap_ms: this.bootstrapMs, first_session_ready_ms: this.firstSessionReadyMs }
    }
    const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined
    return {
      dom_interactive_ms: nav ? round1(nav.domInteractive) : null,
      dom_content_loaded_ms: nav ? round1(nav.domContentLoadedEventEnd) : null,
      load_ms: nav && nav.loadEventEnd > 0 ? round1(nav.loadEventEnd) : null,
      bootstrap_ms: this.bootstrapMs,
      first_session_ready_ms: this.firstSessionReadyMs,
    }
  }

  buildSnapshot(): Record<string, unknown> {
    const allInteractions = [
      ...this.interactionById.values(),
      ...this.ungroupedInteractions,
    ].sort((a, b) => b.ms - a.ms)
    const interactionDurations = allInteractions.map((item) => item.ms)

    const apiSummary = [...this.apiRoutes.entries()]
      .map(([route, b]) => ({
        route,
        count: b.count,
        errors: b.errors,
        p50_ms: percentile(b.durations, 50),
        p95_ms: percentile(b.durations, 95),
        max_ms: round1(b.maxMs),
        avg_kb: b.count > 0 ? round1(b.totalBytes / b.count / 1024) : 0,
        max_kb: round1(b.maxBytes / 1024),
      }))
      .sort((a, b) => b.p95_ms - a.p95_ms)
      .slice(0, 20)

    const topScripts = [...this.heavyScripts.values()]
      .map((s) => ({
        script: s.script,
        invoker: s.invoker,
        fn: s.fn,
        count: s.count,
        total_ms: round1(s.totalMs),
        max_ms: round1(s.maxMs),
        forced_style_ms: round1(s.forcedStyleMs),
        forced_layout_ms: round1(s.forcedLayoutMs),
        forced_style_layout_ms: round1(s.forcedStyleAndLayoutMs),
      }))
      .sort((a, b) => b.total_ms - a.total_ms)
      .slice(0, 12)

    const wsTopTypes = [...this.wsEventsByType.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 12)
      .reduce<Record<string, number>>((acc, [k, v]) => {
        acc[k] = v
        return acc
      }, {})

    const reactHotspots = [...this.profilerStats.values()]
      .map((p) => ({
        id: p.id,
        commits: p.commits,
        mounts: p.mounts,
        total_ms: round1(p.totalActualMs),
        max_ms: round1(p.maxActualMs),
        slow_16ms: p.slowCommits16Ms,
        max_base_ms: round1(p.maxBaseMs),
      }))
      .sort((a, b) => b.total_ms - a.total_ms)

    return {
      schema_version: 1,
      client_id: this.clientId,
      surface: detectSurface(),
      route: this.currentRoute,
      viewport: typeof window !== 'undefined' ? `${window.innerWidth}x${window.innerHeight}` : '0x0',
      dpr: typeof window !== 'undefined' ? round1(window.devicePixelRatio || 1) : 1,
      window_seq: this.windowSeq,
      window_duration_s: round1((Date.now() - this.windowStartedAtMs) / 1000),
      uptime_s: round1((Date.now() - this.startedAtMs) / 1000),
      captured_at: new Date().toISOString(),
      startup: this.captureNavigation(),
      memory: this.captureMemoryAndStore(),
      loaf: {
        supported: this.loafSupported,
        count: this.loafCount,
        total_ms: round1(this.loafTotalMs),
        max_ms: round1(this.loafMaxMs),
        blocking_total_ms: round1(this.loafBlockingTotalMs),
        blocking_max_ms: round1(this.loafBlockingMaxMs),
        render_style_ms: round1(this.renderStyleMs),
        render_layout_ms: round1(this.renderLayoutMs),
        render_style_and_layout_ms: round1(this.renderStyleAndLayoutMs),
        heavy_scripts: topScripts,
        worst_frames: [...this.worstLoafs].sort((a, b) => b.ms - a.ms).slice(0, MAX_WORST_LOAFS),
      },
      long_tasks: this.loafSupported
        ? null
        : {
            count: this.longTaskCount,
            total_ms: round1(this.longTaskTotalMs),
            max_ms: round1(this.longTaskMaxMs),
          },
      interactions: {
        slow_count: this.slowInteractionCount,
        poor_200ms_count: this.poorInteractionCount,
        p98_ms: percentile(interactionDurations, 98),
        max_ms: interactionDurations[0] ?? 0,
        worst: allInteractions.slice(0, MAX_WORST_INTERACTIONS),
      },
      api: {
        routes: apiSummary,
        slow_requests: [...this.slowApis].sort((a, b) => b.ms - a.ms).slice(0, MAX_SLOW_APIS),
      },
      ws: {
        status: this.wsStatus,
        events_total: this.wsEventsTotal,
        peak_events_per_sec: this.wsPeakEventsPerSec,
        delta_flushes: this.wsDeltaFlushes,
        delta_batch_max: this.wsDeltaBatchMax,
        apply_total_ms: round1(this.wsApplyTotalMs),
        apply_max_ms: round1(this.wsApplyMaxMs),
        reconnects: this.wsReconnects,
        by_type: wsTopTypes,
      },
      session_switches: {
        count: this.switchDurations.length,
        p50_ms: percentile(this.switchDurations, 50),
        p95_ms: percentile(this.switchDurations, 95),
        max_ms: this.switchDurations.length ? round1(Math.max(...this.switchDurations)) : 0,
        recent: this.recentSwitches,
      },
      react_profiler: reactHotspots,
    }
  }

  private resetWindow(): void {
    this.windowSeq++
    this.windowStartedAtMs = Date.now()
    this.dirty = false
    this.loafCount = 0
    this.loafTotalMs = 0
    this.loafMaxMs = 0
    this.loafBlockingTotalMs = 0
    this.loafBlockingMaxMs = 0
    this.renderStyleMs = 0
    this.renderLayoutMs = 0
    this.renderStyleAndLayoutMs = 0
    this.heavyScripts.clear()
    this.worstLoafs = []
    this.longTaskCount = 0
    this.longTaskTotalMs = 0
    this.longTaskMaxMs = 0
    this.interactionById.clear()
    this.ungroupedInteractions = []
    this.slowInteractionCount = 0
    this.poorInteractionCount = 0
    this.apiRoutes.clear()
    this.slowApis = []
    this.wsEventsTotal = 0
    this.wsEventsByType.clear()
    this.wsPeakEventsPerSec = 0
    this.wsDeltaFlushes = 0
    this.wsDeltaBatchMax = 0
    this.wsApplyTotalMs = 0
    this.wsApplyMaxMs = 0
    this.wsReconnects = 0
    this.switchDurations = []
    this.recentSwitches = []
    this.profilerStats.clear()
  }

  private buildHeaders(): Record<string, string> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    try {
      if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
        const token = localStorage.getItem('astrorder:token')
        if (token) headers.Authorization = `Bearer ${token}`
      }
    } catch {}
    return headers
  }

  private encodeCompactPayload(): string {
    const snapshot = this.buildSnapshot()
    let encoded = JSON.stringify(snapshot)
    if (encoded.length > MAX_PAYLOAD_BYTES) {
      // Trim verbose detail arrays if payload ever grows close to fetchLater 64KB quota.
      const loaf = snapshot.loaf as { worst_frames?: unknown[]; heavy_scripts?: unknown[] }
      const interactions = snapshot.interactions as { worst?: unknown[] }
      const apiSection = snapshot.api as { slow_requests?: unknown[] }
      if (loaf.worst_frames) loaf.worst_frames = loaf.worst_frames.slice(0, 5)
      if (loaf.heavy_scripts) loaf.heavy_scripts = loaf.heavy_scripts.slice(0, 6)
      if (interactions.worst) interactions.worst = interactions.worst.slice(0, 5)
      if (apiSection.slow_requests) apiSection.slow_requests = apiSection.slow_requests.slice(0, 6)
      encoded = JSON.stringify(snapshot)
    }
    return encoded
  }

  private onTick(): void {
    if (!this.authenticated) return
    if (this.pendingBeaconResult?.activated) {
      this.pendingBeaconController = null
      this.pendingBeaconResult = null
      this.resetWindow()
    }
    const elapsed = Date.now() - this.windowStartedAtMs
    if (elapsed >= FLUSH_INTERVAL_MS && this.dirty) {
      this.flushNow()
      return
    }
    if (this.dirty) {
      this.refreshDeferredBeacon(false)
    }
  }

  private refreshDeferredBeacon(immediate: boolean): boolean {
    if (!this.authenticated) return false
    const send = ensureFetchLater()
    if (!send) return false

    if (this.pendingBeaconResult?.activated) {
      this.pendingBeaconController = null
      this.pendingBeaconResult = null
      this.resetWindow()
    }
    if (this.pendingBeaconController) {
      this.pendingBeaconController.abort()
      this.pendingBeaconController = null
      this.pendingBeaconResult = null
    }

    const controller = new AbortController()
    const body = this.encodeCompactPayload()
    const remainingMs = immediate
      ? 0
      : Math.max(1000, FLUSH_INTERVAL_MS - (Date.now() - this.windowStartedAtMs))

    try {
      const result = send(TELEMETRY_ENDPOINT, {
        method: 'POST',
        headers: this.buildHeaders(),
        body,
        credentials: 'include',
        signal: controller.signal,
        activateAfter: remainingMs,
      })
      if (immediate) {
        this.resetWindow()
      } else {
        this.pendingBeaconController = controller
        this.pendingBeaconResult = result
      }
      return true
    } catch {
      return false
    }
  }

  flushNow(): boolean {
    return this.refreshDeferredBeacon(true)
  }
}

export const clientPerf = new ClientPerfCollector()

export function startClientPerf(options?: { getQueryStats?: () => QueryCacheStats }): void {
  if (import.meta.env.MODE === 'test') return
  clientPerf.start(options)
}

const handleProfilerRender: ProfilerOnRenderCallback = (profilerId, phase, actualDuration, baseDuration) => {
  clientPerf.recordReactCommit(profilerId, phase, actualDuration, baseDuration)
}

export function PerfProfiler({ id, children }: { id: string; children: ReactNode }) {
  return (
    <Profiler id={id} onRender={handleProfilerRender}>
      {children}
    </Profiler>
  )
}
