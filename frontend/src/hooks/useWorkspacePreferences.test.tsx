import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import { resetUserIdleForTest } from './useUserIdle'
import { useWorkspacePreferences, type WorkspacePreferences } from './useWorkspacePreferences'

afterEach(() => { cleanup(); resetUserIdleForTest(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

it('imports legacy preferences, refreshes a second device after focus cooldown and preserves state on save failure', async () => {
  let now = 1_000_000
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  const legacy = { 'astrorder:project_appearance': JSON.stringify({ p: { icon: 'cloud', color: 'orange' } }), astrorder_pinned_sessions: JSON.stringify({ s: true }) }
  vi.stubGlobal('localStorage', { getItem: (key: string) => legacy[key as keyof typeof legacy] || null })
  let server: WorkspacePreferences = { appearance: { p: { icon: 'cloud', color: 'orange' } }, session_pins: { s: true }, pinned_projects: [], project_order: [] }
  const imported = vi.spyOn(api, 'importPreferences').mockImplementation(async () => server)
  const fetched = vi.spyOn(api, 'getPreferences').mockImplementation(async () => server)
  const save = vi.spyOn(api, 'updatePreferences').mockImplementation(async patch => {
    server = { ...server, session_pins: { ...server.session_pins, ...patch.session_pins } }
    return server
  })
  const desktop = renderHook(useWorkspacePreferences)
  await waitFor(() => expect(imported).toHaveBeenCalledWith(expect.objectContaining({ appearance: server.appearance, session_pins: { s: true } })))
  await act(async () => {})
  vi.stubGlobal('localStorage', { getItem: () => null })
  const phone = renderHook(useWorkspacePreferences)
  await waitFor(() => expect(phone.result.current.preferences.appearance.p.color).toBe('orange'))
  const fetchesBeforeFocus = fetched.mock.calls.length
  await act(async () => { await desktop.result.current.updatePreferences(value => ({ session_pins: { s: !value.session_pins.s } })) })
  // Within the 60s cooldown window, rapid focus events should not storm /preferences.
  act(() => window.dispatchEvent(new Event('focus')))
  await act(async () => {})
  expect(fetched).toHaveBeenCalledTimes(fetchesBeforeFocus)
  // After the 60s cooldown window, focus refreshes the second device.
  now += 61_000
  act(() => window.dispatchEvent(new Event('focus')))
  await waitFor(() => expect(phone.result.current.preferences.session_pins.s).toBe(false))
  const notify = vi.spyOn(notifications, 'show').mockReturnValue('error')
  save.mockRejectedValueOnce(new Error('offline'))
  await act(async () => { await phone.result.current.updatePreferences({ session_pins: { s: true } }) })
  expect(phone.result.current.preferences.session_pins.s).toBe(false)
  expect(notify).toHaveBeenCalledWith(expect.objectContaining({ message: '偏好设置同步失败：offline' }))
})

it('pauses periodic preference polling while user is idle overnight and catches up on return', async () => {
  vi.useFakeTimers()
  vi.stubGlobal('localStorage', { getItem: () => null })
  const server: WorkspacePreferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  const fetched = vi.spyOn(api, 'getPreferences').mockResolvedValue(server)
  renderHook(useWorkspacePreferences)
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(fetched).toHaveBeenCalledTimes(1)

  // During the first 15 minutes (4 ticks at 3m, 6m, 9m, 12m), polling continues normally.
  await act(async () => { await vi.advanceTimersByTimeAsync(14 * 60_000) })
  expect(fetched).toHaveBeenCalledTimes(5)

  // After 15 minutes of inactivity, overnight hours trigger zero preference polls.
  await act(async () => { await vi.advanceTimersByTimeAsync(6 * 60 * 60_000) })
  expect(fetched).toHaveBeenCalledTimes(5)

  // Returning wakes the poller and performs one stale catch-up sync.
  await act(async () => {
    window.dispatchEvent(new Event('keydown'))
    await vi.advanceTimersByTimeAsync(0)
  })
  expect(fetched).toHaveBeenCalledTimes(6)
})
