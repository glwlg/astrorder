import { useRef, useEffect, useState, type CSSProperties } from 'react'

export interface VariableProximityProps {
  label: string
  fromWeight?: number
  toWeight?: number
  radius?: number
  className?: string
  style?: CSSProperties
  onClick?: () => void
  active?: boolean
}

export function VariableProximity({
  label,
  fromWeight = 450,
  toWeight = 700,
  radius = 60,
  className = '',
  style,
  onClick,
  active = false,
}: VariableProximityProps) {
  const spanRef = useRef<HTMLSpanElement>(null)
  const [weight, setWeight] = useState(active ? toWeight : fromWeight)

  useEffect(() => {
    if (active) {
      setWeight(toWeight)
      return
    }

    const handleMouseMove = (e: MouseEvent) => {
      if (!spanRef.current) return
      const rect = spanRef.current.getBoundingClientRect()
      const centerX = rect.left + rect.width / 2
      const centerY = rect.top + rect.height / 2
      const dist = Math.hypot(e.clientX - centerX, e.clientY - centerY)

      if (dist < radius) {
        const factor = 1 - dist / radius
        const computed = Math.round(fromWeight + (toWeight - fromWeight) * factor)
        setWeight(computed)
      } else {
        setWeight(fromWeight)
      }
    }

    window.addEventListener('mousemove', handleMouseMove, { passive: true })
    return () => window.removeEventListener('mousemove', handleMouseMove)
  }, [active, fromWeight, toWeight, radius])

  return (
    <span
      ref={spanRef}
      className={className}
      onClick={onClick}
      style={{
        fontWeight: weight,
        transition: 'font-weight 0.12s ease-out',
        userSelect: 'none',
        ...style,
      }}
    >
      {label}
    </span>
  )
}
