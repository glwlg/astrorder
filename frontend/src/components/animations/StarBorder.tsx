import type { ReactNode, CSSProperties } from 'react'
import './StarBorder.css'

export interface StarBorderProps {
  children: ReactNode
  className?: string
  style?: CSSProperties
  color?: string
  speed?: string
  borderRadius?: string | number
  active?: boolean
}

export function StarBorder({
  children,
  className = '',
  style,
  color = '#3b82f6',
  speed = '4s',
  borderRadius = 'var(--mantine-radius-lg, 12px)',
  active = true,
}: StarBorderProps) {
  if (!active) {
    return <div className={className} style={style}>{children}</div>
  }

  return (
    <div
      className={`astr-star-border-wrap ${className}`}
      style={{
        borderRadius,
        '--sb-color': color,
        '--sb-speed': speed,
        ...style,
      } as CSSProperties}
    >
      <div className="astr-sb-glow-top" />
      <div className="astr-sb-glow-bottom" />
      <div className="astr-sb-inner" style={{ borderRadius }}>
        {children}
      </div>
    </div>
  )
}

