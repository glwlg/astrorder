import { IconMicrophone, IconPlayerStop, IconRefresh, IconX } from '@tabler/icons-react'
import { Alert, Button, Drawer, Group, Stack, Text, Textarea } from '@mantine/core'
import { useEffect, useRef, useState } from 'react'

type RecorderStatus = 'idle' | 'requesting' | 'recording' | 'preview' | 'error'

type SpeechRecognitionLike = {
  continuous: boolean
  interimResults: boolean
  lang: string
  onresult: ((event: { results: ArrayLike<ArrayLike<{ transcript: string }>> }) => void) | null
  start: () => void
  stop: () => void
}

type SpeechRecognitionConstructor = new () => SpeechRecognitionLike

type SpeechWindow = Window & {
  SpeechRecognition?: SpeechRecognitionConstructor
  webkitSpeechRecognition?: SpeechRecognitionConstructor
}

export interface VoiceInputSheetProps {
  opened: boolean
  onClose: () => void
  onCommit: (file: File, transcript: string) => void
}

function permissionMessage(error: unknown): string {
  if (error && typeof error === 'object' && 'name' in error && (error as { name?: string }).name === 'NotAllowedError') {
    return '麦克风权限被拒绝或未提供。'
  }
  return '无法访问麦克风；可以取消，或检查浏览器权限后重试。'
}

export function VoiceInputSheet({ opened, onClose, onCommit }: VoiceInputSheetProps) {
  const [status, setStatus] = useState<RecorderStatus>('idle')
  const [error, setError] = useState<string | null>(null)
  const [transcript, setTranscript] = useState('')
  const [recordedBlob, setRecordedBlob] = useState<Blob | null>(null)
  const [audioUrl, setAudioUrl] = useState<string | null>(null)
  const recorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const speechRef = useRef<SpeechRecognitionLike | null>(null)
  const chunksRef = useRef<Blob[]>([])

  const releaseMedia = () => {
    streamRef.current?.getTracks().forEach((track) => track.stop())
    streamRef.current = null
    speechRef.current?.stop()
    speechRef.current = null
    recorderRef.current = null
  }

  const clearPreview = () => {
    if (audioUrl) URL.revokeObjectURL(audioUrl)
    setAudioUrl(null)
    setRecordedBlob(null)
    setTranscript('')
  }

  useEffect(() => () => {
    releaseMedia()
    if (audioUrl) URL.revokeObjectURL(audioUrl)
  }, [audioUrl])

  const start = async () => {
    setError(null)
    setStatus('requesting')
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') {
      setError('当前浏览器未提供录音能力。')
      setStatus('error')
      return
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      const recorder = new MediaRecorder(stream)
      streamRef.current = stream
      recorderRef.current = recorder
      chunksRef.current = []
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) chunksRef.current.push(event.data)
      }
      recorder.onstop = () => {
        const blob = new Blob(chunksRef.current, { type: recorder.mimeType || 'audio/webm' })
        setRecordedBlob(blob)
        setAudioUrl(URL.createObjectURL(blob))
        setStatus('preview')
        releaseMedia()
      }
      recorder.start()
      const SpeechRecognition = (window as SpeechWindow).SpeechRecognition || (window as SpeechWindow).webkitSpeechRecognition
      if (SpeechRecognition) {
        const speech = new SpeechRecognition()
        speech.continuous = true
        speech.interimResults = true
        speech.lang = 'zh-CN'
        speech.onresult = (event) => {
          const text = Array.from({ length: event.results.length }, (_, index) => event.results[index][0]?.transcript || '').join('')
          setTranscript(text.trim())
        }
        speechRef.current = speech
        try { speech.start() } catch { /* permission is already represented by MediaRecorder */ }
      }
      setStatus('recording')
    } catch (nextError) {
      releaseMedia()
      setError(permissionMessage(nextError))
      setStatus('error')
    }
  }

  const stop = () => {
    if (recorderRef.current && status === 'recording') {
      recorderRef.current.stop()
      return
    }
    setStatus('preview')
  }

  const cancel = () => {
    if (recorderRef.current && status === 'recording') {
      recorderRef.current.onstop = null
      recorderRef.current.stop()
    }
    releaseMedia()
    clearPreview()
    setError(null)
    setStatus('idle')
    onClose()
  }

  const commit = () => {
    if (!recordedBlob) return
    const file = new File([recordedBlob], `astrorder-voice-${Date.now()}.webm`, { type: recordedBlob.type || 'audio/webm' })
    onCommit(file, transcript.trim())
    clearPreview()
    setStatus('idle')
    onClose()
  }

  return (
    <Drawer
      opened={opened}
      onClose={cancel}
      position="bottom"
      size="min(72vh, 460px)"
      title="语音输入"
      closeButtonProps={{ 'aria-label': '关闭语音输入' }}
    >
      <Stack gap="md">
        <Text size="sm" c="dimmed">录音只会先进入草稿预览，不会自动发送。浏览器不支持转写时仍可保留音频附件。</Text>
        {error && <Alert color="red" icon={<IconX size={17} />} aria-live="assertive">{error}</Alert>}
        {status === 'idle' && <Button leftSection={<IconMicrophone size={17} />} onClick={() => void start()}>开始录音</Button>}
        {status === 'requesting' && <Button loading disabled>正在请求麦克风权限</Button>}
        {status === 'recording' && <Group>
          <Button color="red" leftSection={<IconPlayerStop size={17} />} onClick={stop}>停止录音</Button>
          <Text size="sm" c="dimmed">正在录音；完成后可以试听或取消。</Text>
        </Group>}
        {status === 'error' && <Button variant="light" leftSection={<IconRefresh size={17} />} onClick={() => void start()}>重试录音</Button>}
        {status === 'preview' && <Stack gap="sm">
          {audioUrl && <audio controls src={audioUrl} aria-label="录音预览" />}
          <Textarea
            label="转写文本"
            placeholder="未提供转写文本；确认后将仅保留录音附件。"
            value={transcript}
            onChange={(event) => setTranscript(event.currentTarget.value)}
            minRows={2}
          />
          <Group>
            <Button onClick={commit} disabled={!recordedBlob}>保留到草稿</Button>
            <Button variant="subtle" onClick={cancel}>取消</Button>
          </Group>
        </Stack>}
        {status !== 'preview' && status !== 'recording' && <Button variant="subtle" onClick={cancel}>取消</Button>}
      </Stack>
    </Drawer>
  )
}
