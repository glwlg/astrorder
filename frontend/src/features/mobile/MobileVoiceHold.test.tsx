import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MobileVoiceHold } from './MobileVoiceHold'

afterEach(() => { cleanup(); vi.useRealTimers() })
it('leaves taps alone, starts on hold and stops on release without a dialog', () => {
  vi.useFakeTimers()
  const voice = { status: 'idle' as const, transcript: '', error: null, start: vi.fn(), stop: vi.fn(), cancel: vi.fn(), toggle: vi.fn() }
  const view = render(<MobileVoiceHold scope="a" voice={voice}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  const input = screen.getByLabelText('消息内容')
  fireEvent.pointerDown(input, { pointerId: 1, pointerType: 'touch', clientX: 180, clientY: 600 })
  fireEvent.pointerUp(input, { pointerId: 1 })
  act(() => vi.advanceTimersByTime(400))
  expect(voice.start).not.toHaveBeenCalled()
  fireEvent.pointerDown(input, { pointerId: 2, pointerType: 'touch', clientX: 180, clientY: 600 })
  act(() => vi.advanceTimersByTime(400))
  expect(voice.start).toHaveBeenCalledOnce()
  expect(voice.start).toHaveBeenCalledWith({ previewOnly: true })
  view.rerender(<MobileVoiceHold scope="a" voice={{ ...voice, status: 'recording', transcript: '甚至出现交易。' }}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  expect(screen.getByText('甚至出现交易。')).toHaveClass('mobile-voice-transcript')
  expect(screen.getByText('松手发送')).toBeInTheDocument()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  fireEvent.pointerUp(input, { pointerId: 2 })
  expect(voice.stop).toHaveBeenCalledOnce()
  expect(voice.stop).toHaveBeenCalledWith({ submit: true })
})
it('cancels when sliding up left and never transcribes', () => {
  vi.useFakeTimers()
  const voice = { status: 'idle' as const, transcript: '', error: null, start: vi.fn(), stop: vi.fn(), cancel: vi.fn(), toggle: vi.fn() }
  const view = render(<MobileVoiceHold scope="a" voice={voice}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  const input = screen.getByLabelText('消息内容')
  fireEvent.pointerDown(input, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 600 })
  act(() => vi.advanceTimersByTime(400))
  view.rerender(<MobileVoiceHold scope="a" voice={{ ...voice, status: 'recording' }}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  fireEvent.pointerMove(input, { pointerId: 1, clientX: 20, clientY: 400 })
  expect(screen.getByText('松手取消')).toBeInTheDocument()
  fireEvent.pointerUp(input, { pointerId: 1 })
  expect(voice.cancel).toHaveBeenCalled()
  expect(voice.stop).not.toHaveBeenCalled()
})
it('keeps the edit gesture as dictation without automatic sending', () => {
  vi.useFakeTimers()
  const voice = { status: 'idle' as const, transcript: '', error: null, start: vi.fn(), stop: vi.fn(), cancel: vi.fn(), toggle: vi.fn() }
  const view = render(<MobileVoiceHold scope="a" voice={voice}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  const input = screen.getByLabelText('消息内容')
  fireEvent.pointerDown(input, { pointerId: 1, pointerType: 'touch', clientX: 200, clientY: 600 })
  act(() => vi.advanceTimersByTime(400))
  view.rerender(<MobileVoiceHold scope="a" voice={{ ...voice, status: 'recording' }}><textarea aria-label="消息内容" /></MobileVoiceHold>)
  fireEvent.pointerMove(input, { pointerId: 1, clientX: window.innerWidth - 20, clientY: 400 })
  fireEvent.pointerUp(input, { pointerId: 1 })
  expect(voice.stop).toHaveBeenCalledExactlyOnceWith({ submit: false })
  expect(input).toHaveFocus()
})
