import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { MonitorPage } from './MonitorPage'

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return { ...actual, useNavigate: () => vi.fn() }
})
vi.mock('../../hooks/useAstrorderData', () => ({ useSessionResources: () => ({}) }))

const activeSession = { id: 'session-1', agent_id: 'agent-1', title: '运行中会话', workspace: '/repo', status: 'running' as const, updated_at: '2026-09-07T10:00:00Z' }
const idleSession = { id: 'session-2', agent_id: 'agent-1', title: '闲置会话', workspace: '/repo', status: 'idle' as const, updated_at: '2026-09-07T09:00:00Z' }
const storage = new Map<string, string>()

describe('MonitorPage', () => {
  beforeEach(() => {
    storage.clear()
    vi.stubGlobal('localStorage', { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) })
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.setState({
      agents: { 'agent-1': { id: 'agent-1', kind: 'hermes', name: '本机', status: 'ready', capabilities: ['chat'], limitation: null } },
      sessions: { 'agent-1::session-1': activeSession, 'agent-1::session-2': idleSession },
      commands: {},
    })
  })
  afterEach(() => { cleanup(); vi.restoreAllMocks() })

  it('defaults to active sessions and persists manually added idle sessions', async () => {
    render(<MantineProvider><MonitorPage /></MantineProvider>)

    expect(screen.getByText('1 个会话')).toBeInTheDocument()
    expect(screen.getByText('运行中会话')).toBeInTheDocument()
    expect(screen.queryByTestId('monitor-card-agent-1-session-2')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '添加会话' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: /闲置会话/ }))

    expect(await screen.findByTestId('monitor-card-agent-1-session-2')).toBeInTheDocument()
    expect(screen.getByText('2 个会话')).toBeInTheDocument()
    expect(JSON.parse(localStorage.getItem('astrorder:monitor-sessions') || '[]')).toEqual(['agent-1::session-2'])
  })

  it('sends a message from a monitor card and clears the input after acceptance', async () => {
    const create = vi.spyOn(api, 'createCommand').mockImplementation(async input => ({ ...input, state: 'accepted', attachments: [], created_at: activeSession.updated_at, error: null }))
    render(<MantineProvider><MonitorPage /></MantineProvider>)

    const input = screen.getByLabelText('发送到 运行中会话')
    fireEvent.change(input, { target: { value: '继续处理' } })
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }))

    await waitFor(() => expect(create).toHaveBeenCalledOnce())
    expect(create.mock.calls[0][0]).toMatchObject({ agent_id: 'agent-1', session_id: 'session-1', action: 'send', text: '继续处理' })
    await waitFor(() => expect(input).toHaveValue(''))
  })
})
