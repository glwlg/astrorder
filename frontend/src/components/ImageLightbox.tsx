import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { IconChevronLeft, IconChevronRight, IconDownload, IconMinus, IconPlus, IconRefresh, IconX } from '@tabler/icons-react'
import { authenticatedApiFetch } from '../api/client'
import { downloadFile } from '../features/sidecar/fileTransfer'
import { imageMediaType } from '../features/sidecar/viewers/image/formats'
import './imageLightbox.css'

const MIN_SCALE = 1
const MAX_SCALE = 6
const RUBBER_MIN_SCALE = 0.72
const DOUBLE_TAP_SCALE = 2.5
const DOUBLE_TAP_MS = 280
const DOUBLE_TAP_DIST_PX = 36
const SWIPE_THRESHOLD_PX = 56

interface TransformState {
  scale: number
  tx: number
  ty: number
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

function extractImageLabel(src: string): string {
  try {
    const parsed = new URL(src, typeof window !== 'undefined' ? window.location.href : 'http://localhost')
    const rawPath = parsed.searchParams.get('path')
    if (rawPath) {
      const base = rawPath.split(/[/\\]/).filter(Boolean).pop()
      if (base) return base
    }
    if (!src.startsWith('data:') && !src.startsWith('blob:')) {
      const seg = decodeURIComponent(parsed.pathname.split('/').filter(Boolean).pop() || '')
      if (seg && seg !== 'raw') return seg
    }
  } catch {
    // Fall back to default label.
  }
  return '图片预览'
}

export interface ImageLightboxProps {
  src?: string | null
  images?: string[] | null
  index?: number
  onIndexChange?: (nextIndex: number) => void
  onClose: () => void
}

export function ImageLightbox({
  src = null,
  images = null,
  index = 0,
  onIndexChange,
  onClose,
}: ImageLightboxProps) {
  const gallery = useMemo(() => {
    if (images && images.length > 0) return images
    return src ? [src] : []
  }, [images, src])

  const [uncontrolledIndex, setUncontrolledIndex] = useState(index)
  useEffect(() => {
    setUncontrolledIndex(index)
  }, [index, images, src])

  const currentIndex = gallery.length > 0
    ? clamp(onIndexChange ? index : uncontrolledIndex, 0, gallery.length - 1)
    : 0
  const activeSrc = gallery.length > 0 ? gallery[currentIndex] : null
  const hasMultiple = gallery.length > 1

  const setActiveIndex = useCallback((nextIndex: number) => {
    if (gallery.length <= 1) return
    const clamped = clamp(nextIndex, 0, gallery.length - 1)
    if (clamped === currentIndex) return
    if (onIndexChange) onIndexChange(clamped)
    else setUncontrolledIndex(clamped)
  }, [gallery.length, currentIndex, onIndexChange])

  const dialogRef = useRef<HTMLDialogElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  const [resolvedSrc, setResolvedSrc] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [transform, setTransform] = useState<TransformState>({ scale: 1, tx: 0, ty: 0 })
  const [swipeOffsetX, setSwipeOffsetX] = useState(0)
  const [animating, setAnimating] = useState(false)

  const transformRef = useRef<TransformState>(transform)
  const pointersRef = useRef<Map<number, Point>>(new Map())
  const pinchRef = useRef<{
    startDist: number
    startMid: Point
    startScale: number
    startTx: number
    startTy: number
  } | null>(null)
  const panRef = useRef<{
    startX: number
    startY: number
    startTx: number
    startTy: number
    moved: boolean
    swiping: boolean
    fromBackdrop: boolean
  } | null>(null)
  const hadMultiTouchRef = useRef(false)
  const lastTapRef = useRef<{ time: number; x: number; y: number } | null>(null)

  const label = useMemo(() => (activeSrc ? extractImageLabel(activeSrc) : '图片预览'), [activeSrc])
  const isApiImage = Boolean(activeSrc && /^\/api\/v1\//i.test(activeSrc))

  const applyTransform = useCallback((next: TransformState, animate = false) => {
    transformRef.current = next
    setAnimating(animate)
    setTransform(next)
  }, [])

  const clampTransform = useCallback((scale: number, tx: number, ty: number): TransformState => {
    const safeScale = clamp(scale, MIN_SCALE, MAX_SCALE)
    if (safeScale <= MIN_SCALE + 0.001) {
      return { scale: MIN_SCALE, tx: 0, ty: 0 }
    }
    const stage = stageRef.current
    const img = imgRef.current
    if (!stage || !img) {
      return { scale: safeScale, tx, ty }
    }
    const stageW = stage.clientWidth || stage.getBoundingClientRect().width
    const stageH = stage.clientHeight || stage.getBoundingClientRect().height
    const baseW = img.offsetWidth || img.clientWidth
    const baseH = img.offsetHeight || img.clientHeight
    if (!stageW || !stageH || !baseW || !baseH) {
      return { scale: safeScale, tx, ty }
    }
    const maxX = Math.max(0, (baseW * safeScale - stageW) / 2)
    const maxY = Math.max(0, (baseH * safeScale - stageH) / 2)
    return {
      scale: safeScale,
      tx: clamp(tx, -maxX, maxX),
      ty: clamp(ty, -maxY, maxY),
    }
  }, [])

  const zoomAroundPoint = useCallback((targetScale: number, clientX: number, clientY: number, animate = true) => {
    const stage = stageRef.current
    const clampedScale = clamp(targetScale, MIN_SCALE, MAX_SCALE)
    if (!stage || clampedScale <= MIN_SCALE + 0.001) {
      applyTransform({ scale: MIN_SCALE, tx: 0, ty: 0 }, animate)
      return
    }
    const rect = stage.getBoundingClientRect()
    const cx = rect.left + rect.width / 2
    const cy = rect.top + rect.height / 2
    const current = transformRef.current
    const ox = clientX - cx
    const oy = clientY - cy
    const lx = (ox - current.tx) / current.scale
    const ly = (oy - current.ty) / current.scale
    const nextTx = ox - lx * clampedScale
    const nextTy = oy - ly * clampedScale
    applyTransform(clampTransform(clampedScale, nextTx, nextTy), animate)
  }, [applyTransform, clampTransform])

  const stepZoom = useCallback((delta: number) => {
    const stage = stageRef.current
    if (!stage) {
      applyTransform(clampTransform(transformRef.current.scale + delta, 0, 0), true)
      return
    }
    const rect = stage.getBoundingClientRect()
    zoomAroundPoint(transformRef.current.scale + delta, rect.left + rect.width / 2, rect.top + rect.height / 2, true)
  }, [applyTransform, clampTransform, zoomAroundPoint])

  // Open/close native modal <dialog> cleanly (with JSDOM fallback).
  useEffect(() => {
    if (!activeSrc) return
    const dialog = dialogRef.current
    if (!dialog) return
    if (typeof dialog.showModal === 'function') {
      if (!dialog.open) {
        try {
          dialog.showModal()
        } catch {
          dialog.setAttribute('open', '')
        }
      }
    } else {
      dialog.setAttribute('open', '')
    }
    return () => {
      if (dialog.open) {
        if (typeof dialog.close === 'function') {
          dialog.close()
        } else {
          dialog.removeAttribute('open')
        }
      }
    }
  }, [Boolean(activeSrc)])

  // Keyboard shortcuts for desktop & external keyboards: arrows navigate gallery, +/-/0 control zoom.
  useEffect(() => {
    if (!activeSrc) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'ArrowLeft' && hasMultiple) {
        event.preventDefault()
        setActiveIndex(currentIndex - 1)
      } else if (event.key === 'ArrowRight' && hasMultiple) {
        event.preventDefault()
        setActiveIndex(currentIndex + 1)
      } else if (event.key === '+' || event.key === '=') {
        event.preventDefault()
        stepZoom(0.5)
      } else if (event.key === '-' || event.key === '_') {
        event.preventDefault()
        stepZoom(-0.5)
      } else if (event.key === '0') {
        event.preventDefault()
        applyTransform({ scale: 1, tx: 0, ty: 0 }, true)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [activeSrc, hasMultiple, currentIndex, setActiveIndex, stepZoom, applyTransform])

  // Reset transform & swipe state whenever active image changes.
  useEffect(() => {
    pointersRef.current.clear()
    pinchRef.current = null
    panRef.current = null
    hadMultiTouchRef.current = false
    lastTapRef.current = null
    setSwipeOffsetX(0)
    applyTransform({ scale: 1, tx: 0, ty: 0 }, false)
  }, [activeSrc, applyTransform])

  // Load image bytes with authentication for /api/v1/* endpoints; pass data:/blob:/http(s) directly.
  useEffect(() => {
    if (!activeSrc) {
      setResolvedSrc('')
      setLoading(false)
      setError('')
      return
    }
    if (!isApiImage) {
      setResolvedSrc(activeSrc)
      setLoading(false)
      setError('')
      return
    }
    const controller = new AbortController()
    let objectUrl = ''
    setLoading(true)
    setError('')
    setResolvedSrc('')
    void (async () => {
      try {
        const response = await authenticatedApiFetch(activeSrc, { signal: controller.signal })
        if (!response.ok) throw new Error(`读取图片失败 (${response.status})`)
        const blob = await response.blob()
        if (controller.signal.aborted) return
        const type = imageMediaType(label)
          || (blob.type.startsWith('image/') ? blob.type : 'application/octet-stream')
        objectUrl = URL.createObjectURL(new Blob([blob], { type }))
        setResolvedSrc(objectUrl)
      } catch (reason) {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : '图片读取失败')
        setLoading(false)
      }
    })()
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [activeSrc, isApiImage, label, revision])

  // Prevent iOS Safari native pinch-zoom / rubber-band scroll and support mouse-wheel zoom on stage.
  useEffect(() => {
    if (!activeSrc) return
    const stage = stageRef.current
    if (!stage) return

    const preventGesture = (event: Event) => {
      event.preventDefault()
    }
    const preventTouchMove = (event: TouchEvent) => {
      event.preventDefault()
    }
    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      const factor = event.deltaY < 0 ? 1.18 : 1 / 1.18
      zoomAroundPoint(transformRef.current.scale * factor, event.clientX, event.clientY, false)
    }

    stage.addEventListener('touchmove', preventTouchMove, { passive: false })
    stage.addEventListener('gesturestart', preventGesture, { passive: false })
    stage.addEventListener('gesturechange', preventGesture, { passive: false })
    stage.addEventListener('wheel', onWheel, { passive: false })
    return () => {
      stage.removeEventListener('touchmove', preventTouchMove)
      stage.removeEventListener('gesturestart', preventGesture)
      stage.removeEventListener('gesturechange', preventGesture)
      stage.removeEventListener('wheel', onWheel)
    }
  }, [activeSrc, zoomAroundPoint])

  const requestClose = useCallback(() => {
    const dialog = dialogRef.current
    if (dialog && dialog.open && typeof dialog.close === 'function') {
      dialog.close()
      return
    }
    closeRef.current()
  }, [])

  const handlePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (event.pointerType === 'mouse' && event.button !== 0) return
    const stage = stageRef.current
    if (!stage) return
    try {
      stage.setPointerCapture(event.pointerId)
    } catch {
      // Synthetic pointer events in tests may not support pointer capture.
    }
    pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })
    setAnimating(false)
    setSwipeOffsetX(0)

    if (pointersRef.current.size === 1) {
      hadMultiTouchRef.current = false
      const current = transformRef.current
      panRef.current = {
        startX: event.clientX,
        startY: event.clientY,
        startTx: current.tx,
        startTy: current.ty,
        moved: false,
        swiping: false,
        fromBackdrop: event.target === stage,
      }
      pinchRef.current = null
      return
    }

    if (pointersRef.current.size === 2) {
      hadMultiTouchRef.current = true
      panRef.current = null
      const [p1, p2] = Array.from(pointersRef.current.values())
      const current = transformRef.current
      pinchRef.current = {
        startDist: Math.max(1, distanceBetween(p1, p2)),
        startMid: midpointBetween(p1, p2),
        startScale: current.scale,
        startTx: current.tx,
        startTy: current.ty,
      }
    }
  }

  const handlePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!pointersRef.current.has(event.pointerId)) return
    pointersRef.current.set(event.pointerId, { x: event.clientX, y: event.clientY })

    const stage = stageRef.current
    if (pointersRef.current.size >= 2 && pinchRef.current && stage) {
      const [p1, p2] = Array.from(pointersRef.current.values())
      const dist = Math.max(1, distanceBetween(p1, p2))
      const mid = midpointBetween(p1, p2)
      const pinch = pinchRef.current
      const rawScale = clamp(pinch.startScale * (dist / pinch.startDist), RUBBER_MIN_SCALE, MAX_SCALE)
      const rect = stage.getBoundingClientRect()
      const cx = rect.left + rect.width / 2
      const cy = rect.top + rect.height / 2
      const ox = pinch.startMid.x - cx
      const oy = pinch.startMid.y - cy
      const mx = mid.x - cx
      const my = mid.y - cy
      const lx = (ox - pinch.startTx) / pinch.startScale
      const ly = (oy - pinch.startTy) / pinch.startScale
      const nextTx = mx - lx * rawScale
      const nextTy = my - ly * rawScale
      if (rawScale >= MIN_SCALE) {
        applyTransform(clampTransform(rawScale, nextTx, nextTy), false)
      } else {
        applyTransform({ scale: rawScale, tx: nextTx * 0.25, ty: nextTy * 0.25 }, false)
      }
      return
    }

    if (pointersRef.current.size === 1 && panRef.current) {
      const pan = panRef.current
      const dx = event.clientX - pan.startX
      const dy = event.clientY - pan.startY
      if (!pan.moved && Math.hypot(dx, dy) > 6) {
        pan.moved = true
      }
      if (transformRef.current.scale > MIN_SCALE + 0.001) {
        applyTransform(clampTransform(transformRef.current.scale, pan.startTx + dx, pan.startTy + dy), false)
      } else if (hasMultiple && (pan.swiping || (Math.abs(dx) > 10 && Math.abs(dx) > Math.abs(dy) * 1.15))) {
        pan.swiping = true
        const atEdge = (dx > 0 && currentIndex === 0) || (dx < 0 && currentIndex === gallery.length - 1)
        setSwipeOffsetX(atEdge ? dx * 0.28 : dx)
      }
    }
  }

  const handlePointerEnd = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!pointersRef.current.has(event.pointerId)) return
    pointersRef.current.delete(event.pointerId)

    // Smoothly transition from 2-finger pinch to 1-finger pan without jumping.
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
        swiping: false,
        fromBackdrop: false,
      }
      return
    }

    if (pointersRef.current.size === 0) {
      const pan = panRef.current
      const wasMultiTouch = hadMultiTouchRef.current
      pinchRef.current = null
      panRef.current = null
      hadMultiTouchRef.current = false

      if (pan?.swiping && hasMultiple && transformRef.current.scale <= MIN_SCALE + 0.02) {
        const dx = event.clientX - pan.startX
        setSwipeOffsetX(0)
        if (event.type !== 'pointercancel' && dx <= -SWIPE_THRESHOLD_PX && currentIndex < gallery.length - 1) {
          setActiveIndex(currentIndex + 1)
          return
        }
        if (event.type !== 'pointercancel' && dx >= SWIPE_THRESHOLD_PX && currentIndex > 0) {
          setActiveIndex(currentIndex - 1)
          return
        }
        setAnimating(true)
        return
      }
      setSwipeOffsetX(0)

      const current = transformRef.current
      if (current.scale < MIN_SCALE + 0.02) {
        if (current.scale !== MIN_SCALE || current.tx !== 0 || current.ty !== 0) {
          applyTransform({ scale: MIN_SCALE, tx: 0, ty: 0 }, true)
        }
      } else {
        const clamped = clampTransform(current.scale, current.tx, current.ty)
        if (clamped.tx !== current.tx || clamped.ty !== current.ty || clamped.scale !== current.scale) {
          applyTransform(clamped, true)
        }
      }

      if (event.type === 'pointercancel' || wasMultiTouch || !pan || pan.moved) {
        return
      }

      // Single tap on dark backdrop outside the image dismisses at 1x zoom.
      if (pan.fromBackdrop && current.scale <= MIN_SCALE + 0.02) {
        lastTapRef.current = null
        requestClose()
        return
      }

      // Double tap/click on image toggles between 1x and 2.5x anchored at tap position.
      const now = Date.now()
      const previousTap = lastTapRef.current
      if (
        previousTap
        && now - previousTap.time <= DOUBLE_TAP_MS
        && Math.hypot(event.clientX - previousTap.x, event.clientY - previousTap.y) <= DOUBLE_TAP_DIST_PX
      ) {
        lastTapRef.current = null
        if (current.scale > MIN_SCALE + 0.05) {
          applyTransform({ scale: MIN_SCALE, tx: 0, ty: 0 }, true)
        } else {
          zoomAroundPoint(DOUBLE_TAP_SCALE, event.clientX, event.clientY, true)
        }
      } else {
        lastTapRef.current = { time: now, x: event.clientX, y: event.clientY }
      }
    }
  }

  const handleDownload = () => {
    if (!activeSrc) return
    if (isApiImage) {
      void downloadFile(activeSrc, label)
      return
    }
    const link = document.createElement('a')
    link.href = activeSrc
    link.download = label === '图片预览' ? 'image.png' : label
    link.click()
  }

  if (!activeSrc) return null

  const zoomPercent = Math.round(transform.scale * 100)
  const translateX = transform.tx + swipeOffsetX

  return (
    <dialog
      ref={dialogRef}
      className="m-lightbox"
      aria-label="图片预览"
      {...({ closedby: 'any' } as Record<string, string>)}
      onClose={() => closeRef.current()}
    >
      <header className="m-lightbox-header">
        <div className="m-lightbox-title-group">
          {hasMultiple && (
            <span className="m-lightbox-counter" aria-label={`第 ${currentIndex + 1} 张，共 ${gallery.length} 张`}>
              {currentIndex + 1} / {gallery.length}
            </span>
          )}
          <span className="m-lightbox-title" title={label}>{label}</span>
        </div>
        <button
          type="button"
          className="m-lightbox-close"
          aria-label="关闭图片"
          autoFocus
          onClick={requestClose}
        >
          <IconX size={22} aria-hidden="true" />
        </button>
      </header>

      <div
        ref={stageRef}
        className="m-lightbox-stage"
        data-scale={transform.scale.toFixed(2)}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerEnd}
        onPointerCancel={handlePointerEnd}
      >
        {loading && !error && (
          <div className="m-lightbox-status" role="status" aria-live="polite">
            正在加载图片…
          </div>
        )}
        {error ? (
          <div className="m-lightbox-error" onPointerDown={(e) => e.stopPropagation()}>
            <p>{error}</p>
            <button
              type="button"
              className="m-lightbox-retry"
              onClick={() => setRevision((v) => v + 1)}
            >
              <IconRefresh size={16} aria-hidden="true" />
              <span>重试</span>
            </button>
          </div>
        ) : resolvedSrc ? (
          <img
            ref={imgRef}
            className={`m-lightbox-img${animating ? ' is-animating' : ''}`}
            src={resolvedSrc}
            alt={label}
            draggable={false}
            style={{
              transform: `translate3d(${translateX}px, ${transform.ty}px, 0) scale(${transform.scale})`,
            }}
            onLoad={() => setLoading(false)}
            onError={() => {
              setLoading(false)
              setError('图片解码失败，文件可能已损坏或格式不受支持')
            }}
          />
        ) : null}
      </div>

      <footer className="m-lightbox-controls" aria-label="图片缩放控制">
        {hasMultiple && (
          <button
            type="button"
            className="m-lightbox-ctrl-btn"
            aria-label="上一张图片"
            disabled={currentIndex <= 0}
            onClick={() => setActiveIndex(currentIndex - 1)}
          >
            <IconChevronLeft size={19} aria-hidden="true" />
          </button>
        )}
        <button
          type="button"
          className="m-lightbox-ctrl-btn"
          aria-label="缩小"
          disabled={transform.scale <= MIN_SCALE + 0.01}
          onClick={() => stepZoom(-0.5)}
        >
          <IconMinus size={18} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="m-lightbox-zoom-readout"
          aria-label="重置缩放"
          onClick={() => applyTransform({ scale: 1, tx: 0, ty: 0 }, true)}
        >
          {zoomPercent}%
        </button>
        <button
          type="button"
          className="m-lightbox-ctrl-btn"
          aria-label="放大"
          disabled={transform.scale >= MAX_SCALE - 0.01}
          onClick={() => stepZoom(0.5)}
        >
          <IconPlus size={18} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="m-lightbox-ctrl-btn"
          aria-label="下载图片"
          onClick={handleDownload}
        >
          <IconDownload size={18} aria-hidden="true" />
        </button>
        {hasMultiple && (
          <button
            type="button"
            className="m-lightbox-ctrl-btn"
            aria-label="下一张图片"
            disabled={currentIndex >= gallery.length - 1}
            onClick={() => setActiveIndex(currentIndex + 1)}
          >
            <IconChevronRight size={19} aria-hidden="true" />
          </button>
        )}
      </footer>
    </dialog>
  )
}
