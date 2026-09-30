import { useEffect, useRef, useState } from 'react'
import { api } from '../../api/client'
import { startLocalVoiceStream, type LocalVoiceStreamController } from './localVoiceStream'

export type VoiceTranscriptEvent =
  | { kind: 'final'; submit?: boolean }
  | { kind: 'stream'; previous: string }

export function applyVoiceTranscript(current: string, transcript: string, event: VoiceTranscriptEvent): string {
  if (event.kind === 'final') return current + (current ? '\n' : '') + transcript
  let base = event.previous && current.endsWith(event.previous)
    ? current.slice(0, -event.previous.length)
    : current
  if (event.previous && !transcript && base.endsWith('\n')) base = base.slice(0, -1)
  return base + (!event.previous && base ? '\n' : '') + transcript
}

export function useVoiceInput(scope: string, onText: (text: string, event: VoiceTranscriptEvent) => void) {
  const [status, setStatus] = useState<'idle' | 'requesting' | 'recording' | 'transcribing'>('idle')
  const [error, setError] = useState<string | null>(null)
  const [transcript, setTranscript] = useState('')
  const previewOnly = useRef(false)
  const submitOnComplete = useRef(false)
  const current = useRef({ scope, onText })
  current.current = { scope, onText }
  const generation = useRef(0)
  const recorder = useRef<MediaRecorder | null>(null)
  const stream = useRef<MediaStream | null>(null)
  const localStream = useRef<LocalVoiceStreamController | null>(null)
  const localTranscript = useRef('')
  const request = useRef<AbortController | null>(null)
  const active = useRef(false)

  const dispose = (rollbackStream = false) => {
    generation.current++
    active.current = false
    request.current?.abort()
    request.current = null
    if (rollbackStream && localTranscript.current && !previewOnly.current) {
      current.current.onText('', { kind: 'stream', previous: localTranscript.current })
    }
    localTranscript.current = ''
    localStream.current?.cancel()
    localStream.current = null
    const rec = recorder.current
    recorder.current = null
    if (rec) {
      rec.onstop = null
      rec.ondataavailable = null
      if (rec.state === 'recording') rec.stop()
    }
    stream.current?.getTracks().forEach(track => track.stop())
    stream.current = null
  }
  const cancel = () => { dispose(true); setTranscript(''); setStatus('idle') }
  useEffect(() => {
    setStatus('idle')
    setError(null)
    setTranscript('')
    const hidden = () => { if (document.visibilityState === 'hidden') cancel() }
    const pagehide = () => cancel()
    document.addEventListener('visibilitychange', hidden)
    window.addEventListener('pagehide', pagehide)
    return () => {
      document.removeEventListener('visibilitychange', hidden)
      window.removeEventListener('pagehide', pagehide)
      dispose()
    }
  }, [scope])

  const start = async (options?: { previewOnly?: boolean }) => {
    if (active.current) return
    previewOnly.current = Boolean(options?.previewOnly)
    submitOnComplete.current = false
    setTranscript('')
    active.current = true
    const token = ++generation.current
    const valid = () => token === generation.current && scope === current.current.scope
    setError(null)
    setStatus('requesting')
    try {
      if (!navigator.mediaDevices?.getUserMedia) throw new Error('当前浏览器无法录音，请使用 HTTPS 并允许麦克风权限。')
      const media = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (!valid()) { media.getTracks().forEach(track => track.stop()); return }
      stream.current = media
      const localStatus = await api.getLocalVoicePlugin().catch(() => null)
      if (!valid()) { media.getTracks().forEach(track => track.stop()); return }
      if (localStatus?.state === 'running') {
        const controller = await startLocalVoiceStream(media, {
          onTranscript: text => {
            if (!valid() || text === localTranscript.current) return
            const previous = localTranscript.current
            localTranscript.current = text
            setTranscript(text)
            if (!previewOnly.current) current.current.onText(text, { kind: 'stream', previous })
          },
          onDone: text => {
            if (!valid()) return
            if (previewOnly.current) {
              const finalText = text || localTranscript.current
              if (finalText) current.current.onText(finalText, submitOnComplete.current ? { kind: 'final', submit: true } : { kind: 'final' })
            } else if (text && text !== localTranscript.current) {
              const previous = localTranscript.current
              current.current.onText(text, { kind: 'stream', previous })
            }
            if (!text && !localTranscript.current) setError('未识别到语音，请重试。')
            localTranscript.current = ''
            localStream.current = null
            stream.current = null
            active.current = false
            generation.current++
            setStatus('idle')
          },
          onError: message => {
            if (!valid()) return
            localStream.current = null
            stream.current = null
            active.current = false
            setError(message)
            setStatus('idle')
          },
        })
        if (!valid()) { controller.cancel(); return }
        localStream.current = controller
        setStatus('recording')
        return
      }
      if (typeof MediaRecorder === 'undefined') throw new Error('当前浏览器无法录音，请使用 HTTPS 并允许麦克风权限。')
      const rec = new MediaRecorder(media)
      recorder.current = rec
      const chunks: Blob[] = []
      rec.ondataavailable = event => { if (event.data.size) chunks.push(event.data) }
      rec.onstop = async () => {
        media.getTracks().forEach(track => track.stop())
        stream.current = null
        recorder.current = null
        if (!valid()) return
        setStatus('transcribing')
        const controller = new AbortController()
        request.current = controller
        try {
          const mime = rec.mimeType || 'audio/webm'
          const ext = mime.includes('mp4') ? 'm4a' : mime.includes('ogg') ? 'ogg' : 'webm'
          const file = new File(chunks, `recording.${ext}`, { type: mime })
          const result = await api.transcribeAudio(file, controller.signal)
          if (!valid()) return
          if (result.text.trim()) current.current.onText(result.text.trim(), submitOnComplete.current ? { kind: 'final', submit: true } : { kind: 'final' })
          else setError('未识别到语音，请重试。')
        } catch (err) {
          if (valid()) setError(err instanceof Error ? err.message : '转写失败，请重试。')
        } finally {
          if (valid()) { active.current = false; setStatus('idle') }
        }
      }
      rec.start()
      setStatus('recording')
    } catch (err) {
      if (!valid()) return
      dispose()
      setStatus('idle')
      setError(err instanceof DOMException && err.name === 'NotAllowedError' ? '麦克风权限被拒绝，请在浏览器设置中允许录音。' : err instanceof Error ? err.message : '无法录音，请重试。')
    }
  }
  const stop = (options?: { submit?: boolean }) => {
    submitOnComplete.current = Boolean(options?.submit)
    if (localStream.current) {
      setStatus('transcribing')
      localStream.current.stop()
    } else if (recorder.current?.state === 'recording') recorder.current.stop()
    else if (status === 'requesting') cancel()
  }
  return { status, error, transcript, start, stop, cancel, toggle: () => { if (active.current) stop(); else void start() } }
}
