export type SessionSwipeDirection = -1 | 1
export type SessionCardCut = 'next-up' | 'next-down' | 'previous-up' | 'previous-down'
export type SessionSwipeGesture = {
  direction: SessionSwipeDirection
  vertical: -1 | 1
  cut: SessionCardCut
}
export type SessionCardPose = {
  x: number
  y: number
  rotateX: number
  rotateY: number
  rotateZ: number
  z: number
  scale: number
  origin: string
}

export const EDGE_START_MAX_X = 36
const EDGE_OPEN_THRESHOLD = 48
const DRAWER_CLOSE_THRESHOLD = 96
const SESSION_SWIPE_THRESHOLD = 90
const SESSION_DIAGONAL_MIN_Y = 28
const SESSION_DIAGONAL_MAX_SLOPE = 1.15
const MAX_DIAGONAL_DRIFT = 180

export function hapticFeedback(pattern: number | number[] = 15): void {
  try {
    if (typeof navigator !== 'undefined' && 'vibrate' in navigator) {
      navigator.vibrate(pattern)
    }
  } catch {}
}

function isDirectionalSwipe(startX: number, startY: number, endX: number, endY: number, threshold: number): boolean {
  const dx = endX - startX
  const dy = endY - startY
  return Math.abs(dx) >= threshold && Math.abs(dx) >= Math.abs(dy) * 0.55 && Math.abs(dy) <= MAX_DIAGONAL_DRIFT
}

export function startsAtSessionDrawerEdge(startX: number): boolean {
  return startX <= EDGE_START_MAX_X
}

/** A bezel gesture is reserved for opening the session drawer. */
export function opensSessionDrawerFromEdge(startX: number, startY: number, endX: number, endY: number): boolean {
  const dx = endX - startX
  const dy = endY - startY
  return startsAtSessionDrawerEdge(startX) && dx >= EDGE_OPEN_THRESHOLD && Math.abs(dx) >= Math.abs(dy) * 0.95 && Math.abs(dy) <= 80
}

/** Close the left drawer only with a clearly horizontal leftward swipe. Vertical list scrolling must not close it. */
export function closesSessionDrawerFromSwipe(startX: number, startY: number, endX: number, endY: number): boolean {
  const dx = endX - startX
  const dy = endY - startY
  return dx <= -DRAWER_CLOSE_THRESHOLD && Math.abs(dx) >= Math.abs(dy) * 1.6 && Math.abs(dy) <= 72
}

export function sessionCardCut(dx: number, dy: number): SessionCardCut {
  const next = dx < 0
  const down = dy >= 0
  return `${next ? 'next' : 'previous'}-${down ? 'down' : 'up'}`
}

/** Leave travels with the finger; enter comes from the opposite corner. */
export function sessionCardPose(cut: SessionCardCut, role: 'leave' | 'enter'): SessionCardPose {
  const left = cut.startsWith('next')
  const down = cut.endsWith('down')
  const leaveX = left ? -188 : 188
  const leaveY = down ? 148 : -148
  const x = role === 'leave' ? leaveX : -leaveX
  const y = role === 'leave' ? leaveY : -leaveY
  return {
    x,
    y,
    rotateX: (y < 0 ? -1 : 1) * (role === 'leave' ? 18 : -16),
    rotateY: (x < 0 ? 1 : -1) * (role === 'leave' ? 38 : 34),
    rotateZ: (x < 0 ? -1 : 1) * (down ? 12 : -12),
    z: role === 'leave' ? -120 : 90,
    scale: role === 'leave' ? 0.86 : 0.9,
    origin: `${left ? 88 : 12}% ${down ? 16 : 84}%`,
  }
}

/**
 * Returns +1 for the next open session (left swipe), -1 for the previous one.
 * Bezel gestures never become session navigation gestures.
 */
export function sessionSwipeGesture(startX: number, startY: number, endX: number, endY: number): SessionSwipeGesture | null {
  if (startsAtSessionDrawerEdge(startX) || !isDirectionalSwipe(startX, startY, endX, endY, SESSION_SWIPE_THRESHOLD)) return null
  const dx = endX - startX
  const dy = endY - startY
  if (Math.abs(dy) < SESSION_DIAGONAL_MIN_Y || Math.abs(dy) > Math.abs(dx) * SESSION_DIAGONAL_MAX_SLOPE) return null
  const cut = sessionCardCut(dx, dy)
  return {
    direction: dx < 0 ? 1 : -1,
    vertical: dy >= 0 ? 1 : -1,
    cut,
  }
}

export function sessionDragPreview(dx: number, dy: number): SessionCardPose | null {
  if (Math.abs(dx) < 18 || Math.abs(dx) < Math.abs(dy) * 0.45) return null
  const leave = sessionCardPose(sessionCardCut(dx, dy), 'leave')
  const progress = Math.min(1, Math.hypot(dx, dy) / 220)
  return {
    x: dx,
    y: dy,
    rotateX: leave.rotateX * progress,
    rotateY: leave.rotateY * progress,
    rotateZ: leave.rotateZ * progress,
    z: leave.z * progress,
    scale: 1 - (1 - leave.scale) * progress,
    origin: leave.origin,
  }
}

/** Compatibility helper for callers that only need the horizontal direction. */
export function sessionSwipeDirection(startX: number, startY: number, endX: number, endY: number): SessionSwipeDirection | null {
  return sessionSwipeGesture(startX, startY, endX, endY)?.direction ?? null
}

const NATIVE_HOLD_FIELDS = 'textarea, input, select, [contenteditable="true"], .m-select-text'

export function allowsNativeTextHold(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false
  if (target.closest('.m-msg, .m-bubble, .m-hold')) return false
  return Boolean(target.closest(NATIVE_HOLD_FIELDS))
}

/** Block the browser callout/context menu so custom long-press can run. Never stopPropagation — our handlers still need the event. */
export function suppressNativeHold(event: Event): void {
  if (allowsNativeTextHold(event.target)) return
  event.preventDefault()
}

/**
 * Global guard: cancels the browser long-press menu *only* when a touch gesture
 * immediately precedes it (touch long-press on Android/iOS emits a synthetic
 * contextmenu). Desktop right-click — no touchstart — is left alone.
 */
export function installGlobalNativeHoldBlocker(root: Pick<Document, 'addEventListener' | 'removeEventListener'> = document): () => void {
  let touchTarget: EventTarget | null = null
  const reset = (): void => { touchTarget = null }
  const onTouchStart = (event: Event): void => { touchTarget = event.target }
  const onContextMenu = (event: Event): void => {
    if (touchTarget === null) return
    if (allowsNativeTextHold(event.target)) return
    event.preventDefault()
  }
  root.addEventListener('touchstart', onTouchStart, { capture: true, passive: true } as AddEventListenerOptions)
  root.addEventListener('touchend', reset, { capture: true, passive: true } as AddEventListenerOptions)
  root.addEventListener('touchcancel', reset, { capture: true, passive: true } as AddEventListenerOptions)
  root.addEventListener('contextmenu', onContextMenu, { capture: true } as AddEventListenerOptions)
  const onGesture = (event: Event): void => { event.preventDefault() }
  root.addEventListener('gesturestart', onGesture, { capture: true, passive: false } as AddEventListenerOptions)
  root.addEventListener('gesturechange', onGesture, { capture: true, passive: false } as AddEventListenerOptions)
  root.addEventListener('gestureend', onGesture, { capture: true, passive: false } as AddEventListenerOptions)
  return () => {
    root.removeEventListener('touchstart', onTouchStart, { capture: true } as EventListenerOptions)
    root.removeEventListener('touchend', reset, { capture: true } as EventListenerOptions)
    root.removeEventListener('touchcancel', reset, { capture: true } as EventListenerOptions)
    root.removeEventListener('contextmenu', onContextMenu, { capture: true } as EventListenerOptions)
    root.removeEventListener('gesturestart', onGesture, { capture: true } as EventListenerOptions)
    root.removeEventListener('gesturechange', onGesture, { capture: true } as EventListenerOptions)
    root.removeEventListener('gestureend', onGesture, { capture: true } as EventListenerOptions)
  }
}

export function queueSwipeZone(startY: number, y: number): 'send' | 'edit' | null {
  const dy = y - startY
  if (dy < 0) return 'send'
  if (dy > 0) return 'edit'
  return null
}
