import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import * as localVoiceStream from './localVoiceStream'
import { applyVoiceTranscript, useVoiceInput } from './useVoiceInput'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
function media() {
  const track = { stop: vi.fn() }
  const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [track] })
  vi.stubGlobal('navigator', Object.create(navigator, { mediaDevices: { value: { getUserMedia } } }))
  class Recorder {
    state = 'inactive'; mimeType = 'audio/webm'
    ondataavailable: ((e: { data: Blob }) => void) | null = null
    onstop: (() => void) | null = null
    start() { this.state = 'recording' }
    stop() { this.state = 'inactive'; this.ondataavailable?.({ data: new Blob(['audio']) }); this.onstop?.() }
  }
  vi.stubGlobal('MediaRecorder', Recorder)
  return { track, getUserMedia }
}
it('releases a late microphone permission grant after cancelling', async () => {
  const { track, getUserMedia } = media()
  let grant!: (s: unknown) => void
  getUserMedia.mockReturnValue(new Promise(resolve => { grant = resolve }))
  const onText = vi.fn()
  const { result } = renderHook(() => useVoiceInput('a', onText))
  act(() => { void result.current.start() })
  act(() => result.current.cancel())
  await act(async () => grant({ getTracks: () => [track] }))
  expect(track.stop).toHaveBeenCalledOnce()
  expect(result.current.status).toBe('idle')
  expect(onText).not.toHaveBeenCalled()
})
it('aborts transcription when switching sessions and never calls the new draft callback', async () => {
  media()
  let resolve!: (r: { text: string }) => void
  const transcribe = vi.spyOn(api, 'transcribeAudio').mockReturnValue(new Promise(done => { resolve = done }))
  const onText = vi.fn()
  const view = renderHook(({ scope }) => useVoiceInput(scope, onText), { initialProps: { scope: 'a' } })
  await act(async () => view.result.current.start())
  act(() => view.result.current.stop())
  view.rerender({ scope: 'b' })
  expect(transcribe.mock.calls[0][1]?.aborted).toBe(true)
  await act(async () => resolve({ text: '旧会话' }))
  expect(onText).not.toHaveBeenCalled()
})
it('preserves an error inline and permits another recording', async () => {
  media()
  vi.spyOn(api, 'transcribeAudio').mockRejectedValue(new Error('网关暂不可用'))
  const { result } = renderHook(() => useVoiceInput('a', vi.fn()))
  await act(async () => result.current.start())
  await act(async () => result.current.stop())
  expect(result.current.error).toBe('网关暂不可用')
  expect(result.current.status).toBe('idle')
  await act(async () => result.current.start())
  expect(result.current.status).toBe('recording')
})
it('stops microphone capture when the page is hidden', async () => {
  const { track } = media()
  const { result } = renderHook(() => useVoiceInput('a', vi.fn()))
  await act(async () => result.current.start())
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  expect(track.stop).toHaveBeenCalled()
  expect(result.current.status).toBe('idle')
})

it('replaces a growing local transcript without duplicating prior text', () => {
  let text = applyVoiceTranscript('已有内容', '甚至出现交易。', { kind: 'stream', previous: '' })
  expect(text).toBe('已有内容\n甚至出现交易。')
  text = applyVoiceTranscript(text, '甚至出现交易几乎停滞。', { kind: 'stream', previous: '甚至出现交易。' })
  expect(text).toBe('已有内容\n甚至出现交易几乎停滞。')
  text = applyVoiceTranscript(text, '', { kind: 'stream', previous: '甚至出现交易几乎停滞。' })
  expect(text).toBe('已有内容')
})

it('uses the local stream while the plugin is running and emits growing transcripts', async () => {
  media()
  vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
    id: 'r2t2-local-voice', name: 'R2T2 本地流式听写', enabled: true, state: 'running',
    available: true, progress: null, error: null, pid: 1234, device: 'Vulkan0',
    model: 'Confucius4-R2T2-Q8_0.gguf',
  })
  let callbacks!: localVoiceStream.LocalVoiceStreamCallbacks
  const controller = { stop: vi.fn(), cancel: vi.fn() }
  vi.spyOn(localVoiceStream, 'startLocalVoiceStream').mockImplementation(async (_media, next) => {
    callbacks = next
    return controller
  })
  const onText = vi.fn()
  const { result } = renderHook(() => useVoiceInput('a', onText))
  await act(async () => result.current.start())
  expect(result.current.status).toBe('recording')
  act(() => callbacks.onTranscript('甚至。'))
  act(() => callbacks.onTranscript('甚至出现交易。'))
  expect(onText).toHaveBeenNthCalledWith(1, '甚至。', { kind: 'stream', previous: '' })
  expect(onText).toHaveBeenNthCalledWith(2, '甚至出现交易。', { kind: 'stream', previous: '甚至。' })
  act(() => result.current.stop())
  expect(controller.stop).toHaveBeenCalledOnce()
  expect(result.current.status).toBe('transcribing')
  act(() => callbacks.onDone('甚至出现交易。'))
  expect(result.current.status).toBe('idle')
  onText.mockClear()
  await act(async () => result.current.start({ previewOnly: true }))
  act(() => callbacks.onTranscript('模板里的文字。'))
  expect(result.current.transcript).toBe('模板里的文字。')
  expect(onText).not.toHaveBeenCalled()
  act(() => result.current.stop())
  act(() => callbacks.onDone('模板里的文字。'))
  expect(onText).toHaveBeenCalledExactlyOnceWith('模板里的文字。', { kind: 'final' })
  onText.mockClear()
  await act(async () => result.current.start({ previewOnly: true }))
  act(() => callbacks.onTranscript('尚未完成'))
  act(() => result.current.stop({ submit: true }))
  expect(onText).not.toHaveBeenCalled()
  act(() => callbacks.onDone('最终发送文字。'))
  act(() => callbacks.onDone('重复结束'))
  expect(onText).toHaveBeenCalledExactlyOnceWith('最终发送文字。', { kind: 'final', submit: true })
  onText.mockClear()
  await act(async () => result.current.start({ previewOnly: true }))
  act(() => callbacks.onTranscript('取消的文字。'))
  act(() => result.current.cancel())
  expect(result.current.transcript).toBe('')
  expect(onText).not.toHaveBeenCalled()
})
