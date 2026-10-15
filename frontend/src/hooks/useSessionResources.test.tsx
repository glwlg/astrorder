import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { api } from '../api/client'
import type { Message, Session } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useSessionResources } from './useAstrorderData'

afterEach(() => { cleanup(); vi.restoreAllMocks() })

it.each(['codex', 'hermes', 'grok'])('preserves %s input through reply-first history refresh and session switching', async agent_id => {
  const session: Session = { id: 'reply-first', agent_id, title: 'Reply first', status: 'running', workspace: null, updated_at: '2026-01-01T00:00:00Z' }
  const input: Message = { id: 'optimistic-command', agent_id, session_id: session.id, role: 'user', kind: 'message', text: '请继续核对', attachments: [], tool: null, command_id: 'command', created_at: '2026-01-01T00:00:10Z' }
  const reply: Message = { ...input, id: 'reply', role: 'assistant', text: '正在核对', created_at: '2026-01-01T00:00:20Z' }
  const native: Message = { ...input, id: 'native-user', command_id: null, text: input.text + '\n\n[Image attached at: /images/upload.png]\n[screenshot]', created_at: '2026-01-01T00:00:11Z' }
  const store = useAstrorderStore.getState()
  store.resetRuntime()
  store.mergeMessages(agent_id, session.id, [input])
  vi.spyOn(api, 'syncSession').mockResolvedValue(session)
  const get = vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [reply], next_cursor: null })
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  const hook = renderHook(({ selected }) => useSessionResources(selected, true), { wrapper, initialProps: { selected: session as Session | null } })
  await waitFor(() => expect(hook.result.current.visibleMessages.map(row => row.id)).toEqual([input.id, reply.id]))
  hook.rerender({ selected: null })
  hook.rerender({ selected: session })
  expect(hook.result.current.visibleMessages.map(row => row.id)).toEqual([input.id, reply.id])
  get.mockResolvedValue({ items: [native, reply], next_cursor: null })
  await act(async () => { await hook.result.current.messages.refetch() })
  await waitFor(() => expect(hook.result.current.visibleMessages.map(row => row.id)).toEqual([native.id, reply.id]))
  const guidance = { ...input, id: 'optimistic-guidance', command_id: 'guidance', text: '补充引导', created_at: '2026-01-01T00:00:30Z' }
  act(() => useAstrorderStore.getState().mergeMessages(agent_id, session.id, [guidance]))
  await waitFor(() => expect(hook.result.current.visibleMessages.map(row => row.id)).toEqual([native.id, reply.id, guidance.id]))
  const nativeGuidance = { ...guidance, id: 'native-guidance', command_id: null, text: '补充引导\n原生附加说明', created_at: '2026-01-01T00:00:31Z' }
  get.mockResolvedValue({ items: [native, reply, nativeGuidance], next_cursor: null })
  await act(async () => { await hook.result.current.messages.refetch() })
  await waitFor(() => expect(hook.result.current.visibleMessages.map(row => row.id)).toEqual([native.id, reply.id, nativeGuidance.id]))
  client.clear()
})

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

it('keeps native page order across live completion, older pages and refetch with equal timestamps', async () => {
  const session: Session = { id: 'ordered', agent_id: 'codex', title: 'Ordered', status: 'idle', workspace: null, updated_at: '2026-10-15T01:00:00Z' }
  const row = (id: string, role: Message['role']): Message => ({ id, agent_id: session.agent_id, session_id: session.id, role, kind: role === 'tool' ? 'tool' : 'message', text: id, attachments: [], tool: role === 'tool' ? { name: 'commandExecution', status: 'completed' } : null, command_id: null, created_at: '1970-01-01T00:00:00Z' })
  const older = [row('user', 'user'), row('intro', 'assistant'), row('read', 'tool')]
  const latest = [row('update', 'assistant'), row('edit', 'tool'), row('answer', 'assistant')]
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().mergeMessages(session.agent_id, session.id, [latest[2], latest[0], latest[1]])
  vi.spyOn(api, 'syncSession').mockResolvedValue(session)
  vi.spyOn(api, 'getMessages').mockImplementation(async (_sid, _aid, before) => before ? { items: older, next_cursor: null } : { items: latest, next_cursor: 'older' })
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
  const hook = renderHook(() => useSessionResources({ ...session }, true), { wrapper })
  const expected = [...older, ...latest].map(m => m.id)
  await waitFor(() => expect(hook.result.current.visibleMessages.map(m => m.id)).toEqual(expected))
  act(() => useAstrorderStore.getState().applyEvent({ id: 'completed', cursor: 0, type: 'message.upsert', agent_id: session.agent_id, session_id: session.id, data: { ...older[2], created_at: '2026-10-15T01:01:00Z', text: 'completed output' } }))
  expect(hook.result.current.visibleMessages.map(m => m.id)).toEqual(expected)
  expect(hook.result.current.visibleMessages[2].text).toBe('completed output')
  hook.rerender()
  expect(hook.result.current.visibleMessages[2].text).toBe('completed output')
  await act(async () => { await hook.result.current.messages.refetch() })
  expect(hook.result.current.visibleMessages.map(m => m.id)).toEqual(expected)
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
