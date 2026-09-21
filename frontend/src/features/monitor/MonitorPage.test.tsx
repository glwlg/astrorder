import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { MonitorPage } from './MonitorPage'
import { draftStorage } from '../chat/draftStorage'

const queueMemory = vi.hoisted(() => ({ rows: [] as any[] }))
vi.mock('../mobile/mobileOutboxStorage', () => ({
  mobileOutboxStorage: {
    load: async () => queueMemory.rows,
    save: async (rows: any[]) => { queueMemory.rows = rows },
  },
}))

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return { ...actual, useNavigate: () => vi.fn() }
})
vi.mock('../../hooks/useAstrorderData', () => ({ useSessionResources: () => ({}) }))

const activeSession = { id: 'session-1', agent_id: 'agent-1', title: '运行中会话', workspace: '/repo', status: 'running' as const, updated_at: '2026-09-07T10:00:00Z' }
const idleSession = { id: 'session-2', agent_id: 'agent-1', title: '闲置会话', workspace: '/repo', status: 'idle' as const, updated_at: '2026-09-07T09:00:00Z' }
const reviewSession = { id: 'session-review', agent_id: 'agent-1', title: 'The following is the Codex agent history whose request action you are assessing', workspace: '/repo', status: 'idle' as const, updated_at: '2026-09-07T08:00:00Z' }
const storage = new Map<string, string>()
const savedDrafts = new Map<string, any>()

describe('MonitorPage', () => {
  beforeEach(() => {
    storage.clear()
    savedDrafts.clear()
    queueMemory.rows = []
    vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value), removeItem: (key: string) => storage.delete(key) })
    vi.spyOn(draftStorage, 'load').mockImplementation(async key => savedDrafts.get(key))
    vi.spyOn(draftStorage, 'save').mockImplementation(async (key, draft) => { savedDrafts.set(key, draft) })
    vi.spyOn(draftStorage, 'remove').mockImplementation(async key => { savedDrafts.delete(key) })
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.setState({
      agents: { 'agent-1': { id: 'agent-1', kind: 'hermes', name: '本机', status: 'ready', capabilities: ['chat'], limitation: null } },
      sessions: { 'agent-1::session-1': activeSession, 'agent-1::session-2': idleSession, 'agent-1::session-review': reviewSession },
      commands: {},
    })
  })
  afterEach(() => { cleanup(); vi.restoreAllMocks() })

  function renderPage() {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <MonitorPage />
        </QueryClientProvider>
      </MantineProvider>,
    )
  }

  it('defaults to active sessions and persists manually added idle sessions', async () => {
    renderPage()

    expect(screen.getByText('1 个会话')).toBeInTheDocument()
    expect(screen.getByText('运行中会话')).toBeInTheDocument()
    expect(screen.queryByTestId('monitor-card-agent-1-session-2')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '添加会话' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByText(reviewSession.title)).not.toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: /闲置会话/ }))

    expect(await screen.findByTestId('monitor-card-agent-1-session-2')).toBeInTheDocument()
    expect(screen.getByText('2 个会话')).toBeInTheDocument()
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-sessions') || '[]')).toEqual(['agent-1::session-2'])
  })

  it('keeps an automatically added session after completion until it is removed', async () => {
    renderPage()
    await waitFor(() => expect(JSON.parse(localStorage.getItem('astrorder:monitor-auto-sessions') || '[]')).toContain('agent-1::session-1'))

    useAstrorderStore.setState({
      sessions: { ...useAstrorderStore.getState().sessions, 'agent-1::session-1': { ...activeSession, status: 'idle' } },
    })

    expect(await screen.findByTestId('monitor-card-agent-1-session-1')).toHaveClass('is-completion-flash')
  })

  it('flashes when a command completes before the session status catches up', async () => {
    renderPage()
    const command = {
      id: 'command-1', agent_id: activeSession.agent_id, session_id: activeSession.id,
      action: 'send' as const, state: 'running' as const, text: '', attachments: [],
      created_at: activeSession.updated_at, error: null, target_id: null,
    }
    useAstrorderStore.getState().mergeCommands([command])
    await waitFor(() => expect(useAstrorderStore.getState().commands['agent-1::session-1::command-1']?.state).toBe('running'))

    useAstrorderStore.getState().mergeCommands([{ ...command, state: 'completed' }])

    expect(await screen.findByTestId('monitor-card-agent-1-session-1')).toHaveClass('is-completion-flash')
  })

  it('does not automatically re-add a manually removed running session', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('monitor-card-agent-1-session-1')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: '移出监控室' }))

    await waitFor(() => expect(screen.queryByTestId('monitor-card-agent-1-session-1')).not.toBeInTheDocument())
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-excluded-sessions') || '[]')).toContain('agent-1::session-1')
  })

  it('cleans only inactive automatically added sessions without messages for 30 minutes', async () => {
    storage.set('astrorder:monitor-auto-sessions', JSON.stringify(['agent-1::session-2']))
    renderPage()
    expect(await screen.findByTestId('monitor-card-agent-1-session-2')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '清理过时会话' }))

    await waitFor(() => expect(screen.queryByTestId('monitor-card-agent-1-session-2')).not.toBeInTheDocument())
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-auto-sessions') || '[]')).not.toContain('agent-1::session-2')
    expect(screen.getByTestId('monitor-card-agent-1-session-1')).toBeInTheDocument()
  })

  it('edits and sends a queued message from the monitor room', async () => {
    queueMemory.rows = [{
      payload: { id: 'queued-1', agent_id: 'agent-1', session_id: 'session-1', action: 'send', text: '旧内容', attachment_ids: [], target_id: null },
      files: [], attachments: [], state: 'queued',
    }]
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async payload => ({ ...payload, state: 'accepted', attachments: [], created_at: activeSession.updated_at, error: null }))
    renderPage()

    expect(await screen.findByText('旧内容')).toBeInTheDocument()
    const queuedItem = screen.getByText('旧内容').closest('.monitor-queue-item') as HTMLElement
    fireEvent.click(within(queuedItem).getByRole('button', { name: '编辑' }))
    const dialog = await screen.findByRole('dialog', { name: '编辑排队消息' })
    fireEvent.change(within(dialog).getByLabelText('排队消息内容'), { target: { value: '新内容' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(queueMemory.rows[0].payload.text).toBe('新内容'))

    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ id: 'queued-1', text: '新内容' })))
    await waitFor(() => expect(document.querySelector('.monitor-queue')).toBeNull())
  })

  it('deletes a queued message from the monitor room', async () => {
    queueMemory.rows = [{
      payload: { id: 'queued-2', agent_id: 'agent-1', session_id: 'session-1', action: 'send', text: '待删除', attachment_ids: [], target_id: null },
      files: [], attachments: [], state: 'queued',
    }]
    renderPage()

    expect(await screen.findByText('待删除')).toBeInTheDocument()
    const queuedItem = screen.getByText('待删除').closest('.monitor-queue-item') as HTMLElement
    fireEvent.click(within(queuedItem).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(screen.queryByText('待删除')).not.toBeInTheDocument())
    expect(queueMemory.rows).toHaveLength(0)
  })

  it('sends a message from a monitor card and clears the input after acceptance', async () => {
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: activeSession.updated_at, error: null }))
    renderPage()

    const input = screen.getByLabelText('发送到 运行中会话')
    fireEvent.change(input, { target: { value: '继续处理' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))

    await waitFor(() => expect(create).toHaveBeenCalledOnce())
    expect(create.mock.calls[0][0]).toMatchObject({ agent_id: 'agent-1', session_id: 'session-1', action: 'send', text: '继续处理' })
    await waitFor(() => expect(input).toHaveValue(''))
  })

  it('accepts pasted images and arbitrary files and uploads them with the message', async () => {
    const upload = vi.spyOn(api, 'uploadAttachment').mockImplementation(async file => ({ id: `attachment-${file.name}`, name: file.name, media_type: file.type, url: `/files/${file.name}` }))
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: activeSession.updated_at, error: null }))
    const { container } = renderPage()
    const input = screen.getByLabelText('发送到 运行中会话')
    const image = new File(['png'], 'shot.png', { type: 'image/png' })
    fireEvent.paste(input, { clipboardData: { files: [image], items: [{ kind: 'file', type: image.type, getAsFile: () => image }] } })
    expect(await screen.findByText('shot.png')).toBeInTheDocument()

    const picker = container.querySelector('.monitor-card-footer input[type="file"]') as HTMLInputElement
    expect(picker).not.toHaveAttribute('accept')
    const archive = new File(['zip'], 'logs.zip', { type: 'application/zip' })
    fireEvent.change(picker, { target: { files: [archive] } })
    expect(await screen.findByText('logs.zip')).toBeInTheDocument()
    fireEvent.change(input, { target: { value: '检查附件' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))

    await waitFor(() => expect(upload).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({
      text: '检查附件',
      attachment_ids: ['attachment-shot.png', 'attachment-logs.zip'],
    })))
    await waitFor(() => expect(input).toHaveValue(''))
    expect(screen.queryByText('shot.png')).not.toBeInTheDocument()
  })

  it('adds an idle session to monitor room when dragged and dropped into monitor page', async () => {
    const { container } = renderPage()
    const page = container.querySelector('.monitor-page')!

    fireEvent.dragOver(page, {
      dataTransfer: {
        types: ['application/x-astrorder-session'],
      },
    })
    fireEvent.drop(page, {
      dataTransfer: {
        types: ['application/x-astrorder-session'],
        getData: (type: string) =>
          type === 'application/x-astrorder-session'
            ? JSON.stringify({
                agent_id: idleSession.agent_id,
                id: idleSession.id,
                title: idleSession.title,
                key: `${idleSession.agent_id}::${idleSession.id}`,
              })
            : '',
      },
    })

    expect(await screen.findByTestId('monitor-card-agent-1-session-2')).toBeInTheDocument()
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-sessions') || '[]')).toContain('agent-1::session-2')
  })
})
