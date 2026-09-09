import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { api } from '../api/client'
import type { Message, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useSessionResources } from './useAstrorderData'

afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('opens with two records, pages only on demand, and reopens without replaying older pages', async () => {
  const session: Session = { id: 'native', agent_id: 'agent', title: 'Native', status: 'idle', workspace: null, updated_at: '2026-01-01T00:00:00Z' }
  const rows: Message[] = Array.from({ length: 30 }, (_, i) => ({ id: String(i), session_id: 'native', agent_id: 'agent', role: 'assistant', kind: 'message', text: String(i), created_at: new Date(1000 * i).toISOString(), attachments: [], command_id: null, tool: null }))
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().mergeMessages('agent', 'native', rows)
  const get = vi.spyOn(api, 'getMessages').mockImplementation(async (_sid, _aid, before) => before ? { items: rows.slice(8, 28), next_cursor: 'older-again' } : { items: rows.slice(-2), next_cursor: 'older' })
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  const hook = renderHook(({ selected }) => useSessionResources(selected, true), { wrapper, initialProps: { selected: session as Session | null } })
  await waitFor(() => expect(hook.result.current.messages.isSuccess).toBe(true))
  expect(get).toHaveBeenCalledWith('native', 'agent', undefined, 2)
  expect(hook.result.current.visibleMessages.map(m => m.id)).toEqual(['28', '29'])
  await act(async () => { await hook.result.current.messages.fetchNextPage() })
  expect(get).toHaveBeenLastCalledWith('native', 'agent', 'older', 20)
  await waitFor(() => expect(hook.result.current.visibleMessages).toHaveLength(22))
  hook.rerender({ selected: null })
  hook.rerender({ selected: session })
  await waitFor(() => expect(get).toHaveBeenCalledTimes(3))
  await waitFor(() => expect(hook.result.current.visibleMessages.map(m => m.id)).toEqual(['28', '29']))
  client.clear()
})
