import { useEffect, useRef, useState, type ReactNode, type PointerEvent } from 'react'
import { createPortal } from 'react-dom'
import { IconMicrophone, IconPencil, IconX } from '@tabler/icons-react'
import type { useVoiceInput } from '../chat/useVoiceInput'
import './mobileVoice.css'

type Zone = 'draft' | 'cancel' | 'edit'
export function MobileVoiceHold({ scope, voice, children }: { scope: string; voice: ReturnType<typeof useVoiceInput>; children: ReactNode }) {
  const latest = useRef(voice)
  latest.current = voice
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const gesture = useRef<{ id: number; x: number; y: number; active: boolean; zone: Zone; target: HTMLElement } | null>(null)
  const suppressClick = useRef(false)
  const [holding, setHolding] = useState(false)
  const [zone, setZone] = useState<Zone>('draft')
  const clearTimer = () => { if (timer.current) clearTimeout(timer.current); timer.current = null }
  const release = (cancel: boolean) => {
    clearTimer()
    const held = gesture.current
    gesture.current = null
    setHolding(false)
    if (!held?.active) return
    if (cancel || held.zone === 'cancel') latest.current.cancel()
    else {
      latest.current.stop({ submit: held.zone === 'draft' })
      if (held.zone === 'edit') held.target.focus()
    }
    if (held.target.hasPointerCapture?.(held.id)) held.target.releasePointerCapture(held.id)
  }
  useEffect(() => {
    setHolding(false)
    return () => { clearTimer(); gesture.current = null }
  }, [scope])
  const down = (event: PointerEvent<HTMLDivElement>) => {
    if (event.pointerType !== 'touch' || gesture.current || voice.status !== 'idle') return
    const target = event.target as HTMLElement
    if (target.tagName !== 'TEXTAREA') return
    suppressClick.current = false
    gesture.current = { id: event.pointerId, x: event.clientX, y: event.clientY, active: false, zone: 'draft', target }
    timer.current = setTimeout(() => {
      const held = gesture.current
      if (!held) return
      held.active = true
      suppressClick.current = true
      held.target.blur()
      held.target.setPointerCapture?.(held.id)
      setZone('draft')
      setHolding(true)
      void latest.current.start({ previewOnly: true })
    }, 350)
  }
  const move = (event: PointerEvent<HTMLDivElement>) => {
    const held = gesture.current
    if (!held || event.pointerId !== held.id) return
    if (!held.active) {
      if (Math.hypot(event.clientX - held.x, event.clientY - held.y) > 12) { clearTimer(); gesture.current = null }
      return
    }
    event.preventDefault()
    held.zone = event.clientY < held.y - 45 ? (event.clientX < window.innerWidth / 2 ? 'cancel' : 'edit') : 'draft'
    setZone(held.zone)
  }
  const up = (event: PointerEvent<HTMLDivElement>, cancel = false) => {
    if (event.pointerId !== gesture.current?.id) return
    if (gesture.current.active) event.preventDefault()
    release(cancel)
  }
  const visible = holding && (voice.status === 'recording' || voice.status === 'requesting')
  return <div className="mobile-voice-hold" onPointerDown={down} onPointerMove={move} onPointerUp={event => up(event)} onPointerCancel={event => up(event, true)} onLostPointerCapture={event => up(event, true)} onContextMenu={event => { if (gesture.current) event.preventDefault() }} onClickCapture={event => { if (suppressClick.current) { suppressClick.current = false; event.preventDefault(); event.stopPropagation() } }}>
    {children}
    {visible && createPortal(<div className={`mobile-voice-overlay is-${zone}`} aria-live="polite">
      <div className="mobile-voice-center">
        <div className={`mobile-voice-bubble${voice.transcript ? ' has-transcript' : ''}`}>
          {voice.transcript ? <div className="mobile-voice-transcript">{voice.transcript}</div> : <><IconMicrophone size={34} /><div className="mobile-voice-bars" aria-hidden="true">{Array.from({ length: 9 }, (_, i) => <i key={i} style={{ animationDelay: `${i * 85}ms` }} />)}</div></>}
        </div>
        <strong>{voice.status === 'requesting' ? '正在等待麦克风权限…' : zone === 'cancel' ? '松手取消' : zone === 'edit' ? '松手编辑文字' : '松手发送'}</strong>
        <small>左上滑取消 · 右上滑编辑</small>
      </div>
      <div className="mobile-voice-zones"><div className={zone === 'cancel' ? 'selected' : ''}><IconX size={28} />取消</div><div className={zone === 'edit' ? 'selected' : ''}><IconPencil size={26} />编辑</div></div>
      <div className="mobile-voice-arc" />
    </div>, document.body)}
  </div>
}
