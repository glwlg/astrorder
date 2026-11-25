import { useSyncExternalStore } from 'react'

/** Duration of zero user interaction before background polling pauses (15 minutes). */
export const USER_IDLE_TIMEOUT_MS = 15 * 60_000
const POINTER_MOVE_THROTTLE_MS = 30_000

type IdleListener = (idle: boolean) => void

let idleByTimeout = false
let idleTimer: ReturnType<typeof setTimeout> | null = null
let lastPointerMoveAt = 0
let attached = false
const listeners = new Set<IdleListener>()

function isDocumentHidden(): boolean {
  if (typeof document === 'undefined') return false
  return Boolean(document.hidden || document.visibilityState === 'hidden')
}

export function isUserIdle(): boolean {
  return idleByTimeout || isDocumentHidden()
}

let lastEmitted = isUserIdle()

function emitIfChanged() {
  const next = isUserIdle()
  if (next === lastEmitted) return
  lastEmitted = next
  for (const listener of [...listeners]) {
    listener(next)
  }
}

function clearIdleTimer() {
  if (idleTimer !== null) {
    clearTimeout(idleTimer)
    idleTimer = null
  }
}

function armIdleTimer() {
  clearIdleTimer()
  if (isDocumentHidden()) return
  idleTimer = setTimeout(() => {
    idleTimer = null
    idleByTimeout = true
    emitIfChanged()
  }, USER_IDLE_TIMEOUT_MS)
}

function markActive() {
  const wasIdleByTimeout = idleByTimeout
  idleByTimeout = false
  armIdleTimer()
  if (wasIdleByTimeout || lastEmitted !== isUserIdle()) {
    emitIfChanged()
  }
}

function onPointerMove() {
  const now = Date.now()
  if (!idleByTimeout && now >= lastPointerMoveAt && now - lastPointerMoveAt < POINTER_MOVE_THROTTLE_MS) {
    return
  }
  lastPointerMoveAt = now
  markActive()
}

function onVisibilityChange() {
  if (isDocumentHidden()) {
    clearIdleTimer()
    emitIfChanged()
    return
  }
  idleByTimeout = false
  lastPointerMoveAt = Date.now()
  armIdleTimer()
  emitIfChanged()
}

function attach() {
  if (attached || typeof window === 'undefined' || typeof document === 'undefined') return
  attached = true
  idleByTimeout = false
  lastPointerMoveAt = Date.now()
  lastEmitted = isUserIdle()
  armIdleTimer()
  window.addEventListener('pointerdown', markActive, { passive: true, capture: true })
  window.addEventListener('keydown', markActive, { passive: true, capture: true })
  window.addEventListener('wheel', markActive, { passive: true, capture: true })
  window.addEventListener('touchstart', markActive, { passive: true, capture: true })
  window.addEventListener('focus', markActive)
  window.addEventListener('pointermove', onPointerMove, { passive: true, capture: true })
  document.addEventListener('visibilitychange', onVisibilityChange)
}

function detach() {
  if (!attached || typeof window === 'undefined' || typeof document === 'undefined') return
  attached = false
  clearIdleTimer()
  window.removeEventListener('pointerdown', markActive, true)
  window.removeEventListener('keydown', markActive, true)
  window.removeEventListener('wheel', markActive, true)
  window.removeEventListener('touchstart', markActive, true)
  window.removeEventListener('focus', markActive)
  window.removeEventListener('pointermove', onPointerMove, true)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  idleByTimeout = false
  lastPointerMoveAt = 0
  lastEmitted = isUserIdle()
}

export function subscribeUserIdle(listener: IdleListener): () => void {
  listeners.add(listener)
  if (listeners.size === 1) attach()
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) detach()
  }
}

function subscribeStore(onStoreChange: () => void): () => void {
  return subscribeUserIdle(() => onStoreChange())
}

export function useUserIdle(): boolean {
  return useSyncExternalStore(subscribeStore, isUserIdle, () => false)
}

export function resetUserIdleForTest(): void {
  clearIdleTimer()
  if (attached) detach()
  listeners.clear()
  idleByTimeout = false
  lastPointerMoveAt = 0
  lastEmitted = isUserIdle()
}
