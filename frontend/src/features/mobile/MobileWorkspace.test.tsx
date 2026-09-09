import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { selectMessages, useAstrorderStore } from '../../state/store'
import { MobileWorkspace } from './MobileWorkspace'
import type { OutboxEntry } from './mobileOutbox'
import { scopeKey } from '../../domain/semantics'

const memory = vi.hoisted(() => ({ rows: [] as OutboxEntry[] }))
vi.mock('./mobileOutboxStorage', () => ({ mobileOutboxStorage: { load: async () => memory.rows, save: async (rows: OutboxEntry[]) => { memory.rows = rows } } }))
vi.mock('../../hooks/useAstrorderData', () => ({ useSessionResources: () => ({ visibleMessages: selectMessages(useAstrorderStore.getState(), 'inert', 'native-test'), messages: { fetchNextPage: vi.fn(), hasNextPage: false }, commands: { isSuccess: true }, tasks: { isSuccess: true } }) }))
const session = { id: 'native-test', agent_id: 'inert', title: '隔离移动测试', workspace: null, status: 'idle' as const, updated_at: '2026-09-08T00:00:00Z' }
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })
function mount() {
  return render(<MantineProvider><QueryClientProvider client={new QueryClient()}><MemoryRouter initialEntries={['/mobile/chat/native-test?agent_id=inert']}><MobileWorkspace /></MemoryRouter></QueryClientProvider></MantineProvider>)
}
beforeEach(() => {
  memory.rows = []
  vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'bound-model', branch: 'feature/native' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: { kind: 'hermes', state: 'connected', available: true, version: null, agent_id: 'inert', profile_name: 'fixture', session_id: 'native-test', detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
  const values = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) })
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, cursor: 0, agents: [{ id: 'inert', kind: 'hermes', name: '协议测试', status: 'ready', capabilities: ['chat', 'stop', 'attachments'], limitation: null }], sessions: [session] })
})
describe('independent mobile composer', () => {
  it('shows native connection, agent, model and branch in runtime status', async () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '运行状态' }))
    const facts = screen.getByLabelText('运行信息')
    await waitFor(() => expect(facts).toHaveTextContent('feature/native'))
    expect(facts).toHaveTextContent('p/bound-model')
    expect(facts).toHaveTextContent('Hermes')
    expect(facts).toHaveTextContent('本机 · fixture')
    expect(facts).toHaveTextContent('已连接')
  })
  it('opens an anchored message menu, not a sheet, and quotes into the composer', () => {
    useAstrorderStore.getState().mergeMessages('inert', 'native-test', [{ id: 'm', agent_id: 'inert', session_id: 'native-test', role: 'assistant', kind: 'message', text: '引用这条消息', attachments: [], created_at: session.updated_at, command_id: null, tool: null }])
    const view = mount()
    fireEvent.contextMenu(view.container.querySelector('.m-msg')!, { clientX: 140, clientY: 220 })
    expect(screen.getByRole('menu', { name: '消息操作' })).toBeInTheDocument()
    expect(view.container.querySelector('.m-sheet')).toBeNull()
    fireEvent.click(screen.getByRole('menuitem', { name: '引用' }))
    expect(view.container.querySelector('.m-quote')).toHaveTextContent('引用这条消息')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
  it('reads and displays the native-bound model immediately when opening a session', async () => {
    mount()
    expect(await screen.findByText('p/bound-model')).toBeVisible()
    expect(api.getSessionModel).toHaveBeenCalledWith('native-test', 'inert')
  })
  it('confirms model selection inside the mobile sheet, without a native browser confirm', async () => {
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [{ provider: 'p', model: 'm', label: 'Provider · m' }] })
    const change = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ provider: 'p', model: 'm' })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount()
    fireEvent.click(screen.getByRole('button', { name: '选择会话模型' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Provider · m' }))
    fireEvent.click(screen.getByRole('button', { name: '确认切换模型' }))
    await waitFor(() => expect(change).toHaveBeenCalledWith('native-test', 'inert', 'p', 'm'))
    expect(confirm).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByRole('button', { name: '选择会话模型' })).toHaveTextContent('p/m'))
  })
  it('shows mobile pins above all projects, without a duplicate project row', () => {
    localStorage.setItem('astrorder_pinned_sessions', JSON.stringify({ [scopeKey('inert', session.id)]: true }))
    const view = mount()
    fireEvent.click(screen.getByRole('button', { name: '打开会话列表' }))
    const pinned = screen.getByRole('region', { name: '置顶会话' })
    expect(pinned).toHaveTextContent('隔离移动测试')
    const project = view.container.querySelector('.m-project-list > section:not(.m-pinned-sessions)')!
    expect(pinned.compareDocumentPosition(project) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(project.querySelectorAll('.m-session-row')).toHaveLength(0)
  })
  it('stores an offline file and draft before clearing without uploading', async () => {
    const upload = vi.spyOn(api, 'uploadAttachment').mockRejectedValue(new Error('must not upload offline'))
    const view = mount()
    fireEvent.change(view.container.querySelector('input[type=file]')!, { target: { files: [new File(['offline'], 'test.txt', { type: 'text/plain' })] } })
    fireEvent.change(screen.getByLabelText('消息内容'), { target: { value: 'offline message' } })
    fireEvent.click(screen.getByRole('button', { name: /^发送$/ }))
    await waitFor(() => expect(memory.rows).toHaveLength(1))
    expect(memory.rows[0].files[0].name).toBe('test.txt')
    expect(memory.rows[0].payload.text).toBe('offline message')
    expect(upload).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByLabelText('消息内容')).toHaveValue(''))
  })
})
