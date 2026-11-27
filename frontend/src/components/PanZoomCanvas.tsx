import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { IconFocusCentered, IconMinus, IconPlus, IconRefresh } from '@tabler/icons-react'
import './panZoomCanvas.css'

export interface PanZoomTransform {
  scale: number
  tx: number
  ty: number
}

export interface IntrinsicSize {
  width: number
  height: number
}

export interface PanZoomCanvasHandle {
  zoomIn: () => void
  zoomOut: () => void
  reset: () => void
  fitToViewport: () => void
  setScale: (nextScale: number, animate?: boolean) => void
  getTransform: () => PanZoomTransform
}

export interface PanZoomCanvasProps {
  children: ReactNode
  className?: string
  ariaLabel?: string
  dotGrid?: boolean
  minScale?: number
  maxScale?: number
  showControls?: boolean
  /**
   * Natural intrinsic size (e.g. SVG viewBox or image naturalWidth/Height).
   * When provided, scaling resizes the stage width/height directly instead of CSS transform: scale(),
   * keeping SVG vectors, Mermaid foreignObject text, and high-DPI images razor-sharp at any zoom.
   */
  contentSize?: IntrinsicSize | null
  /** Trigger auto-fit whenever this key changes (e.g. new diagram/image rendered). */
  fitKey?: string | number
  /** Extra buttons appended to the right of the floating zoom pill. */
  extraControls?: ReactNode
  onScaleChange?: (scale: number) => void
}

interface Point {
  x: number
  y: number
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function distanceBetween(a: Point, b: Point): number {
  return Math.hypot(a.x - b.x, a.y - b.y)
}

function midpointBetween(a: Point, b: Point): Point {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }
}

function eventTargetElement(target: EventTarget | null): Element | null {
  if (target instanceof Element) return target
  if (target instanceof Node) return target.parentElement
  return null
}

/**
 * Extracts natural pixel dimensions from an inline SVG string (viewBox first, then explicit width/height).
 */
export function extractSvgIntrinsicSize(svgMarkup: string): IntrinsicSize | null {
  if (!svgMarkup) return null
  const openTagMatch = svgMarkup.match(/<svg\b[^>]*>/i)
  if (!openTagMatch) return null
  const openTag = openTagMatch[0]

  const viewBoxMatch = openTag.match(
    /viewBox\s*=\s*["']\s*[+-]?[\d.eE+-]+[\s,]+[+-]?[\d.eE+-]+[\s,]+([+-]?[\d.eE+-]+)[\s,]+([+-]?[\d.eE+-]+)\s*["']/i,
  )
  if (viewBoxMatch) {
    const width = Number.parseFloat(viewBoxMatch[1])
    const height = Number.parseFloat(viewBoxMatch[2])
    if (Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0) {
      return { width, height }
    }
  }

  const widthMatch = openTag.match(/\bwidth\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/i)
  const heightMatch = openTag.match(/\bheight\s*=\s*["']\s*([\d.]+)(?:px)?\s*["']/i)
  if (widthMatch && heightMatch) {
    const width = Number.parseFloat(widthMatch[1])
    const height = Number.parseFloat(heightMatch[1])
    if (Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0) {
      return { width, height }
    }
  }

  return null
}

export const PanZoomCanvas = forwardRef<PanZoomCanvasHandle, PanZoomCanvasProps>(function PanZoomCanvas(
  {
    children,
    className,
    ariaLabel = '可缩放和拖拽画布',
    dotGrid = false,
    minScale = 0.1,
    maxScale = 8,
    showControls = true,
    contentSize = null,
    fitKey,
    extraControls,
    onScaleChange,
  },
  ref,
) {
  const viewportRef = useRef<HTMLDivElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const [transform, setTransform] = useState<PanZoomTransform>({ scale: 1, tx: 0, ty: 0 })
  const [animating, setAnimating] = useState(false)
  const [dragging, setDragging] = useState(false)

  const transformRef = useRef<PanZoomTransform>(transform)
  const fitScaleRef = useRef<number>(1)
  const pointersRef = useRef<Map<number, Point>>(new Map())
  const lastTapRef = useRef<{ time: number; x: number; y: number } | null>(null)
  const suppressDblClickUntilRef = useRef<number>(0)
  const panRef = useRef<{
    startX: number
    startY: number
    startTx: number
    startTy: number
    moved: boolean
  } | null>(null)
  const pinchRef = useRef<{
    startDist: number
    startMid: Point
    startScale: number
    startTx: number
    startTy: number
  } | null>(null)
  const onScaleChangeRef = useRef(onScaleChange)
  useEffect(() => {
    onScaleChangeRef.current = onScaleChange
  }, [onScaleChange])

  const hasIntrinsicSize = Boolean(
    contentSize &&
      Number.isFinite(contentSize.width) &&
      Number.isFinite(contentSize.height) &&
      contentSize.width > 0 &&
      contentSize.height > 0,
  )

  const applyTransform = useCallback(
    (next: PanZoomTransform, animate = false) => {
      const safe: PanZoomTransform = {
        scale: clamp(next.scale, minScale, maxScale),
        tx: Number.isFinite(next.tx) ? next.tx : 0,
        ty: Number.isFinite(next.ty) ? next.ty : 0,
      }
      transformRef.current = safe
      setAnimating(animate)
      setTransform(safe)
      onScaleChangeRef.current?.(safe.scale)
    },
    [maxScale, minScale],
  )

  const zoomAroundClientPoint = useCallback(
    (targetScale: number, clientX: number, clientY: number, animate = false) => {
      const vp = viewportRef.current
      const clampedScale = clamp(targetScale, minScale, maxScale)
      if (!vp) {
        applyTransform({ scale: clampedScale, tx: 0, ty: 0 }, animate)
        return
      }
      const rect = vp.getBoundingClientRect()
      const cx = rect.left + rect.width / 2
      const cy = rect.top + rect.height / 2
      const current = transformRef.current
      const ox = clientX - cx
      const oy = clientY - cy
      const lx = (ox - current.tx) / current.scale
      const ly = (oy - current.ty) / current.scale
      applyTransform(
        {
          scale: clampedScale,
          tx: ox - lx * clampedScale,
          ty: oy - ly * clampedScale,
        },
        animate,
      )
    },
    [applyTransform, maxScale, minScale],
  )

  const zoomByFactor = useCallback(
    (factor: number, animate = true) => {
      const vp = viewportRef.current
      const nextScale = clamp(transformRef.current.scale * factor, minScale, maxScale)
      if (!vp) {
        applyTransform({ scale: nextScale, tx: 0, ty: 0 }, animate)
        return
      }
      const rect = vp.getBoundingClientRect()
      zoomAroundClientPoint(nextScale, rect.left + rect.width / 2, rect.top + rect.height / 2, animate)
    },
    [applyTransform, maxScale, minScale, zoomAroundClientPoint],
  )

  const reset = useCallback(() => {
    applyTransform({ scale: 1, tx: 0, ty: 0 }, true)
  }, [applyTransform])

  const computeFitScale = useCallback(() => {
    const vp = viewportRef.current
    const stage = stageRef.current
    if (!vp) return 1
    const vpW = vp.clientWidth
    const vpH = vp.clientHeight
    const contentW = hasIntrinsicSize && contentSize ? contentSize.width : stage?.offsetWidth || stage?.scrollWidth || 0
    const contentH = hasIntrinsicSize && contentSize ? contentSize.height : stage?.offsetHeight || stage?.scrollHeight || 0
    if (!vpW || !vpH || !contentW || !contentH) {
      return 1
    }
    const isNarrow = vpW <= 640
    const padX = isNarrow ? Math.max(12, vpW * 0.03) : Math.max(24, vpW * 0.05)
    const padY = isNarrow ? Math.max(64, vpH * 0.08) : Math.max(58, vpH * 0.08)
    const availW = Math.max(80, vpW - padX * 2)
    const availH = Math.max(80, vpH - padY * 2)
    return clamp(Math.min(availW / contentW, availH / contentH, 1.35), minScale, maxScale)
  }, [contentSize, hasIntrinsicSize, maxScale, minScale])

  const fitToViewport = useCallback(() => {
    const fitScale = computeFitScale()
    fitScaleRef.current = fitScale
    applyTransform({ scale: fitScale, tx: 0, ty: 0 }, true)
  }, [applyTransform, computeFitScale])

  const toggleDoubleTapZoom = useCallback(
    (clientX: number, clientY: number) => {
      const current = transformRef.current
      const fitScale = computeFitScale()
      fitScaleRef.current = fitScale
      const isZoomedOrPanned =
        current.scale > fitScale * 1.15 ||
        Math.abs(current.tx) > 12 ||
        Math.abs(current.ty) > 12
      if (isZoomedOrPanned) {
        fitToViewport()
      } else {
        const zoomedScale = clamp(fitScale * 2.2, minScale, maxScale)
        zoomAroundClientPoint(zoomedScale, clientX, clientY, true)
      }
    },
    [computeFitScale, fitToViewport, maxScale, minScale, zoomAroundClientPoint],
  )

  useImperativeHandle(
    ref,
    () => ({
      zoomIn: () => zoomByFactor(1.25, true),
      zoomOut: () => zoomByFactor(1 / 1.25, true),
      reset,
      fitToViewport,
      setScale: (nextScale: number, animate = true) => {
        applyTransform({ scale: nextScale, tx: 0, ty: 0 }, animate)
      },
      getTransform: () => transformRef.current,
    }),
    [applyTransform, fitToViewport, reset, zoomByFactor],
  )

  // Auto-fit cleanly whenever fitKey or intrinsic contentSize changes
  useEffect(() => {
    if (fitKey === undefined) return
    let raf1 = 0
    let raf2 = 0
    raf1 = window.requestAnimationFrame(() => {
      const vp = viewportRef.current
      if (vp && vp.clientWidth > 0 && vp.clientHeight > 0) {
        fitToViewport()
      } else {
        raf2 = window.requestAnimationFrame(() => {
          fitToViewport()
        })
      }
    })
    return () => {
      window.cancelAnimationFrame(raf1)
      window.cancelAnimationFrame(raf2)
    }
  }, [fitKey, fitToViewport])

  // Native non-passive wheel, Safari gesture, and touchmove listeners to prevent
  // browser pull-to-refresh / scroll bleed while dragging or pinching on mobile.
  useEffect(() => {
    const vp = viewportRef.current
    if (!vp) return

    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      event.stopPropagation()
      const delta = event.deltaY
      if (delta === 0) return
      const intensity = event.ctrlKey ? 0.012 : Math.abs(delta) < 40 ? 0.0045 : 0.0018
      const factor = Math.exp(-delta * intensity)
      const clampedFactor = clamp(factor, 0.72, 1.38)
      zoomAroundClientPoint(transformRef.current.scale * clampedFactor, event.clientX, event.clientY, false)
    }

    const preventGesture = (event: Event) => {
      event.preventDefault()
    }

    const preventCanvasTouchMove = (event: TouchEvent) => {
      const target = eventTargetElement(event.target)
      if (target?.closest('.astr-panzoom-controls')) return
      if (event.cancelable) {
        event.preventDefault()
      }
    }

    vp.addEventListener('wheel', onWheel, { passive: false })
    vp.addEventListener('gesturestart', preventGesture, { passive: false })
    vp.addEventListener('gesturechange', preventGesture, { passive: false })
    vp.addEventListener('gestureend', preventGesture, { passive: false })
    vp.addEventListener('touchmove', preventCanvasTouchMove, { passive: false })
    return () => {
      vp.removeEventListener('wheel', onWheel)
      vp.removeEventListener('gesturestart', preventGesture)
      vp.removeEventListener('gesturechange', preventGesture)
      vp.removeEventListener('gestureend', preventGesture)
      vp.removeEventListener('touchmove', preventCanvasTouchMove)
    }
  }, [zoomAroundClientPoint])

  const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return
    const target = eventTargetElement(event.target)
    if (target?.closest('.astr-panzoom-controls') || target?.closest('button, a, input, textarea')) {
      return
    }
    const vp = viewportRef.current
    if (!vp) return
    try {
      vp.setPointerCapture(event.pointerId)
    } catch {
      // Synthetic pointer events in JSDOM may not support pointer capture
    }
    pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })
    setAnimating(false)

    if (pointersRef.current.size === 1) {
      const current = transformRef.current
      panRef.current = {
        startX: event.clientX,
        startY: event.clientY,
        startTx: current.tx,
        startTy: current.ty,
        moved: false,
      }
      pinchRef.current = null
      setDragging(true)
    } else if (pointersRef.current.size === 2) {
      panRef.current = null
      lastTapRef.current = null
      const [p1, p2] = Array.from(pointersRef.current.values())
      const current = transformRef.current
      pinchRef.current = {
        startDist: Math.max(1, distanceBetween(p1, p2)),
        startMid: midpointBetween(p1, p2),
        startScale: current.scale,
        startTx: current.tx,
        startTy: current.ty,
      }
    } else {
      lastTapRef.current = null
    }
  }

  const handlePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!pointersRef.current.has(event.pointerId)) return
    pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })

    const vp = viewportRef.current
    if (pointersRef.current.size >= 2 && pinchRef.current && vp) {
      const [p1, p2] = Array.from(pointersRef.current.values())
      const dist = Math.max(1, distanceBetween(p1, p2))
      const mid = midpointBetween(p1, p2)
      const pinch = pinchRef.current
      const nextScale = clamp(pinch.startScale * (dist / pinch.startDist), minScale, maxScale)
      const rect = vp.getBoundingClientRect()
      const cx = rect.left + rect.width / 2
      const cy = rect.top + rect.height / 2
      const ox = pinch.startMid.x - cx
      const oy = pinch.startMid.y - cy
      const mx = mid.x - cx
      const my = mid.y - cy
      const lx = (ox - pinch.startTx) / pinch.startScale
      const ly = (oy - pinch.startTy) / pinch.startScale
      applyTransform(
        {
          scale: nextScale,
          tx: mx - lx * nextScale,
          ty: my - ly * nextScale,
        },
        false,
      )
      return
    }

    if (pointersRef.current.size === 1 && panRef.current) {
      const pan = panRef.current
      const dx = event.clientX - pan.startX
      const dy = event.clientY - pan.startY
      const dragSlop = event.pointerType === 'touch' ? 6 : 3
      if (!pan.moved) {
        if (Math.hypot(dx, dy) <= dragSlop) {
          return
        }
        pan.moved = true
      }
      applyTransform(
        {
          scale: transformRef.current.scale,
          tx: pan.startTx + dx,
          ty: pan.startTy + dy,
        },
        false,
      )
    }
  }

  const finishPointer = (event: React.PointerEvent<HTMLDivElement>, cancelled: boolean) => {
    if (!pointersRef.current.has(event.pointerId)) return
    pointersRef.current.delete(event.pointerId)
    const vp = viewportRef.current
    if (vp) {
      try {
        if (vp.hasPointerCapture?.(event.pointerId)) {
          vp.releasePointerCapture(event.pointerId)
        }
      } catch {
        // Ignore in environments without pointer capture
      }
    }

    if (pointersRef.current.size === 2) {
      const [p1, p2] = Array.from(pointersRef.current.values())
      const current = transformRef.current
      pinchRef.current = {
        startDist: Math.max(1, distanceBetween(p1, p2)),
        startMid: midpointBetween(p1, p2),
        startScale: current.scale,
        startTx: current.tx,
        startTy: current.ty,
      }
      panRef.current = null
      return
    }

    if (pointersRef.current.size === 1) {
      pinchRef.current = null
      const [remaining] = Array.from(pointersRef.current.values())
      const current = transformRef.current
      panRef.current = {
        startX: remaining.x,
        startY: remaining.y,
        startTx: current.tx,
        startTy: current.ty,
        moved: true,
      }
      return
    }

    const endedPan = panRef.current
    panRef.current = null
    pinchRef.current = null
    setDragging(false)

    if (cancelled) {
      lastTapRef.current = null
      return
    }

    if (event.pointerType !== 'mouse') {
      if (endedPan && !endedPan.moved) {
        const now = Date.now()
        const point = { x: event.clientX, y: event.clientY }
        const prevTap = lastTapRef.current
        if (prevTap && now - prevTap.time <= 320 && distanceBetween(prevTap, point) <= 36) {
          lastTapRef.current = null
          suppressDblClickUntilRef.current = now + 450
          toggleDoubleTapZoom(event.clientX, event.clientY)
        } else {
          lastTapRef.current = { time: now, x: point.x, y: point.y }
        }
      } else {
        lastTapRef.current = null
      }
    }
  }

  const handlePointerUp = (event: React.PointerEvent<HTMLDivElement>) => {
    finishPointer(event, false)
  }

  const handlePointerCancel = (event: React.PointerEvent<HTMLDivElement>) => {
    finishPointer(event, true)
  }

  const handleDoubleClick = (event: React.MouseEvent<HTMLDivElement>) => {
    const target = eventTargetElement(event.target)
    if (target?.closest('.astr-panzoom-controls') || target?.closest('button, a, input, textarea')) {
      return
    }
    if (Date.now() < suppressDblClickUntilRef.current) {
      return
    }
    toggleDoubleTapZoom(event.clientX, event.clientY)
  }

  const zoomPercent = Math.round(transform.scale * 100)

  // When intrinsic size is known, scale via real layout width/height + pure 2D translate
  // so Chromium/WebView2 re-rasterizes vector SVGs and crisp text natively at target DPI.
  const stageStyle: CSSProperties =
    hasIntrinsicSize && contentSize
      ? {
          width: Math.max(1, Math.round(contentSize.width * transform.scale)),
          height: Math.max(1, Math.round(contentSize.height * transform.scale)),
          transform: `translate(${Math.round(transform.tx)}px, ${Math.round(transform.ty)}px)`,
        }
      : {
          transform: `translate(${Math.round(transform.tx)}px, ${Math.round(transform.ty)}px) scale(${transform.scale})`,
        }

  return (
    <div
      ref={viewportRef}
      className={`astr-panzoom-viewport${dotGrid ? ' is-dot-grid' : ''}${dragging ? ' is-dragging' : ''}${className ? ` ${className}` : ''}`}
      aria-label={ariaLabel}
      data-zoom-scale={transform.scale.toFixed(2)}
      data-pan-x={Math.round(transform.tx)}
      data-pan-y={Math.round(transform.ty)}
      onPointerDown={handlePointerDown}
      onPointerMove={handlePointerMove}
      onPointerUp={handlePointerUp}
      onPointerCancel={handlePointerCancel}
      onDoubleClick={handleDoubleClick}
    >
      <div
        ref={stageRef}
        className={`astr-panzoom-stage${animating ? ' is-animating' : ''}`}
        style={stageStyle}
      >
        {children}
      </div>

      {showControls && (
        <div
          className="astr-panzoom-controls"
          role="toolbar"
          aria-label="画布缩放控制"
          onPointerDown={(e) => e.stopPropagation()}
          onTouchStart={(e) => e.stopPropagation()}
          onDoubleClick={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            className="astr-panzoom-btn"
            aria-label="缩小画布"
            title="缩小 (-)"
            disabled={transform.scale <= minScale + 0.01}
            onClick={() => zoomByFactor(1 / 1.25, true)}
          >
            <IconMinus size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="astr-panzoom-btn astr-panzoom-readout"
            aria-label="重置为 100%"
            title="点击重置为 100% 原始比例"
            onClick={reset}
          >
            {zoomPercent}%
          </button>
          <button
            type="button"
            className="astr-panzoom-btn"
            aria-label="放大画布"
            title="放大 (+)"
            disabled={transform.scale >= maxScale - 0.01}
            onClick={() => zoomByFactor(1.25, true)}
          >
            <IconPlus size={14} aria-hidden="true" />
          </button>
          <span className="astr-panzoom-sep" aria-hidden="true" />
          <button
            type="button"
            className="astr-panzoom-btn"
            aria-label="适应画布"
            title="适应窗口"
            onClick={fitToViewport}
          >
            <IconFocusCentered size={14} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="astr-panzoom-btn"
            aria-label="复位视角"
            title="重置为 100% (0)"
            onClick={reset}
          >
            <IconRefresh size={13} aria-hidden="true" />
          </button>
          {extraControls}
        </div>
      )}
    </div>
  )
})
