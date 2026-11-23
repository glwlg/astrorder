import { MantineProvider, Menu } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NotificationPermissionControl, NotificationPermissionMenuItems } from './NotificationPermissionControl'

const mocks = vi.hoisted(() => ({ permission: 'default', request: vi.fn(), disable: vi.fn(), test: vi.fn(), show: vi.fn() }))
vi.mock('../domain/notifications', () => ({
  notificationPermission: () => mocks.permission,
  requestNotificationPermission: mocks.request,
  disableSystemNotifications: mocks.disable,
  testSystemNotification: mocks.test,
}))
vi.mock('@mantine/notifications', () => ({ notifications: { show: mocks.show } }))

beforeEach(() => { vi.clearAllMocks(); mocks.permission = 'default' })
afterEach(cleanup)
describe('notification permission control', () => {
  it('only subscribes after a user click and updates the control on success', async () => {
    mocks.request.mockImplementation(async () => { mocks.permission = 'granted'; return 'granted' })
    render(<MantineProvider><NotificationPermissionControl /></MantineProvider>)
    expect(mocks.request).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '启用后台通知' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '后台通知已启用' })).toBeInTheDocument())
  })
  it('reports a failed subscription without claiming success', async () => {
    mocks.request.mockRejectedValue(new Error('推送服务不可用'))
    render(<MantineProvider><NotificationPermissionControl /></MantineProvider>)
    fireEvent.click(screen.getByRole('button', { name: '启用后台通知' }))
    await waitFor(() => expect(mocks.show).toHaveBeenCalledWith({ color: 'red', message: '推送服务不可用' }))
    expect(screen.queryByRole('button', { name: '后台通知已启用' })).not.toBeInTheDocument()
  })
  it('explains denied permission without repeatedly requesting it', () => {
    mocks.permission = 'denied'
    render(<MantineProvider><NotificationPermissionControl /></MantineProvider>)
    fireEvent.click(screen.getByRole('button', { name: '后台通知已拒绝' }))
    expect(mocks.show).toHaveBeenCalledWith(expect.objectContaining({ color: 'orange' }))
    expect(mocks.request).not.toHaveBeenCalled()
  })
  it('enables notifications from mobile settings menu item', async () => {
    mocks.request.mockImplementation(async () => { mocks.permission = 'granted'; return 'granted' })
    render(
      <MantineProvider>
        <Menu>
          <Menu.Target><button type="button">应用设置</button></Menu.Target>
          <Menu.Dropdown><NotificationPermissionMenuItems /></Menu.Dropdown>
        </Menu>
      </MantineProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: '应用设置' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '启用后台通知' }))
    await waitFor(() => expect(mocks.request).toHaveBeenCalledTimes(1))
  })
  it('tests notifications from mobile settings menu item when granted', async () => {
    mocks.permission = 'granted'
    mocks.test.mockResolvedValue(undefined)
    render(
      <MantineProvider>
        <Menu>
          <Menu.Target><button type="button">应用设置</button></Menu.Target>
          <Menu.Dropdown><NotificationPermissionMenuItems /></Menu.Dropdown>
        </Menu>
      </MantineProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: '应用设置' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '测试系统通知' }))
    await waitFor(() => expect(mocks.test).toHaveBeenCalledTimes(1))
  })
  it('disables notifications from mobile settings menu item when granted', async () => {
    mocks.permission = 'granted'
    mocks.disable.mockImplementation(async () => { mocks.permission = 'default' })
    render(
      <MantineProvider>
        <Menu>
          <Menu.Target><button type="button">应用设置</button></Menu.Target>
          <Menu.Dropdown><NotificationPermissionMenuItems /></Menu.Dropdown>
        </Menu>
      </MantineProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: '应用设置' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '停用系统通知' }))
    await waitFor(() => expect(mocks.disable).toHaveBeenCalledTimes(1))
  })
})
