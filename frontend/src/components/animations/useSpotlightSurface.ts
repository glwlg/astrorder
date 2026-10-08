import { useRef, type MouseEvent } from 'react'
import './SpotlightCard.css'

// React Bits Spotlight Card adaptation; full notice: /react-bits-NOTICE.txt.
// Reuse the pointer surface without wrapping cards or changing their controls.
export function useSpotlightSurface(spotlightColor = 'rgba(91, 108, 255, 0.12)') {
  const ref = useRef<HTMLDivElement>(null)
  const onMouseMove = (event: MouseEvent<HTMLDivElement>) => {
    if (!ref.current || event.buttons !== 0) return
    if (!window.matchMedia('(hover: hover) and (pointer: fine)').matches
      || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const rect = ref.current.getBoundingClientRect()
    ref.current.style.setProperty('--spotlight-x', `${event.clientX - rect.left}px`)
    ref.current.style.setProperty('--spotlight-y', `${event.clientY - rect.top}px`)
    ref.current.style.setProperty('--spotlight-color', spotlightColor)
  }
  return { ref, onMouseMove }
}
