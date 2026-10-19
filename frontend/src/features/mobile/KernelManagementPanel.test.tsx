import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type KernelStatusResponse } from '../../api/client'
import { KernelManagementPanel } from './KernelManagementPanel'

const mockStatus: KernelStatusResponse = {
  app: {
    state: 'running',
    healthy: true,
    process: { pid: 30001, creation_time: '2026-10-17T08:00:00Z' },
  },
  daemon: {
    state: 'running',
    active_sessions: { 'session-1': {}, 'session-2': {} },
    process: { pid: 30009, creation_time: '2026-10-17T08:00:01Z' },
  },
  update: {
    update_available: false,
  },
}

describe('KernelManagementPanel', () => {
  beforeEach(() => {
    sessionStorage.clear()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    cleanup()
    sessionStorage.clear()
    vi.restoreAllMocks()
  })

  it('fetches and displays kernel statuses on mount and supports refresh', async () => {
    const getStatusSpy = vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    expect(await screen.findByText('大内核')).toBeInTheDocument()
    expect(screen.getByText('小内核')).toBeInTheDocument()
    expect(screen.queryByText(/App Server/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Session Daemon/)).not.toBeInTheDocument()
    expect(screen.getByText(/30001/)).toBeInTheDocument()
    expect(screen.getByText(/30009/)).toBeInTheDocument()
    expect(screen.getByText(/活动会话：2/)).toBeInTheDocument()
    expect(screen.getByText('无可用更新')).toBeInTheDocument()

    const refreshBtn = screen.getByRole('button', { name: '刷新' })
    fireEvent.click(refreshBtn)
    await waitFor(() => expect(getStatusSpy).toHaveBeenCalledTimes(2))
  })

  it('disables update button when update_available is false and enables when true', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      update: { update_available: false },
    })

    const { rerender } = render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const updateBtn = await screen.findByRole('button', { name: '更新并重启小内核' })
    expect(updateBtn).toBeDisabled()

    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      update: { update_available: true },
    })

    rerender(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const refreshBtn = screen.getByRole('button', { name: '刷新' })
    fireEvent.click(refreshBtn)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '更新并重启小内核' })).not.toBeDisabled()
    })
  })

  it('does not send request when operation is cancelled in ConfirmPopover', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    const createOpSpy = vi.spyOn(api, 'createKernelOperation')

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const dialog = await screen.findByRole('dialog', { name: '重启小内核？' })
    expect(dialog).toBeInTheDocument()

    const cancelBtn = screen.getByRole('button', { name: '取消' })
    fireEvent.click(cancelBtn)

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '重启小内核？' })).not.toBeInTheDocument()
    })
    expect(createOpSpy).not.toHaveBeenCalled()
  })

  it('sends correct operation payload with confirm_active: true on confirm', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    const createOpSpy = vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-daemon-restart',
      status: 'queued',
      target: 'daemon',
      action: 'restart',
    })
    vi.spyOn(api, 'getKernelOperation').mockResolvedValue({
      id: 'op-daemon-restart',
      status: 'success',
      target: 'daemon',
      action: 'restart',
    })

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(createOpSpy).toHaveBeenCalledWith({
        target: 'daemon',
        action: 'restart',
        confirm_active: true,
      })
    })
  })

  it('warns about active sessions when daemon has active sessions', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      daemon: {
        state: 'running',
        active_sessions: { 'sess-1': {}, 'sess-2': {} },
      },
    })

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const dialog = await screen.findByRole('dialog', { name: '重启小内核？' })
    expect(dialog).toHaveTextContent(/中断/)
    expect(dialog).toHaveTextContent(/2 个活动会话/)
  })

  it('warns about unknown session state when daemon state is unknown or active_sessions is missing', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      daemon: {
        state: 'unknown',
      },
    })

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const dialog = await screen.findByRole('dialog', { name: '重启小内核？' })
    expect(dialog).toHaveTextContent(/无法确认/)
    expect(dialog).toHaveTextContent(/中断/)
  })

  it('warns about temporary disconnect while daemon continues running for app restart', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartAppBtn = await screen.findByRole('button', { name: '重启大内核' })
    fireEvent.click(restartAppBtn)

    const dialog = await screen.findByRole('dialog', { name: '重启大内核？' })
    expect(dialog).toHaveTextContent(/短暂断开连接/)
    expect(dialog).toHaveTextContent(/小内核.*继续运行/)
  })

  it('disables operation buttons while an operation is in progress to prevent duplicate submission', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      update: { update_available: true },
    })
    vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-inflight',
      status: 'queued',
      target: 'daemon',
      action: 'restart',
    })
    vi.spyOn(api, 'getKernelOperation').mockReturnValue(new Promise(() => {}))

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重启小内核' })).toBeDisabled()
      expect(screen.getByRole('button', { name: '更新并重启小内核' })).toBeDisabled()
      expect(screen.getByRole('button', { name: '重启大内核' })).toBeDisabled()
    })
  })

  it('polls operation and shows inline error on failure without reporting success', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-failed',
      status: 'queued',
      target: 'daemon',
      action: 'restart',
    })
    vi.spyOn(api, 'getKernelOperation').mockResolvedValue({
      id: 'op-failed',
      status: 'failed',
      target: 'daemon',
      action: 'restart',
      error: 'Daemon process failed to start',
      detail: '守护进程启动超时',
    })

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    expect(await screen.findByRole('alert')).toHaveTextContent('守护进程启动超时')
    expect(screen.queryByText(/操作成功/)).not.toBeInTheDocument()
  })

  it('tolerates temporary network disconnections and succeeds upon recovery', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-reconnect',
      status: 'queued',
      target: 'app',
      action: 'restart',
    })

    let pollCount = 0
    vi.spyOn(api, 'getKernelOperation').mockImplementation(async () => {
      pollCount++
      if (pollCount === 1) {
        throw new TypeError('Failed to fetch')
      }
      return {
        id: 'op-reconnect',
        status: 'success',
        target: 'app',
        action: 'restart',
      }
    })

    render(
      <MantineProvider>
        <KernelManagementPanel pollIntervalMs={50} />
      </MantineProvider>,
    )

    const restartAppBtn = await screen.findByRole('button', { name: '重启大内核' })
    fireEvent.click(restartAppBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    expect(await screen.findByText(/操作已成功完成/)).toBeInTheDocument()
    expect(pollCount).toBeGreaterThanOrEqual(2)
  })

  it('shows honest unconfirmed inline error on timeout and does not auto-resend', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    const createOpSpy = vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-timeout',
      status: 'queued',
      target: 'app',
      action: 'restart',
    })
    vi.spyOn(api, 'getKernelOperation').mockImplementation(async () => {
      throw new TypeError('Failed to fetch')
    })

    render(
      <MantineProvider>
        <KernelManagementPanel pollIntervalMs={20} timeoutMs={60} />
      </MantineProvider>,
    )

    const restartAppBtn = await screen.findByRole('button', { name: '重启大内核' })
    fireEvent.click(restartAppBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    expect(await screen.findByRole('alert')).toHaveTextContent(/未确认/)
    expect(createOpSpy).toHaveBeenCalledTimes(1)
  })

  it('restores active operation from sessionStorage on mount and continues polling', async () => {
    sessionStorage.setItem(
      'astrorder:kernel-active-operation',
      JSON.stringify({
        id: 'op-stored',
        target: 'daemon',
        action: 'restart',
        startTime: Date.now(),
      }),
    )

    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    const getOpSpy = vi.spyOn(api, 'getKernelOperation').mockResolvedValue({
      id: 'op-stored',
      status: 'success',
      target: 'daemon',
      action: 'restart',
    })

    render(
      <MantineProvider>
        <KernelManagementPanel pollIntervalMs={50} />
      </MantineProvider>,
    )

    expect(await screen.findByText(/操作已成功完成/)).toBeInTheDocument()
    expect(getOpSpy).toHaveBeenCalledWith('op-stored')
    expect(sessionStorage.getItem('astrorder:kernel-active-operation')).toBeNull()
  })

  it('displays "未确认" and disables update button when status is null or fetch fails', async () => {
    vi.spyOn(api, 'getKernelStatus').mockRejectedValue(new Error('Network error'))

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    expect(await screen.findByText('未确认')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '更新并重启小内核' })).toBeDisabled()
  })

  it('formats Windows FILETIME into readable time and avoids raw integer label', async () => {
    // Windows FILETIME uint64 (e.g. 133735680000000000 = ~2024-10-17 UTC)
    const filetime = 133735680000000000
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue({
      ...mockStatus,
      app: {
        state: 'running',
        healthy: true,
        process: { pid: 45678, creation_time: filetime },
      },
    })

    render(
      <MantineProvider>
        <KernelManagementPanel />
      </MantineProvider>,
    )

    expect(await screen.findByText(/45678/)).toBeInTheDocument()
    expect(screen.queryByText(/133735680000000000/)).not.toBeInTheDocument()
    expect(screen.getByText(/启动时间：/)).toBeInTheDocument()
  })

  it('continues polling when sessionStorage throws and does not report submission failure', async () => {
    vi.spyOn(api, 'getKernelStatus').mockResolvedValue(mockStatus)
    vi.spyOn(api, 'createKernelOperation').mockResolvedValue({
      id: 'op-nostorage',
      status: 'queued',
      target: 'daemon',
      action: 'restart',
    })
    vi.spyOn(api, 'getKernelOperation').mockResolvedValue({
      id: 'op-nostorage',
      status: 'success',
      target: 'daemon',
      action: 'restart',
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError')
    })

    render(
      <MantineProvider>
        <KernelManagementPanel pollIntervalMs={50} />
      </MantineProvider>,
    )

    const restartDaemonBtn = await screen.findByRole('button', { name: '重启小内核' })
    fireEvent.click(restartDaemonBtn)

    const confirmBtn = await screen.findByRole('button', { name: '确认重启' })
    fireEvent.click(confirmBtn)

    expect(await screen.findByText(/操作已成功完成/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
