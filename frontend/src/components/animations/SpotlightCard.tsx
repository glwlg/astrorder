import { useRef, type ReactNode, type CSSProperties } from 'react'
import './SpotlightCard.css'

export interface SpotlightCardProps {
  children: ReactNode
  className?: string
  style?: CSSProperties
  spotlightColor?: string
  radius?: string | number
  onClick?: () => void
}

export function SpotlightCard({
  children,
  className = '',
  style,
  spotlightColor = 'rgba(91, 108, 255, 0.15)',
  radius = 'var(--mantine-radius-lg, 12px)',
  onClick,
}: SpotlightCardProps) {
  const cardRef = useRef<HTMLDivElement>(null)

  const handleMouseMove = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!cardRef.current) return
    const rect = cardRef.current.getBoundingClientRect()
    const x = e.clientX - rect.left
    const y = e.clientY - rect.top

    cardRef.current.style.setProperty('--spotlight-x', `${x}px`)
    cardRef.current.style.setProperty('--spotlight-y', `${y}px`)
    cardRef.current.style.setProperty('--spotlight-color', spotlightColor)
  }

  return (
    <div
      ref={cardRef}
      onMouseMove={handleMouseMove}
      onClick={onClick}
      className={`astr-spotlight-card ${className}`}
      style={{ borderRadius: radius, ...style }}
    >
      {children}
    </div>
  )
}

