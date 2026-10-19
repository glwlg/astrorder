import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '../../api/client'
import { QrCodeLogin } from './QrCodeLogin'

describe('QrCodeLogin', () => {
  const validId = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
  const validSecret = 'fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210'

  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('creates QR session on mount, displays confirmation code, and polls', async () => {
    const createMock = vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '884812',
    })
    const pollMock = vi.spyOn(api, 'pollQrSession').mockResolvedValue({
      status: 'pending',
    })

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    expect(createMock).toHaveBeenCalledTimes(1)
    expect(screen.getByText(/884812/)).toBeInTheDocument()

    // Advance timer to trigger poll
    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    expect(pollMock).toHaveBeenCalledWith(validId)
  })

  it('cancels scheduled creation on quick unmount (protecting React StrictMode)', async () => {
    const createMock = vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '884812',
    })

    const view = render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    // Unmount before setTimeout(0) fires
    view.unmount()

    await act(async () => {
      vi.advanceTimersByTime(50)
      await Promise.resolve()
    })

    // createQrSession was cancelled by cleanup
    expect(createMock).not.toHaveBeenCalled()
  })

  it('calls onAuthenticated when poll returns approved', async () => {
    vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '123456',
    })
    const pollMock = vi.spyOn(api, 'pollQrSession').mockResolvedValue({
      status: 'approved',
      authenticated: true,
    })
    const onAuthenticated = vi.fn()

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={onAuthenticated} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    expect(pollMock).toHaveBeenCalledWith(validId)
    expect(onAuthenticated).toHaveBeenCalledTimes(1)
  })

  it('handles rejected status and stops polling', async () => {
    vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '567890',
    })
    const pollMock = vi.spyOn(api, 'pollQrSession').mockResolvedValue({
      status: 'rejected',
    })
    const onAuthenticated = vi.fn()

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={onAuthenticated} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    expect(pollMock).toHaveBeenCalledWith(validId)
    expect(onAuthenticated).not.toHaveBeenCalled()
    expect(screen.getByText(/取消|失效/)).toBeInTheDocument()
  })

  it('handles 410 HTTP error by setting expired state', async () => {
    vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '567890',
    })
    vi.spyOn(api, 'pollQrSession').mockRejectedValue(new ApiError(410, '二维码已过期'))

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    expect(screen.getByText(/二维码已过期/)).toBeInTheDocument()
  })

  it('handles local expiration when expires_at timestamp is reached', async () => {
    const expiresAt = new Date(Date.now() + 5000).toISOString()
    vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: expiresAt,
      confirmation_code: '567890',
    })
    vi.spyOn(api, 'pollQrSession').mockResolvedValue({ status: 'pending' })

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    // Advance 5500ms past expiration
    await act(async () => {
      vi.advanceTimersByTime(5500)
      await Promise.resolve()
    })

    expect(screen.getByText(/二维码已过期/)).toBeInTheDocument()
  })

  it('handles expired status and allows refreshing', async () => {
    const nextId = '1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
    const createMock = vi.spyOn(api, 'createQrSession')
      .mockResolvedValueOnce({
        id: validId,
        scan_secret: validSecret,
        expires_at: new Date(Date.now() + 180000).toISOString(),
        confirmation_code: '999999',
      })
      .mockResolvedValueOnce({
        id: nextId,
        scan_secret: validSecret,
        expires_at: new Date(Date.now() + 180000).toISOString(),
        confirmation_code: '111111',
      })
    vi.spyOn(api, 'pollQrSession').mockResolvedValue({
      status: 'expired',
    })

    render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    const refreshBtn = screen.getByRole('button', { name: /刷新/ })
    expect(refreshBtn).toBeInTheDocument()

    fireEvent.click(refreshBtn)
    await act(async () => {
      await Promise.resolve()
    })

    expect(createMock).toHaveBeenCalledTimes(2)
    expect(screen.getByText(/111111/)).toBeInTheDocument()
  })

  it('cleans up polling on unmount and prevents unmounted response from triggering login', async () => {
    let resolvePoll: (value: any) => void = () => {}
    const delayedPollPromise = new Promise((resolve) => {
      resolvePoll = resolve
    })

    vi.spyOn(api, 'createQrSession').mockResolvedValue({
      id: validId,
      scan_secret: validSecret,
      expires_at: new Date(Date.now() + 180000).toISOString(),
      confirmation_code: '432100',
    })
    vi.spyOn(api, 'pollQrSession').mockReturnValue(delayedPollPromise as any)
    const onAuthenticated = vi.fn()

    const view = render(
      <MantineProvider>
        <QrCodeLogin onAuthenticated={onAuthenticated} />
      </MantineProvider>,
    )

    await act(async () => {
      vi.advanceTimersByTime(0)
      await Promise.resolve()
    })

    // Advance timer to trigger poll call
    await act(async () => {
      vi.advanceTimersByTime(2000)
      await Promise.resolve()
    })

    // Unmount before poll resolves
    view.unmount()

    // Resolve poll after unmount
    await act(async () => {
      resolvePoll({ status: 'approved', authenticated: true })
      await Promise.resolve()
    })

    expect(onAuthenticated).not.toHaveBeenCalled()
  })
})