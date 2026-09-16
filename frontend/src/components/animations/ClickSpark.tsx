import { useRef, useEffect, useCallback, type ReactNode, type CSSProperties } from 'react'

export interface ClickSparkProps {
  sparkColor?: string
  sparkSize?: number
  sparkRadius?: number
  sparkCount?: number
  duration?: number
  extraScale?: number
  className?: string
  style?: CSSProperties
  children?: ReactNode
}

interface Spark {
  x: number
  y: number
  angle: number
  startTime: number
}

export function ClickSpark({
  sparkColor = '#3b82f6',
  sparkSize = 12,
  sparkRadius = 32,
  sparkCount = 8,
  duration = 450,
  extraScale = 1.0,
  className = '',
  style,
  children,
}: ClickSparkProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const sparksRef = useRef<Spark[]>([])
  const startTimeRef = useRef<number | null>(null)
  const padding = 30 // allow sparks to explode beyond button bounds

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const parent = canvas.parentElement
    if (!parent) return

    let resizeTimeout: ReturnType<typeof setTimeout>
    const resizeCanvas = () => {
      const { width, height } = parent.getBoundingClientRect()
      const targetW = width + padding * 2
      const targetH = height + padding * 2
      if (canvas.width !== targetW || canvas.height !== targetH) {
        canvas.width = targetW
        canvas.height = targetH
      }
    }

    const handleResize = () => {
      clearTimeout(resizeTimeout)
      resizeTimeout = setTimeout(resizeCanvas, 100)
    }

    resizeCanvas()
    if (typeof ResizeObserver === 'undefined') {
      return () => clearTimeout(resizeTimeout)
    }
    const ro = new ResizeObserver(handleResize)
    ro.observe(parent)

    return () => {
      ro.disconnect()
      clearTimeout(resizeTimeout)
    }
  }, [padding])

  const easeFunc = useCallback((t: number) => t * (2 - t), [])

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    let animationId: number

    const draw = (timestamp: number) => {
      if (!startTimeRef.current) {
        startTimeRef.current = timestamp
      }
      ctx.clearRect(0, 0, canvas.width, canvas.height)

      sparksRef.current = sparksRef.current.filter((spark) => {
        const elapsed = timestamp - spark.startTime
        if (elapsed >= duration) return false

        const progress = elapsed / duration
        const eased = easeFunc(progress)

        const distance = eased * sparkRadius * extraScale
        const lineLength = sparkSize * (1 - eased)

        const x1 = spark.x + distance * Math.cos(spark.angle)
        const y1 = spark.y + distance * Math.sin(spark.angle)
        const x2 = spark.x + (distance + lineLength) * Math.cos(spark.angle)
        const y2 = spark.y + (distance + lineLength) * Math.sin(spark.angle)

        ctx.strokeStyle = sparkColor
        ctx.lineWidth = 2.2
        ctx.lineCap = 'round'
        ctx.beginPath()
        ctx.moveTo(x1, y1)
        ctx.lineTo(x2, y2)
        ctx.stroke()

        return true
      })

      animationId = requestAnimationFrame(draw)
    }

    animationId = requestAnimationFrame(draw)
    return () => cancelAnimationFrame(animationId)
  }, [sparkColor, sparkSize, sparkRadius, duration, easeFunc, extraScale])

  const handleClick = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    const canvas = canvasRef.current
    if (!canvas) return
    const rect = canvas.getBoundingClientRect()
    const x = e.clientX - rect.left
    const y = e.clientY - rect.top

    const now = performance.now()
    const newSparks = Array.from({ length: sparkCount }, (_, i) => ({
      x,
      y,
      angle: (2 * Math.PI * i) / sparkCount,
      startTime: now,
    }))

    sparksRef.current.push(...newSparks)
  }

  return (
    <div
      className={className}
      style={{ position: 'relative', display: 'inline-flex', ...style }}
      onPointerDown={handleClick}
    >
      <canvas
        ref={canvasRef}
        style={{
          width: `calc(100% + ${padding * 2}px)`,
          height: `calc(100% + ${padding * 2}px)`,
          display: 'block',
          userSelect: 'none',
          position: 'absolute',
          top: -padding,
          left: -padding,
          pointerEvents: 'none',
          zIndex: 99,
        }}
      />
      {children}
    </div>
  )
}
