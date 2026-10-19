import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { QrAuthorizePage } from './QrAuthorizePage'

describe('QrAuthorizePage', () => {
  const validId = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
  const validSecret = 'fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210'

  beforeEach(() => {
    window.location.hash = `#id=${validId}&scan_secret=${validSecret}`
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    window.location.hash = ''
  })

  it('requires user to login first when unauthenticated, without auto-authorizing', async () => {
    const inspectMock = vi.spyOn(api, 'inspectQrSession')
    const approveMock = vi.spyOn(api, 'approveQrSession')

    render(
      <MantineProvider>
        <QrAuthorizePage authenticated={false} onAuthenticated={vi.fn()} />
      </MantineProvider>,
    )

    expect(await screen.findByText(/先登录/)).toBeInTheDocument()
    expect(inspectMock).not.toHaveBeenCalled()
    expect(approveMock).not.toHaveBeenCalled()
  })

  it('removes scan_secret from browser history fragment on mount but preserves state', async () => {
    const replaceStateSpy = vi.spyOn(window.history, 'replaceState')
    vi.spyOn(api, 'inspectQrSession').mockResolvedValue({
      status: 'pending',
      expires_at: new Date(Date.now() + 60000).toISOString(),
      confirmation_code: '772211',
      device: 'MacBook Pro',
    })

    render(
      <MantineProvider>
        <QrAuthorizePage authenticated={true} />
      </MantineProvider>,
    )

    expect(replaceStateSpy).toHaveBeenCalled()
    expect(await screen.findByText(/772211/)).toBeInTheDocument()
    expect(screen.getByText(/MacBook Pro/)).toBeInTheDocument()
  })

  it('submits approve decision when user clicks confirm button', async () => {
    vi.spyOn(api, 'inspectQrSession').mockResolvedValue({
      status: 'pending',
      expires_at: new Date(Date.now() + 60000).toISOString(),
      confirmation_code: '901234',
      device: 'Chrome on Windows',
    })
    const approveMock = vi.spyOn(api, 'approveQrSession').mockResolvedValue({
      status: 'approved',
    })

    render(
      <MantineProvider>
        <QrAuthorizePage authenticated={true} />
      </MantineProvider>,
    )

    const confirmBtn = await screen.findByRole('button', { name: /确认授权/ })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(approveMock).toHaveBeenCalledWith(validId, validSecret, 'approve')
    })
    expect(await screen.findByText(/已授权/)).toBeInTheDocument()
  })

  it('submits reject decision when user clicks cancel button', async () => {
    vi.spyOn(api, 'inspectQrSession').mockResolvedValue({
      status: 'pending',
      expires_at: new Date(Date.now() + 60000).toISOString(),
      confirmation_code: '345678',
    })
    const approveMock = vi.spyOn(api, 'approveQrSession').mockResolvedValue({
      status: 'rejected',
    })

    render(
      <MantineProvider>
        <QrAuthorizePage authenticated={true} />
      </MantineProvider>,
    )

    const cancelBtn = await screen.findByRole('button', { name: /取消|拒绝/ })
    fireEvent.click(cancelBtn)

    await waitFor(() => {
      expect(approveMock).toHaveBeenCalledWith(validId, validSecret, 'reject')
    })
    expect(await screen.findByText('已取消授权')).toBeInTheDocument()
  })

  it('handles race boundary when approve returns 200 expired status', async () => {
    vi.spyOn(api, 'inspectQrSession').mockResolvedValue({
      status: 'pending',
      expires_at: new Date(Date.now() + 60000).toISOString(),
      confirmation_code: '112233',
    })
    const approveMock = vi.spyOn(api, 'approveQrSession').mockResolvedValue({
      status: 'expired',
    })

    render(
      <MantineProvider>
        <QrAuthorizePage authenticated={true} />
      </MantineProvider>,
    )

    const confirmBtn = await screen.findByRole('button', { name: /确认授权/ })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(approveMock).toHaveBeenCalledWith(validId, validSecret, 'approve')
    })
    expect(await screen.findByText(/已过期.*重/)).toBeInTheDocument()
    expect(screen.queryByText('已授权电脑端登录')).not.toBeInTheDocument()
  })
})