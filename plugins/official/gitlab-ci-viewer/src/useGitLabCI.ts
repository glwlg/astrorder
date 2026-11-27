import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import type { Session } from '@astrorder/sdk'
import type { GitLabApiClient, GitLabPipelinesResponse } from './api'

const ACTIVE_PIPELINE_STATUSES = new Set([
  'running',
  'pending',
  'created',
  'preparing',
  'waiting_for_resource',
  'scheduled',
])

const USER_IDLE_TIMEOUT_MS = 15 * 60_000
const POINTER_MOVE_THROTTLE_MS = 30_000
const PIPELINES_STALE_TIME_MS = 8_000

export function isActiveGitLabStatus(status?: string | null): boolean {
  if (!status) return false
  return ACTIVE_PIPELINE_STATUSES.has(status.trim().toLowerCase())
}

export interface GitLabStatusMeta {
  label: string
  dotColor: string
  tone: 'success' | 'running' | 'pending' | 'failed' | 'manual' | 'muted'
  spinning: boolean
}

export function gitLabStatusMeta(status?: string | null): GitLabStatusMeta {
  switch ((status || '').trim().toLowerCase()) {
    case 'success':
      return { label: '通过', dotColor: '#2f9e44', tone: 'success', spinning: false }
    case 'running':
      return { label: '运行中', dotColor: '#228be6', tone: 'running', spinning: true }
    case 'pending':
    case 'created':
    case 'preparing':
    case 'waiting_for_resource':
    case 'scheduled':
      return { label: '排队中', dotColor: '#d97706', tone: 'pending', spinning: true }
    case 'failed':
      return { label: '失败', dotColor: '#e03131', tone: 'failed', spinning: false }
    case 'canceled':
    case 'canceling':
      return { label: '已取消', dotColor: '#868e96', tone: 'muted', spinning: false }
    case 'manual':
      return { label: '待手动', dotColor: '#5c7cfa', tone: 'manual', spinning: false }
    case 'skipped':
      return { label: '已跳过', dotColor: '#adb5bd', tone: 'muted', spinning: false }
    default:
      return { label: status || '未知', dotColor: '#868e96', tone: 'muted', spinning: false }
  }
}

export function formatPipelineDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return '--'
  const rounded = Math.round(seconds)
  if (rounded < 60) return `${rounded}s`
  const mins = Math.floor(rounded / 60)
  const rem = rounded % 60
  if (mins < 60) return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`
  const hours = Math.floor(mins / 60)
  const remMins = mins % 60
  return remMins > 0 ? `${hours}h ${remMins}m` : `${hours}h`
}

export function formatRelativeTime(iso?: string): string {
  if (!iso) return ''
  const ts = Date.parse(iso)
  if (Number.isNaN(ts)) return ''
  const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000))
  if (diffSec < 60) return '刚刚'
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin} 分钟前`
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return `${diffHour} 小时前`
  const diffDay = Math.floor(diffHour / 24)
  if (diffDay < 30) return `${diffDay} 天前`
  return new Date(ts).toLocaleDateString()
}

// Lightweight internal invalidation bus replacing React Query invalidateQueries
const invalidationListeners = new Set<() => void>()

export function invalidateGitLabPipelines(): void {
  for (const listener of [...invalidationListeners]) {
    try {
      listener()
    } catch {
      // Ignore individual listener failures
    }
  }
}

export function subscribeGitLabPipelinesInvalidation(listener: () => void): () => void {
  invalidationListeners.add(listener)
  return () => {
    invalidationListeners.delete(listener)
  }
}

// Lightweight user-idle & document-visibility monitor (15-min timeout)
let idleByTimeout = false
let idleTimer: ReturnType<typeof setTimeout> | null = null
let lastPointerMoveAt = 0
let idleAttached = false
const idleListeners = new Set<() => void>()

function isDocumentHidden(): boolean {
  if (typeof document === 'undefined') return false
  return Boolean(document.hidden || document.visibilityState === 'hidden')
}

function getUserIdleSnapshot(): boolean {
  return idleByTimeout || isDocumentHidden()
}

let lastIdleEmitted = getUserIdleSnapshot()

function emitIdleIfChanged(): void {
  const next = getUserIdleSnapshot()
  if (next === lastIdleEmitted) return
  lastIdleEmitted = next
  for (const listener of [...idleListeners]) {
    listener()
  }
}

function clearIdleTimer(): void {
  if (idleTimer !== null) {
    clearTimeout(idleTimer)
    idleTimer = null
  }
}

function armIdleTimer(): void {
  clearIdleTimer()
  if (isDocumentHidden()) return
  idleTimer = setTimeout(() => {
    idleTimer = null
    idleByTimeout = true
    emitIdleIfChanged()
  }, USER_IDLE_TIMEOUT_MS)
}

function markUserActive(): void {
  const wasIdle = idleByTimeout
  idleByTimeout = false
  armIdleTimer()
  if (wasIdle || lastIdleEmitted !== getUserIdleSnapshot()) {
    emitIdleIfChanged()
  }
}

function onPointerMove(): void {
  const now = Date.now()
  if (!idleByTimeout && now >= lastPointerMoveAt && now - lastPointerMoveAt < POINTER_MOVE_THROTTLE_MS) {
    return
  }
  lastPointerMoveAt = now
  markUserActive()
}

function onVisibilityChange(): void {
  if (isDocumentHidden()) {
    clearIdleTimer()
    emitIdleIfChanged()
    return
  }
  idleByTimeout = false
  lastPointerMoveAt = Date.now()
  armIdleTimer()
  emitIdleIfChanged()
}

function attachIdleListeners(): void {
  if (idleAttached || typeof window === 'undefined' || typeof document === 'undefined') return
  idleAttached = true
  idleByTimeout = false
  lastPointerMoveAt = Date.now()
  lastIdleEmitted = getUserIdleSnapshot()
  armIdleTimer()
  window.addEventListener('pointerdown', markUserActive, { passive: true, capture: true })
  window.addEventListener('keydown', markUserActive, { passive: true, capture: true })
  window.addEventListener('wheel', markUserActive, { passive: true, capture: true })
  window.addEventListener('touchstart', markUserActive, { passive: true, capture: true })
  window.addEventListener('focus', markUserActive)
  window.addEventListener('pointermove', onPointerMove, { passive: true, capture: true })
  document.addEventListener('visibilitychange', onVisibilityChange)
}

function detachIdleListeners(): void {
  if (!idleAttached || typeof window === 'undefined' || typeof document === 'undefined') return
  idleAttached = false
  clearIdleTimer()
  window.removeEventListener('pointerdown', markUserActive, true)
  window.removeEventListener('keydown', markUserActive, true)
  window.removeEventListener('wheel', markUserActive, true)
  window.removeEventListener('touchstart', markUserActive, true)
  window.removeEventListener('focus', markUserActive)
  window.removeEventListener('pointermove', onPointerMove, true)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  idleByTimeout = false
  lastPointerMoveAt = 0
  lastIdleEmitted = getUserIdleSnapshot()
}

function subscribeUserIdle(onStoreChange: () => void): () => void {
  idleListeners.add(onStoreChange)
  if (idleListeners.size === 1) {
    attachIdleListeners()
  }
  return () => {
    idleListeners.delete(onStoreChange)
    if (idleListeners.size === 0) {
      detachIdleListeners()
    }
  }
}

export function useUserIdle(): boolean {
  return useSyncExternalStore(subscribeUserIdle, getUserIdleSnapshot, () => false)
}

function computeNextPollIntervalMs(data: GitLabPipelinesResponse | null): number {
  if (!data) return 60_000
  if (!data.configured) return 120_000
  if (!data.matched) return 120_000
  if (isActiveGitLabStatus(data.latest_pipeline?.status)) return 10_000
  return 45_000
}

export interface UseGitLabPipelinesResult {
  data: GitLabPipelinesResponse | undefined
  isLoading: boolean
  error: Error | null
  refetch: () => Promise<void>
}

export function useGitLabPipelines(
  api: GitLabApiClient,
  session: Pick<Session, 'id' | 'agent_id' | 'workspace' | 'connection_id'>,
  options?: { enabled?: boolean; limit?: number; ref?: string },
): UseGitLabPipelinesResult {
  const idle = useUserIdle()
  const enabled = Boolean(session.workspace) && (options?.enabled ?? true)
  const limit = options?.limit ?? 8
  const refFilter = options?.ref ?? ''

  const queryKey = `${session.agent_id}::${session.id}::${session.workspace || ''}::${session.connection_id || ''}::${refFilter}::${limit}`

  const [data, setData] = useState<GitLabPipelinesResponse | undefined>(undefined)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [invalidationTick, setInvalidationTick] = useState(0)

  const latestDataRef = useRef<GitLabPipelinesResponse | null>(null)
  const lastFetchedAtRef = useRef(0)
  const lastQueryKeyRef = useRef(queryKey)
  const abortRef = useRef<AbortController | null>(null)
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const idleRef = useRef(idle)
  idleRef.current = idle

  useEffect(() => {
    if (lastQueryKeyRef.current !== queryKey) {
      lastQueryKeyRef.current = queryKey
      latestDataRef.current = null
      lastFetchedAtRef.current = 0
      setData(undefined)
      setError(null)
    }
  }, [queryKey])

  useEffect(() => {
    return subscribeGitLabPipelinesInvalidation(() => {
      lastFetchedAtRef.current = 0
      setInvalidationTick((t) => t + 1)
    })
  }, [])

  const clearPollTimer = useCallback(() => {
    if (pollTimerRef.current !== null) {
      clearTimeout(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }, [])

  const fetchOnce = useCallback(async () => {
    if (!enabled || !session.workspace) return
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setIsLoading(true)
    try {
      const resp = await api.listPipelines(
        {
          workspace: session.workspace || '',
          session_id: session.id,
          connection_id: session.connection_id || undefined,
          ref: refFilter || undefined,
          limit,
        },
        controller.signal,
      )
      if (controller.signal.aborted) return
      latestDataRef.current = resp
      lastFetchedAtRef.current = Date.now()
      setData(resp)
      setError(null)
    } catch (err) {
      if (controller.signal.aborted) return
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      if (!controller.signal.aborted) {
        setIsLoading(false)
      }
    }
  }, [api, enabled, limit, refFilter, session.connection_id, session.id, session.workspace])

  useEffect(() => {
    clearPollTimer()
    if (!enabled) {
      abortRef.current?.abort()
      return
    }

    let cancelled = false

    const scheduleNext = () => {
      if (cancelled || idleRef.current || isDocumentHidden()) return
      const delayMs = computeNextPollIntervalMs(latestDataRef.current)
      pollTimerRef.current = setTimeout(() => {
        if (cancelled || idleRef.current || isDocumentHidden()) return
        void fetchOnce().finally(() => {
          if (!cancelled) {
            scheduleNext()
          }
        })
      }, delayMs)
    }

    if (idle) {
      return () => {
        cancelled = true
        clearPollTimer()
      }
    }

    const isFresh =
      lastFetchedAtRef.current > 0 &&
      Date.now() - lastFetchedAtRef.current < PIPELINES_STALE_TIME_MS &&
      latestDataRef.current !== null

    if (isFresh) {
      scheduleNext()
    } else {
      void fetchOnce().finally(() => {
        if (!cancelled) {
          scheduleNext()
        }
      })
    }

    return () => {
      cancelled = true
      clearPollTimer()
      abortRef.current?.abort()
    }
  }, [clearPollTimer, enabled, fetchOnce, idle, invalidationTick, queryKey])

  return {
    data,
    isLoading,
    error,
    refetch: fetchOnce,
  }
}
