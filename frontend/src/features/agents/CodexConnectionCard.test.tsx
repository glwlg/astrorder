import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api, type CodexConnectionStatus } from '../../api/client'
import { CodexConnectionCard } from './CodexConnectionCard'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('connects then reads back native status before showing connected', async () => {
  let state: CodexConnectionStatus = { kind: 'codex', state: 'disconnected', available: true, agent_id: 'local-codex', session_count: 0, auth_required: false, detail: 'ready to connect', daemon_mode: true }
  const get = vi.spyOn(api, 'getCodexConnection').mockImplementation(async () => state)
  const connect = vi.spyOn(api, 'connectCodex').mockImplementation(async () => { state = { ...state, state: 'connected', session_count: 3 }; return state })
  render(<MantineProvider><QueryClientProvider client={new QueryClient()}><CodexConnectionCard /></QueryClientProvider></MantineProvider>)
  fireEvent.click(await screen.findByRole('button', { name: '连接 Codex' }))
  await waitFor(() => expect(connect).toHaveBeenCalledOnce())
  await waitFor(() => expect(get.mock.calls.length).toBeGreaterThanOrEqual(2))
  expect(await screen.findByText('已连接')).toBeVisible()
  expect(screen.getByText('3 个原生会话')).toBeVisible()
  expect(screen.getByTestId('codex-daemon-mode')).toHaveTextContent('守护进程托管')
})
