import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NotificationPermissionControl } from './NotificationPermissionControl'

afterEach(() => vi.unstubAllGlobals())

describe('notification permission control', () => {
  it('does not request permission until the user clicks enable', async () => {
    const requestPermission = vi.fn().mockResolvedValue('granted')
    vi.stubGlobal('Notification', { permission: 'default', requestPermission })
    render(<MantineProvider><NotificationPermissionControl /></MantineProvider>)

    expect(requestPermission).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '启用后台通知' }))
    await waitFor(() => expect(requestPermission).toHaveBeenCalledOnce())
    expect(screen.getByRole('button', { name: '后台通知已启用' })).toBeDisabled()
  })

  it('explains denied permission without retrying or requesting again', () => {
    const requestPermission = vi.fn()
    vi.stubGlobal('Notification', { permission: 'denied', requestPermission })
    render(<MantineProvider><NotificationPermissionControl /></MantineProvider>)

    expect(screen.getByRole('button', { name: '后台通知已拒绝' })).toBeDisabled()
    expect(requestPermission).not.toHaveBeenCalled()
  })
})
