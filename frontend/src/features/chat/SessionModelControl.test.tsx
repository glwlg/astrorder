import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SessionModelControl } from './SessionModelControl'
const session = { id: 'native', agent_id: 'source', title: 'Test', status: 'idle' as const, workspace: null, updated_at: '2026-09-08T00:00:00Z' }
afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('desktop shows the bound native model and switches the exact session', async () => {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'bound', provider: 'p' })
  vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [{ model: 'next', provider: 'p', label: 'Provider · next' }] })
  const change = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ model: 'next', provider: 'p' })
  render(<MantineProvider><QueryClientProvider client={new QueryClient()}><SessionModelControl session={session} /></QueryClientProvider></MantineProvider>)
  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/bound'))
  fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Provider · next' }))
  fireEvent.click(screen.getByRole('button', { name: '确认切换模型' }))
  await waitFor(() => expect(change).toHaveBeenCalledWith('native', 'source', 'p', 'next'))
  await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/next'))
})
