import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { QrScanPage } from './QrScanPage'

describe('QrScanPage', () => {
  const stopTrackMock = vi.fn()
  const originalMediaDevices = navigator.mediaDevices

  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue()
    Object.defineProperty(navigator, 'mediaDevices', {
      writable: true,
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockResolvedValue({
          getTracks: () => [{ stop: stopTrackMock }],
        }),
      },
    })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    Object.defineProperty(navigator, 'mediaDevices', {
      writable: true,
      configurable: true,
      value: originalMediaDevices,
    })
  })

  it('stops camera media tracks on unmount', async () => {
    const view = render(
      <MemoryRouter>
        <MantineProvider>
          <QrScanPage onNavigate={vi.fn()} />
        </MantineProvider>
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(navigator.mediaDevices.getUserMedia).toHaveBeenCalled()
    })

    view.unmount()
    expect(stopTrackMock).toHaveBeenCalled()
  })

  it('binds the acquired camera stream to the mounted video', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue()
    const stream = { getTracks: () => [{ stop: stopTrackMock }] } as unknown as MediaStream
    vi.mocked(navigator.mediaDevices.getUserMedia).mockResolvedValue(stream)
    const view = render(<MemoryRouter><MantineProvider><QrScanPage /></MantineProvider></MemoryRouter>)
    await waitFor(() => expect(view.container.querySelector('video')?.srcObject).toBe(stream))
  })

  it('stops a camera stream that resolves after unmount', async () => {
    let resolve!: (stream: MediaStream) => void
    vi.mocked(navigator.mediaDevices.getUserMedia).mockReturnValue(new Promise(done => { resolve = done }))
    const view = render(<MemoryRouter><MantineProvider><QrScanPage /></MantineProvider></MemoryRouter>)
    view.unmount()
    const lateStop = vi.fn()
    resolve({ getTracks: () => [{ stop: lateStop }] } as unknown as MediaStream)
    await waitFor(() => expect(lateStop).toHaveBeenCalledOnce())
  })

  it('does not restart frame scanning after video play resolves on an unmounted page', async () => {
    let finishPlay!: () => void
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockReturnValue(new Promise(done => { finishPlay = done }))
    const frames = vi.spyOn(window, 'requestAnimationFrame')
    const view = render(<MemoryRouter><MantineProvider><QrScanPage /></MantineProvider></MemoryRouter>)
    await waitFor(() => expect(HTMLMediaElement.prototype.play).toHaveBeenCalled())
    view.unmount()
    const before = frames.mock.calls.length
    finishPlay()
    await new Promise(done => setTimeout(done, 10))
    expect(frames.mock.calls.length).toBe(before)
  })

  it('displays notice when camera is unavailable or fails to start', async () => {
    vi.mocked(navigator.mediaDevices.getUserMedia).mockRejectedValue(new Error('Permission denied'))

    render(
      <MemoryRouter>
        <MantineProvider>
          <QrScanPage onNavigate={vi.fn()} />
        </MantineProvider>
      </MemoryRouter>,
    )

    expect(await screen.findByText(/无法打开摄像头|网络环境/)).toBeInTheDocument()
  })
})