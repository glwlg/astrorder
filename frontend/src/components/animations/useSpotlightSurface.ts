import { useCallback, useRef, type MouseEvent } from 'react'
import './SpotlightCard.css'

const SPOTLIGHT_MIN_INTERVAL_MS = 32
const RECT_CACHE_TTL_MS = 500

// React Bits Spotlight Card adaptation; full notice: /react-bits-NOTICE.txt.
// Reuse the pointer surface without wrapping cards or changing their controls.
export function useSpotlightSurface(spotlightColor = 'rgba(91, 108, 255, 0.12)') {
  const ref = useRef<HTMLDivElement>(null)
  const cachedRectRef = useRef<{ left: number; top: number; capturedAt: number } | null>(null)
  const mediaAllowedRef = useRef<boolean | null>(null)
  const lastWriteRef = useRef<{ at: number; x: number; y: number; color: string } | null>(null)

  const onMouseMove = useCallback((event: MouseEvent<HTMLDivElement>) => {
    const el = ref.current
    if (!el || event.buttons !== 0) return

    const now = typeof performance !== 'undefined' ? performance.now() : Date.now()
    const cachedRect = cachedRectRef.current
    const needsFreshRect = !cachedRect || now - cachedRect.capturedAt > RECT_CACHE_TTL_MS

    if (needsFreshRect) {
      const canHover = window.matchMedia('(hover: hover) and (pointer: fine)').matches
        && !window.matchMedia('(prefers-reduced-motion: reduce)').matches
      mediaAllowedRef.current = canHover
      if (!canHover) return
      const rect = el.getBoundingClientRect()
      cachedRectRef.current = { left: rect.left, top: rect.top, capturedAt: now }
    } else if (!mediaAllowedRef.current) {
      return
    }

    const rect = cachedRectRef.current
    if (!rect) return

    const x = Math.round(event.clientX - rect.left)
    const y = Math.round(event.clientY - rect.top)
    const last = lastWriteRef.current
    if (last && now - last.at < SPOTLIGHT_MIN_INTERVAL_MS) return
    if (last && Math.abs(x - last.x) < 2 && Math.abs(y - last.y) < 2 && last.color === spotlightColor) return

    el.style.setProperty('--spotlight-x', `${x}px`)
    el.style.setProperty('--spotlight-y', `${y}px`)
    if (!last || last.color !== spotlightColor) {
      el.style.setProperty('--spotlight-color', spotlightColor)
    }
    lastWriteRef.current = { at: now, x, y, color: spotlightColor }
  }, [spotlightColor])

  const onMouseLeave = useCallback(() => {
    cachedRectRef.current = null
    mediaAllowedRef.current = null
    lastWriteRef.current = null
  }, [])

  return { ref, onMouseMove, onMouseLeave }
}
