import type { CSSProperties } from 'react'

export interface ShinyTextProps {
  text: string
  disabled?: boolean
  speed?: number
  className?: string
  color?: string
  shineColor?: string
  spread?: number
  yoyo?: boolean
  pauseOnHover?: boolean
  direction?: 'left' | 'right'
  delay?: number
}

export function ShinyText({
  text,
  disabled = false,
  speed = 2,
  className = '',
  color = 'currentColor',
  shineColor = 'var(--astr-indigo, #5b6cff)',
  spread = 120,
  yoyo = false,
  pauseOnHover = false,
  direction = 'left',
}: ShinyTextProps) {
  const duration = Math.max(0.6, speed)
  const style: CSSProperties = {
    backgroundImage: `linear-gradient(${spread}deg, ${color} 0%, ${color} 35%, ${shineColor} 50%, ${color} 65%, ${color} 100%)`,
    backgroundSize: '200% auto',
    backgroundPosition: '150% center',
    WebkitBackgroundClip: 'text',
    backgroundClip: 'text',
    WebkitTextFillColor: 'transparent',
    display: 'inline-block',
    animation: disabled
      ? undefined
      : `astr-shiny-sweep ${duration}s linear infinite ${yoyo ? 'alternate' : direction === 'right' ? 'reverse' : 'normal'}`,
  }

  return (
    <span
      data-liquid-ignore=""
      className={`astr-shiny-text${pauseOnHover ? ' is-pause-hover' : ''}${className ? ` ${className}` : ''}`}
      style={style}
    >
      {text}
    </span>
  )
}

