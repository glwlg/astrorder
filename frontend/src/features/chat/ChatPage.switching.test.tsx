import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { selectMessages, useAstrorderStore } from '../../state/store'
import { ChatPage } from './ChatPage'

function Switcher() {
  const navigate = useNavigate()
  return <>{[['codex', 'one'], ['codex', 'two'], ['hermes', 'one']].map(([agent, id]) => <button key={`${agent}/${id}`} onClick={() => navigate(`/chat/${id}?agent_id=${agent}`)}>{agent}/{id}</button>)}</>
}
afterEach(() => { cleanup(); vi.restoreAllMocks() })
it('replaces the transcript on in-app navigation, including the same native ID in another source', async () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 0, agents: ['codex', 'hermes'].map(id => ({ id, name: id, kind: id as 'codex' | 'hermes', status: 'ready', capabilities: ['chat'], limitation: null })), sessions: [['codex', 'one'], ['codex', 'two'], ['hermes', 'one']].map(([agent_id, id]) => ({ id, agent_id, title: `${agent_id}/${id}`, workspace: null, status: 'idle', updated_at: '2026-01-01T00:00:00Z' })) })
  vi.spyOn(api, 'getMessages').mockImplementation(async (session_id, agent_id) => ({ items: [{ id: 'native-item', session_id, agent_id, role: 'assistant', kind: 'message', text: `body:${agent_id}/${session_id}`, created_at: '2026-01-01T00:00:00Z', attachments: [], command_id: null, tool: null }], next_cursor: 'older' }))
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'model', provider: 'provider' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: { kind: 'hermes', state: 'offline', available: true, version: null, agent_id: null, session_id: null, detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<MantineProvider><QueryClientProvider client={client}><MemoryRouter initialEntries={['/chat/one?agent_id=codex']}><Switcher /><Routes><Route path="/chat/:sessionId" element={<ChatPage />} /></Routes></MemoryRouter></QueryClientProvider></MantineProvider>)
  expect(await screen.findByText('body:codex/one')).toBeInTheDocument()
  expect(view.container.querySelector('.desktop-details')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '打开会话详情' }))
  expect(await screen.findByRole('dialog', { name: '会话详情' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '固定详情侧栏' }))
  expect(view.container.querySelector('.desktop-details')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '收起详情侧栏' }))
  expect(view.container.querySelector('.desktop-details')).not.toBeInTheDocument()
  for (const target of ['codex/two', 'hermes/one', 'codex/one', 'codex/two', 'codex/one']) {
    fireEvent.click(screen.getByRole('button', { name: target }))
    await waitFor(() => expect(screen.getByRole('heading', { level: 2, name: target })).toBeInTheDocument())
    await waitFor(() => expect(view.container.querySelectorAll('.transcript')).toHaveLength(1))
    await waitFor(() => expect(screen.getByRole('log')).toHaveTextContent(`body:${target}`))
    expect(view.container.querySelectorAll('.history-button')).toHaveLength(1)
    for (const other of ['codex/one', 'codex/two', 'hermes/one'].filter(value => value !== target)) expect(screen.queryByText(`body:${other}`)).not.toBeInTheDocument()
  }
  expect(selectMessages(useAstrorderStore.getState(), 'codex', 'one')).toHaveLength(1)
  expect(selectMessages(useAstrorderStore.getState(), 'codex', 'two')).toHaveLength(1)
  expect(errors.mock.calls.some(args => args.join(' ').includes('same key'))).toBe(false)
  client.clear()
})
