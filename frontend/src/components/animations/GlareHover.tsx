import type { ReactNode, CSSProperties } from 'react'
import './GlareHover.css'

export interface GlareHoverProps {
  children: ReactNode
  className?: string
  style?: CSSProperties
  borderRadius?: string | number
  glareColor?: string
  glareOpacity?: number
}

export function GlareHover({
  children,
  className = '',
  style,
  borderRadius = 'var(--mantine-radius-lg, 12px)',
  glareColor = '#ffffff',
  glareOpacity = 0.08,
}: GlareHoverProps) {
  return (
    <div
      className={`astr-glare-card ${className}`}
      style={{
        borderRadius,
        '--glare-color': glareColor,
        '--glare-opacity': glareOpacity,
        ...style,
      } as CSSProperties}
    >
      <div className="astr-glare-sheen" />
      {children}
    </div>
  )
}

