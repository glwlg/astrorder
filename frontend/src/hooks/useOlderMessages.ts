import { useRef } from 'react'
import type { TouchEvent, UIEvent, WheelEvent } from 'react'

export function useOlderMessages({ hasMore, loading, load, capture }: { hasMore: boolean; loading: boolean; load: () => unknown; capture: () => void }) {
  const pending = useRef(false)
  const previousTop = useRef(0)
  const touchY = useRef<number | null>(null)
  const request = async () => {
    if (!hasMore || loading || pending.current) return
    pending.current = true
    capture()
    try { await load() } finally { pending.current = false }
  }
  return {
    request,
    onScroll: (event: UIEvent<HTMLDivElement>) => {
      const top = event.currentTarget.scrollTop
      if (top < previousTop.current && top <= 32) void request()
      previousTop.current = top
    },
    onWheel: (event: WheelEvent<HTMLDivElement>) => { if (event.deltaY < 0 && event.currentTarget.scrollTop <= 32) void request() },
    onTouchStart: (event: TouchEvent<HTMLDivElement>) => { touchY.current = event.touches[0]?.clientY ?? null },
    onTouchMove: (event: TouchEvent<HTMLDivElement>) => {
      if (touchY.current !== null && event.touches[0] && event.touches[0].clientY - touchY.current > 32 && event.currentTarget.scrollTop <= 32) {
        touchY.current = null
        void request()
      }
    },
  }
}
