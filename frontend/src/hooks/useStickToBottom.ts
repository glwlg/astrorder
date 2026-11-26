import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { isNearBottom } from '../domain/semantics'

interface StickOptions {
  contentVersion: string | number
  forceFollowVersion?: string | number
}

const FOLLOW_BOTTOM_THRESHOLD = 48
const UPWARD_GESTURE_LOCK_MS = 400

function nowMs(): number {
  return typeof performance !== 'undefined' ? performance.now() : Date.now()
}

export function useStickToBottom<T extends HTMLElement>({ contentVersion, forceFollowVersion }: StickOptions) {
  const containerRef = useRef<T | null>(null)
  const contentRef = useRef<HTMLElement | null>(null)
  const [containerEl, setContainerEl] = useState<T | null>(null)
  const [contentEl, setContentEl] = useState<HTMLElement | null>(null)
  const followRef = useRef(true)
  const programmaticScrollRef = useRef(false)
  const programmaticRafRef = useRef<number | null>(null)
  const programmaticTimerRef = useRef<number | null>(null)
  const prependHeightRef = useRef<number | null>(null)
  const previousForceFollowRef = useRef(forceFollowVersion)
  const lastScrollTopRef = useRef(0)
  const lastScrollHeightRef = useRef(0)
  const lastClientHeightRef = useRef(0)
  const lastUserUpwardAtRef = useRef(-Infinity)
  const touchStartYRef = useRef<number | null>(null)
  const [following, setFollowing] = useState(true)

  const setContainerRef = useCallback((element: T | null) => {
    containerRef.current = element
    setContainerEl(element)
    if (element) {
      lastScrollTopRef.current = element.scrollTop
      lastScrollHeightRef.current = element.scrollHeight
      lastClientHeightRef.current = element.clientHeight
    }
  }, [])

  const setContentRef = useCallback((element: HTMLElement | null) => {
    contentRef.current = element
    setContentEl(element)
  }, [])

  const cancelProgrammaticFinish = useCallback(() => {
    if (programmaticRafRef.current !== null && typeof window.cancelAnimationFrame === 'function') {
      window.cancelAnimationFrame(programmaticRafRef.current)
      programmaticRafRef.current = null
    }
    if (programmaticTimerRef.current !== null) {
      window.clearTimeout(programmaticTimerRef.current)
      programmaticTimerRef.current = null
    }
  }, [])

  const finishProgrammaticScroll = useCallback(() => {
    cancelProgrammaticFinish()
    const callback = () => {
      programmaticRafRef.current = null
      programmaticTimerRef.current = null
      programmaticScrollRef.current = false
      const element = containerRef.current
      if (element) {
        lastScrollTopRef.current = element.scrollTop
        lastScrollHeightRef.current = element.scrollHeight
        lastClientHeightRef.current = element.clientHeight
      }
    }
    if (typeof window.requestAnimationFrame === 'function') {
      programmaticRafRef.current = window.requestAnimationFrame(callback)
    } else {
      programmaticTimerRef.current = window.setTimeout(callback, 0)
    }
  }, [cancelProgrammaticFinish])

  useEffect(() => () => cancelProgrammaticFinish(), [cancelProgrammaticFinish])

  const scrollToBottom = useCallback(
    () => {
      const element = containerRef.current
      if (!element) return
      lastUserUpwardAtRef.current = -Infinity
      followRef.current = true
      setFollowing(true)
      programmaticScrollRef.current = true
      if (typeof element.scrollTo === 'function') {
        element.scrollTo({ top: element.scrollHeight, behavior: 'auto' })
      } else {
        element.scrollTop = element.scrollHeight
      }
      lastScrollTopRef.current = element.scrollTop
      lastScrollHeightRef.current = element.scrollHeight
      lastClientHeightRef.current = element.clientHeight
      finishProgrammaticScroll()
    },
    [finishProgrammaticScroll],
  )

  useLayoutEffect(() => {
    const element = containerRef.current
    if (!element) return

    if (prependHeightRef.current !== null) {
      const delta = element.scrollHeight - prependHeightRef.current
      programmaticScrollRef.current = true
      element.scrollTop += delta
      lastScrollTopRef.current = element.scrollTop
      lastScrollHeightRef.current = element.scrollHeight
      lastClientHeightRef.current = element.clientHeight
      prependHeightRef.current = null
      finishProgrammaticScroll()
      return
    }

    const forceFollow = previousForceFollowRef.current !== forceFollowVersion
    previousForceFollowRef.current = forceFollowVersion
    if (forceFollow) {
      scrollToBottom()
    } else if (followRef.current && nowMs() - lastUserUpwardAtRef.current >= UPWARD_GESTURE_LOCK_MS) {
      scrollToBottom()
    } else {
      lastScrollTopRef.current = element.scrollTop
      lastScrollHeightRef.current = element.scrollHeight
      lastClientHeightRef.current = element.clientHeight
    }
  }, [contentVersion, forceFollowVersion, finishProgrammaticScroll, scrollToBottom])

  useEffect(() => {
    if (!containerEl) return

    const pauseOnUpwardIntent = () => {
      if (containerEl.scrollHeight <= containerEl.clientHeight + 2) return
      lastUserUpwardAtRef.current = nowMs()
      cancelProgrammaticFinish()
      programmaticScrollRef.current = false
      followRef.current = false
      setFollowing(false)
      lastScrollTopRef.current = containerEl.scrollTop
      lastScrollHeightRef.current = containerEl.scrollHeight
      lastClientHeightRef.current = containerEl.clientHeight
    }

    const handleWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) {
        pauseOnUpwardIntent()
      } else if (event.deltaY > 0) {
        lastUserUpwardAtRef.current = -Infinity
      }
    }

    const handleTouchStart = (event: TouchEvent) => {
      touchStartYRef.current = event.touches[0]?.clientY ?? null
    }

    const handleTouchMove = (event: TouchEvent) => {
      const startY = touchStartYRef.current
      const currentY = event.touches[0]?.clientY
      if (startY === null || currentY === undefined) return
      const deltaY = currentY - startY
      if (deltaY > 4) {
        pauseOnUpwardIntent()
      } else if (deltaY < -4) {
        lastUserUpwardAtRef.current = -Infinity
      }
    }

    const handleTouchEnd = () => {
      touchStartYRef.current = null
    }

    containerEl.addEventListener('wheel', handleWheel, { passive: true })
    containerEl.addEventListener('touchstart', handleTouchStart, { passive: true })
    containerEl.addEventListener('touchmove', handleTouchMove, { passive: true })
    containerEl.addEventListener('touchend', handleTouchEnd, { passive: true })
    containerEl.addEventListener('touchcancel', handleTouchEnd, { passive: true })
    return () => {
      containerEl.removeEventListener('wheel', handleWheel)
      containerEl.removeEventListener('touchstart', handleTouchStart)
      containerEl.removeEventListener('touchmove', handleTouchMove)
      containerEl.removeEventListener('touchend', handleTouchEnd)
      containerEl.removeEventListener('touchcancel', handleTouchEnd)
    }
  }, [cancelProgrammaticFinish, containerEl])

  useEffect(() => {
    if (!containerEl || typeof ResizeObserver === 'undefined') return
    const handleResize = () => {
      const element = containerRef.current
      if (!element || prependHeightRef.current !== null) return
      const heightChanged =
        element.scrollHeight !== lastScrollHeightRef.current ||
        element.clientHeight !== lastClientHeightRef.current
      if (!heightChanged) return
      if (followRef.current && nowMs() - lastUserUpwardAtRef.current >= UPWARD_GESTURE_LOCK_MS) {
        scrollToBottom()
      } else {
        lastScrollTopRef.current = element.scrollTop
        lastScrollHeightRef.current = element.scrollHeight
        lastClientHeightRef.current = element.clientHeight
      }
    }
    const observer = new ResizeObserver(handleResize)
    observer.observe(containerEl)
    if (contentEl && contentEl !== containerEl) {
      observer.observe(contentEl)
    }
    return () => observer.disconnect()
  }, [containerEl, contentEl, scrollToBottom])

  const onScroll = useCallback(() => {
    const element = containerRef.current
    if (!element) return

    const prevTop = lastScrollTopRef.current
    const prevHeight = lastScrollHeightRef.current
    const currTop = element.scrollTop
    const currHeight = element.scrollHeight
    lastScrollTopRef.current = currTop
    lastScrollHeightRef.current = currHeight
    lastClientHeightRef.current = element.clientHeight

    const scrolledUp = currTop < prevTop - 1
    if (programmaticScrollRef.current) {
      // Never swallow an explicit upward scroll (e.g. scrollbar drag or wheel during streaming)
      if (scrolledUp && currHeight >= prevHeight) {
        cancelProgrammaticFinish()
        programmaticScrollRef.current = false
        followRef.current = false
        setFollowing(false)
      }
      return
    }

    const recentUpwardGesture = nowMs() - lastUserUpwardAtRef.current < UPWARD_GESTURE_LOCK_MS
    if (scrolledUp) {
      followRef.current = false
      setFollowing(false)
      return
    }

    const nextFollowing = isNearBottom({
      scrollTop: currTop,
      clientHeight: element.clientHeight,
      scrollHeight: currHeight,
      threshold: FOLLOW_BOTTOM_THRESHOLD,
    })

    if (nextFollowing) {
      if (!recentUpwardGesture) {
        followRef.current = true
        setFollowing(true)
      }
      return
    }

    // If we were pinned to the bottom and content height expanded without the user
    // scrolling upward, keep following instead of falsely pausing auto-scroll.
    const wasNearBottom =
      prevHeight > 0 &&
      isNearBottom({
        scrollTop: prevTop,
        clientHeight: element.clientHeight,
        scrollHeight: prevHeight,
        threshold: FOLLOW_BOTTOM_THRESHOLD,
      })
    if (
      followRef.current &&
      !recentUpwardGesture &&
      wasNearBottom &&
      currHeight > prevHeight &&
      currTop >= prevTop - 2
    ) {
      scrollToBottom()
      return
    }

    followRef.current = false
    setFollowing(false)
  }, [cancelProgrammaticFinish, scrollToBottom])

  const capturePrependAnchor = useCallback(() => {
    if (containerRef.current) prependHeightRef.current = containerRef.current.scrollHeight
  }, [])

  const pauseFollow = useCallback(() => {
    followRef.current = false
    setFollowing(false)
  }, [])

  return {
    setContainerRef,
    setContentRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
    pauseFollow,
  }
}
