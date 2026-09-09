import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { VoiceInputSheet } from './VoiceInputSheet'

describe('VoiceInputSheet', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  function installMockMedia() {
    class FakeMediaRecorder {
      mimeType = 'audio/webm'
      ondataavailable: ((event: { data: Blob }) => void) | null = null
      onstop: (() => void) | null = null

      start = vi.fn()
      stop = vi.fn(() => {
        this.ondataavailable?.({ data: new Blob(['mock-audio'], { type: this.mimeType }) })
        this.onstop?.()
      })
    }
    const track = { stop: vi.fn() }
    const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [track] })
    vi.stubGlobal('MediaRecorder', FakeMediaRecorder)
    Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia } })
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:mock-audio')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
  }

  it('reports a microphone permission refusal without submitting', async () => {
    vi.stubGlobal('MediaRecorder', function FakeMediaRecorder() {})
    const getUserMedia = vi.fn().mockRejectedValue(Object.assign(new Error('denied'), { name: 'NotAllowedError' }))
    Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia } })
    const onCommit = vi.fn()

    render(
      <MantineProvider>
        <VoiceInputSheet opened onClose={vi.fn()} onCommit={onCommit} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
    expect(await screen.findByText('麦克风权限被拒绝或未提供。')).toBeInTheDocument()
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('does not submit when the preview is cancelled', () => {
    const onClose = vi.fn()
    const onCommit = vi.fn()
    render(
      <MantineProvider>
        <VoiceInputSheet opened onClose={onClose} onCommit={onCommit} />
      </MantineProvider>,
    )

    const dialog = screen.getAllByRole('dialog').at(-1)
    if (!dialog) throw new Error('voice dialog was not rendered')
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))
    expect(onCommit).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('records with mock media, lets the user edit the transcript, and commits only to draft', async () => {
    installMockMedia()
    const onCommit = vi.fn()
    render(
      <MantineProvider>
        <VoiceInputSheet opened onClose={vi.fn()} onCommit={onCommit} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
    expect(await screen.findByText(/正在录音/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '停止录音' }))
    expect(await screen.findByLabelText('录音预览')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('转写文本'), { target: { value: '编辑后的语音草稿' } })
    fireEvent.click(screen.getByRole('button', { name: '保留到草稿' }))

    expect(onCommit).toHaveBeenCalledOnce()
    expect(onCommit.mock.calls[0][0]).toBeInstanceOf(File)
    expect(onCommit.mock.calls[0][1]).toBe('编辑后的语音草稿')
  })

  it('keeps the parent draft untouched when a completed recording is cancelled', async () => {
    installMockMedia()
    const onCommit = vi.fn()
    const onClose = vi.fn()
    render(
      <MantineProvider>
        <VoiceInputSheet opened onClose={onClose} onCommit={onCommit} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '开始录音' }))
    await screen.findByText(/正在录音/)
    fireEvent.click(screen.getByRole('button', { name: '停止录音' }))
    const dialog = screen.getAllByRole('dialog').at(-1)
    if (!dialog) throw new Error('voice dialog was not rendered')
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))

    expect(onCommit).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })
})
