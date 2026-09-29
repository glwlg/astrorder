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
  vi.spyOn(api, 'getMessages').mockImplementation(async (session_id, agent_id) => ({
    items: [
      { id: 'native-user', session_id, agent_id, role: 'user', kind: 'message', text: `ask:${agent_id}/${session_id}`, created_at: '2026-01-01T00:00:00Z', attachments: [], command_id: null, tool: null },
      { id: 'native-item', session_id, agent_id, role: 'assistant', kind: 'message', text: `body:${agent_id}/${session_id}`, created_at: '2026-01-01T00:00:01Z', attachments: [], command_id: null, tool: null },
    ],
    next_cursor: 'older',
  }))
  vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'model', provider: 'provider' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: { kind: 'hermes', state: 'offline', available: true, version: null, agent_id: null, session_id: null, detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<MantineProvider><QueryClientProvider client={client}><MemoryRouter initialEntries={['/chat/one?agent_id=codex']}><Switcher /><Routes><Route path="/chat/:sessionId" element={<ChatPage />} /></Routes></MemoryRouter></QueryClientProvider></MantineProvider>)
  expect(await screen.findByText('body:codex/one')).toBeInTheDocument()
  expect(view.container.querySelector('.desktop-details')).toHaveStyle({ display: 'none' })
  fireEvent.click(screen.getByRole('button', { name: '打开会话详情' }))
  expect(await screen.findByRole('dialog', { name: '会话详情' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '固定详情侧栏' }))
  expect(view.container.querySelector('.desktop-details')).toHaveStyle({ display: 'block' })
  fireEvent.click(screen.getByRole('button', { name: '收起详情侧栏' }))
  expect(view.container.querySelector('.desktop-details')).toHaveStyle({ display: 'none' })
  for (const target of ['codex/two', 'hermes/one', 'codex/one', 'codex/two', 'codex/one']) {
    fireEvent.click(screen.getByRole('button', { name: target }))
    expect(view.container.querySelector('.chat-page-switching')).toBeInTheDocument()
    expect(view.container.querySelector('.transcript-message-entry')).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('heading', { level: 2, name: target })).toBeInTheDocument())
    await waitFor(() => expect(view.container.querySelectorAll('.transcript')).toHaveLength(1))
    await waitFor(() => expect(screen.getByRole('log')).toHaveTextContent(`body:${target}`))
    expect(view.container.querySelectorAll('.history-button')).toHaveLength(1)
    for (const other of ['codex/one', 'codex/two', 'hermes/one'].filter(value => value !== target)) expect(screen.queryByText(`body:${other}`)).not.toBeInTheDocument()
  }
  expect(selectMessages(useAstrorderStore.getState(), 'codex', 'one')).toHaveLength(2)
  expect(selectMessages(useAstrorderStore.getState(), 'codex', 'two')).toHaveLength(2)
  expect(errors.mock.calls.some(args => args.join(' ').includes('same key'))).toBe(false)
  client.clear()
})

it.each([
  ['failed', 'Codex 审查超过 10 分钟未完成。'],
  ['empty', null],
] as const)('recovers a completed %s review into the developer draft after returning to the page', async (status, error) => {
  useAstrorderStore.getState().resetRuntime()
  const agent_id = 'codex'
  const source_session_id = 'dev'
  useAstrorderStore.getState().setDraft(agent_id, source_session_id, { text: '', attachments: [], sessionRefs: [] })
  const review_session_id = 'review'
  const date = '2026-09-29T03:32:28Z'
  const comment = '::code-comment{title="[P1] 修复问题" body="补充校验。" file="src/a.ts" start=12 priority=1}'
  useAstrorderStore.getState().hydrateBootstrap({
    protocol_version: 1,
    cursor: 0,
    agents: [{ id: agent_id, name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
    sessions: [source_session_id, review_session_id].map(id => ({ id, agent_id, title: id, workspace: '/repo', status: 'idle', updated_at: date })),
  })
  const getRuns = vi.spyOn(api, 'getReviewRelayRuns').mockResolvedValue({ items: [{
    id: 'run-1', source_agent_id: agent_id, source_session_id, review_agent_id: agent_id,
    review_session_id, command_id: 'command-1', baseline_ids: ['old-message'],
    status, comment_text: null, error, created_at: date, updated_at: date,
  }] })
  let reviewReads = 0
  vi.spyOn(api, 'getMessages').mockImplementation(async id => ({
    items: id === review_session_id && (++reviewReads > (status === 'empty' ? 1 : 0)) ? [{
      id: 'new-message', session_id: review_session_id, agent_id, role: 'assistant', kind: 'message',
      text: comment, created_at: date, attachments: [], command_id: 'command-1', tool: null,
    }] : [], next_cursor: null,
  }))
  vi.spyOn(api, 'getCommands').mockImplementation(async id => ({ items: id === review_session_id ? [{
    id: 'command-1', session_id: review_session_id, agent_id, action: 'send', state: 'completed',
    text: '请检查我未提交的更改', attachments: [], created_at: date, error: null, target_id: null,
  }] : [] }))
  vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'model', provider: 'provider' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: { kind: 'hermes', state: 'offline', available: true, version: null, agent_id: null, session_id: null, detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
  const update = vi.spyOn(api, 'updateReviewRelayRun').mockImplementation(async (_, payload) => ({
    id: 'run-1', source_agent_id: agent_id, source_session_id, review_agent_id: agent_id,
    review_session_id, command_id: 'command-1', baseline_ids: ['old-message'],
    status: payload.status, comment_text: payload.comment_text || null, error: null, created_at: date, updated_at: date,
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MantineProvider><QueryClientProvider client={client}><MemoryRouter initialEntries={['/chat/dev?agent_id=codex']}><Routes><Route path="/chat/:sessionId" element={<ChatPage />} /></Routes></MemoryRouter></QueryClientProvider></MantineProvider>)
  await waitFor(() => expect(useAstrorderStore.getState().drafts['codex::dev']?.text).toBe(comment), { timeout: 5000 })
  expect(getRuns).toHaveBeenCalledWith(agent_id, source_session_id)
  expect(update).toHaveBeenCalledWith('run-1', { status: 'draft_ready', comment_text: comment })
  expect(update).not.toHaveBeenCalledWith('run-1', { status: 'empty' })
  client.clear()
})