import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import {
  buildCapsuleDisplayText,
  ensureMeetingCapsuleActionListener,
  sampleCapsuleBars,
  useMeetingSessionStore,
} from './meetingSessionStore'
import { formatDurationClock } from './useMeetingLiveRecorder'
import './meetingFloatingCapsule.css'

const CAPSULE_WIDTH = 368
const CAPSULE_HEIGHT = 54
const VIEWPORT_MARGIN = 14

export function MeetingFloatingCapsule() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()

  const recordState = useMeetingSessionStore((s) => s.recordState)
  const elapsedSeconds = useMeetingSessionStore((s) => s.elapsedSeconds)
  const liveText = useMeetingSessionStore((s) => s.liveText)
  const waveform = useMeetingSessionStore((s) => s.waveform)
  const nativeCapsuleActive = useMeetingSessionStore((s) => s.nativeCapsuleActive)
  const inAppCapsuleForced = useMeetingSessionStore((s) => s.inAppCapsuleForced)
  const stopRecording = useMeetingSessionStore((s) => s.stopRecording)
  const dismissInAppCapsule = useMeetingSessionStore((s) => s.dismissInAppCapsule)

  const [pos, setPos] = useState<{ x: number; y: number } | null>(null)
  const dragRef = useRef<{ offsetX: number; offsetY: number; moved: boolean } | null>(null)
  const pillRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    ensureMeetingCapsuleActionListener()
    const onMeetingsChanged = () => {
      void queryClient.invalidateQueries({ queryKey: ['astrorder', 'meeting-action-items'] })
    }
    window.addEventListener('astrorder:meetings-changed', onMeetingsChanged)
    return () => {
      window.removeEventListener('astrorder:meetings-changed', onMeetingsChanged)
    }
  }, [queryClient])

  const isRecordingActive =
    recordState === 'starting' ||
    recordState === 'recording' ||
    recordState === 'paused' ||
    recordState === 'stopping'
  const isMeetingsRoute = location.pathname.startsWith('/meetings')
  const visible = isRecordingActive && !nativeCapsuleActive && (!isMeetingsRoute || inAppCapsuleForced)

  useEffect(() => {
    if (!visible) return
    const onResize = () => {
      setPos((prev) => {
        if (!prev) return prev
        const maxX = Math.max(VIEWPORT_MARGIN, window.innerWidth - CAPSULE_WIDTH - VIEWPORT_MARGIN)
        const maxY = Math.max(VIEWPORT_MARGIN, window.innerHeight - CAPSULE_HEIGHT - VIEWPORT_MARGIN)
        return {
          x: Math.min(Math.max(VIEWPORT_MARGIN, prev.x), maxX),
          y: Math.min(Math.max(VIEWPORT_MARGIN, prev.y), maxY),
        }
      })
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [visible])

  if (!visible) return null

  const bars = sampleCapsuleBars(waveform)
  const displayText = buildCapsuleDisplayText(recordState, liveText)
  const isPausedOrStopping = recordState === 'paused' || recordState === 'stopping'

  const handlePointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    if ((e.target as HTMLElement).closest('button')) return
    const rect = pillRef.current?.getBoundingClientRect()
    if (!rect) return
    dragRef.current = {
      offsetX: e.clientX - rect.left,
      offsetY: e.clientY - rect.top,
      moved: false,
    }
    const onMove = (moveEvent: PointerEvent) => {
      if (!dragRef.current) return
      dragRef.current.moved = true
      const maxX = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN)
      const maxY = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.height - VIEWPORT_MARGIN)
      const nextX = Math.min(Math.max(VIEWPORT_MARGIN, moveEvent.clientX - dragRef.current.offsetX), maxX)
      const nextY = Math.min(Math.max(VIEWPORT_MARGIN, moveEvent.clientY - dragRef.current.offsetY), maxY)
      setPos({ x: nextX, y: nextY })
    }
    const onUp = () => {
      dragRef.current = null
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  const handleExpand = () => {
    dismissInAppCapsule()
    if (!isMeetingsRoute) {
      navigate('/meetings')
    }
  }

  return (
    <div
      ref={pillRef}
      role="region"
      aria-label="独立录音胶囊"
      className={`mtg-float-capsule${isPausedOrStopping ? ' is-paused' : ''}`}
      style={pos ? { left: pos.x, top: pos.y, right: 'auto' } : undefined}
      onPointerDown={handlePointerDown}
      onDoubleClick={(e) => {
        if ((e.target as HTMLElement).closest('button')) return
        handleExpand()
      }}
      title="拖动调整位置，双击回到实时会议页"
    >
      <div className="mtg-float-wave" aria-hidden="true">
        {bars.map((amp, idx) => (
          <span
            key={idx}
            className="mtg-float-wave-bar"
            style={{ height: `${Math.round(4 + (isPausedOrStopping ? 0.22 : amp) * 14)}px` }}
          />
        ))}
      </div>

      <div className="mtg-float-copy">
        <span className="mtg-float-text">{displayText}</span>
        <span className="mtg-float-timer">{formatDurationClock(elapsedSeconds)}</span>
      </div>

      <div className="mtg-float-actions">
        <button
          type="button"
          className="mtg-float-circle-btn"
          aria-label="结束录制"
          title="结束录制并整理纪要"
          disabled={recordState === 'stopping'}
          onClick={(e) => {
            e.stopPropagation()
            stopRecording()
          }}
        >
          <span className="mtg-float-stop-square" />
        </button>

        <button
          type="button"
          className="mtg-float-circle-btn"
          aria-label="展开会议页面"
          title="回到实时会议工作台"
          onClick={(e) => {
            e.stopPropagation()
            handleExpand()
          }}
        >
          <svg className="mtg-float-expand-icon" viewBox="0 0 16 16" aria-hidden="true">
            <polyline points="9.5 2.5 13.5 2.5 13.5 6.5" />
            <polyline points="6.5 13.5 2.5 13.5 2.5 9.5" />
            <line x1="13.2" y1="2.8" x2="8.8" y2="7.2" />
            <line x1="2.8" y1="13.2" x2="7.2" y2="8.8" />
          </svg>
        </button>
      </div>
    </div>
  )
}
