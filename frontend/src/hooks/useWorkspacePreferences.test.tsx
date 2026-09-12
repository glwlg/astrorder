import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import { useWorkspacePreferences, type WorkspacePreferences } from './useWorkspacePreferences'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('imports legacy preferences, refreshes a second device and preserves state on save failure', async () => {
  const legacy = { 'astrorder:project_appearance': JSON.stringify({ p: { icon: 'cloud', color: 'orange' } }), astrorder_pinned_sessions: JSON.stringify({ s: true }) }
  vi.stubGlobal('localStorage', { getItem: (key: string) => legacy[key as keyof typeof legacy] || null })
  let server: WorkspacePreferences = { appearance: { p: { icon: 'cloud', color: 'orange' } }, session_pins: { s: true }, pinned_projects: [], project_order: [] }
  const imported = vi.spyOn(api, 'importPreferences').mockImplementation(async () => server)
  vi.spyOn(api, 'getPreferences').mockImplementation(async () => server)
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
  await act(async () => { await desktop.result.current.updatePreferences(value => ({ session_pins: { s: !value.session_pins.s } })) })
  act(() => window.dispatchEvent(new Event('focus')))
  await waitFor(() => expect(phone.result.current.preferences.session_pins.s).toBe(false))
  const notify = vi.spyOn(notifications, 'show').mockReturnValue('error')
  save.mockRejectedValueOnce(new Error('offline'))
  await act(async () => { await phone.result.current.updatePreferences({ session_pins: { s: true } }) })
  expect(phone.result.current.preferences.session_pins.s).toBe(false)
  expect(notify).toHaveBeenCalledWith(expect.objectContaining({ message: '偏好设置同步失败：offline' }))
})
