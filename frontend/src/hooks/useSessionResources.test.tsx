import { cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { api } from '../api/client'
import type { Message, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useSessionResources } from './useAstrorderData'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('loads initial page with 50 records, preserves cache on session switch without clearing', async () => {
  const session: Session = { id: 'native', agent_id: 'agent', title: 'Native', status: 'idle', workspace: null, updated_at: '2026-01-01T00:00:00Z' }
  const rows: Message[] = Array.from({ length: 30 }, (_, i) => ({
    id: String(i),
    session_id: 'native',
    agent_id: 'agent',
    role: i === 0 ? 'user' : 'assistant',
    kind: 'message',
    text: String(i),
    created_at: new Date(1000 * i).toISOString(),
    attachments: [],
    command_id: null,
    tool: null,
  }))
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().mergeMessages('agent', 'native', rows)
  vi.spyOn(api, 'syncSession').mockResolvedValue(session)
  const get = vi.spyOn(api, 'getMessages').mockImplementation(async (_sid, _aid, before) =>
    before ? { items: rows.slice(0, 10), next_cursor: null } : { items: rows.slice(10), next_cursor: 'older' }
  )
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  const hook = renderHook(({ selected }) => useSessionResources(selected, true), { wrapper, initialProps: { selected: session as Session | null } })
  await waitFor(() => expect(hook.result.current.messages.isSuccess).toBe(true))
  expect(api.syncSession).toHaveBeenCalledWith('native', 'agent')
  expect(get).toHaveBeenCalledWith('native', 'agent', undefined, 50)
  // 因为第一页 (10-29) 全是 assistant，系统自动追溯上一页 (0-9) 直至包含 role==='user' 的消息，直接拉出全部 30 条
  await waitFor(() => expect(hook.result.current.visibleMessages).toHaveLength(30))

  // 切走会话并切回，验证保留内存缓存，已翻出的 30 条数据依然完整保留
  hook.rerender({ selected: null })
  hook.rerender({ selected: session })
  expect(hook.result.current.visibleMessages).toHaveLength(30)
  client.clear()
})

it('loads messages without waiting for session sync', async () => {
  const session: Session = { id: 'native', agent_id: 'agent', title: 'Native', status: 'idle', workspace: null, updated_at: '2026-01-01T00:00:00Z' }
  const rows: Message[] = [{
    id: '1',
    session_id: 'native',
    agent_id: 'agent',
    role: 'user',
    kind: 'message',
    text: 'hi',
    created_at: '2026-01-01T00:00:00Z',
    attachments: [],
    command_id: null,
    tool: null,
  }]
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(api, 'syncSession').mockImplementation(() => new Promise(() => {}))
  vi.spyOn(api, 'getMessages').mockResolvedValue({ items: rows, next_cursor: null })
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  const hook = renderHook(() => useSessionResources(session, true), { wrapper })
  await waitFor(() => expect(hook.result.current.messages.isSuccess).toBe(true))
  expect(hook.result.current.visibleMessages).toHaveLength(1)
  client.clear()
})
