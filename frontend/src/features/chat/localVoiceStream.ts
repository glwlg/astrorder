export interface LocalVoiceStreamController {
  stop: () => void
  cancel: () => void
}

export interface LocalVoiceStreamCallbacks {
  onTranscript: (text: string) => void
  onDone: (text: string) => void
  onError: (message: string) => void
}

export function buildLocalVoiceStreamUrl(): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let token: string | null = null
  try { token = localStorage.getItem('astrorder:token') } catch {}
  const query = token ? `?token=${encodeURIComponent(token)}` : ''
  return `${protocol}//${window.location.host}/api/v1/audio/local-stream${query}`
}

export function downsampleToPcm16(input: Float32Array, inputRate: number): Int16Array {
  if (inputRate <= 16000) {
    const output = new Int16Array(input.length)
    for (let index = 0; index < input.length; index++) {
      const sample = Math.max(-1, Math.min(1, input[index]))
      output[index] = sample < 0 ? sample * 32768 : sample * 32767
    }
    return output
  }
  const ratio = inputRate / 16000
  const length = Math.floor(input.length / ratio)
  const output = new Int16Array(length)
  for (let target = 0; target < length; target++) {
    const start = Math.floor(target * ratio)
    const end = Math.min(input.length, Math.floor((target + 1) * ratio))
    let sum = 0
    for (let source = start; source < end; source++) sum += input[source]
    const sample = Math.max(-1, Math.min(1, sum / Math.max(1, end - start)))
    output[target] = sample < 0 ? sample * 32768 : sample * 32767
  }
  return output
}

export async function startLocalVoiceStream(
  media: MediaStream,
  callbacks: LocalVoiceStreamCallbacks,
): Promise<LocalVoiceStreamController> {
  const AudioContextClass = window.AudioContext || (window as typeof window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (!AudioContextClass || typeof WebSocket === 'undefined') throw new Error('当前浏览器不支持本地流式听写。')

  const socket = new WebSocket(buildLocalVoiceStreamUrl())
  socket.binaryType = 'arraybuffer'
  const context = new AudioContextClass()
  const source = context.createMediaStreamSource(media)
  const processor = context.createScriptProcessor(4096, 1, 1)
  const mute = context.createGain()
  mute.gain.value = 0
  let ready = false
  let closed = false
  let audioDisposed = false

  const disposeAudio = () => {
    if (audioDisposed) return
    audioDisposed = true
    processor.onaudioprocess = null
    processor.disconnect()
    source.disconnect()
    mute.disconnect()
    media.getTracks().forEach(track => track.stop())
    void context.close()
  }
  const finish = () => {
    if (closed) return
    closed = true
    disposeAudio()
    socket.close(1000)
  }

  processor.onaudioprocess = event => {
    if (!ready || socket.readyState !== WebSocket.OPEN) return
    const pcm = downsampleToPcm16(event.inputBuffer.getChannelData(0), context.sampleRate)
    if (pcm.byteLength) socket.send(pcm.buffer as ArrayBuffer)
  }
  source.connect(processor)
  processor.connect(mute)
  mute.connect(context.destination)

  return await new Promise<LocalVoiceStreamController>((resolve, reject) => {
    const fail = (message: string, initial = false) => {
      if (closed) return
      finish()
      if (initial) reject(new Error(message))
      else callbacks.onError(message)
    }
    socket.onerror = () => fail('无法连接本地流式听写服务。', !ready)
    socket.onclose = () => {
      if (!closed) fail('本地流式听写连接已断开。', !ready)
    }
    socket.onmessage = event => {
      let message: { type?: string; text?: string; message?: string }
      try { message = JSON.parse(String(event.data)) as typeof message } catch { return }
      if (message.type === 'ready') {
        ready = true
        resolve({
          stop: () => {
            disposeAudio()
            if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'stop' }))
          },
          cancel: () => {
            finish()
            if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'cancel' }))
            socket.close(1000)
          },
        })
      } else if (message.type === 'transcript' && typeof message.text === 'string') {
        callbacks.onTranscript(message.text)
      } else if (message.type === 'done') {
        finish()
        callbacks.onDone(message.text || '')
      } else if (message.type === 'error') {
        fail(message.message || '本地流式听写失败。')
      }
    }
  })
}
