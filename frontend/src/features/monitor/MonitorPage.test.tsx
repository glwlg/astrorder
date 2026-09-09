import { MantineProvider } from '@mantine/core'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MonitorPage } from './MonitorPage'
import { useAstrorderStore } from '../../state/store'

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return { ...actual, useNavigate: () => vi.fn() }
})

vi.mock('../../hooks/useAstrorderData', () => ({ useSessionResources: () => ({}) }))

describe('MonitorPage', () => {
  it('shows only real queued commands and keeps the session list unbounded', () => {
    useAstrorderStore.setState({
      agents: { 'agent-1': { id: 'agent-1', kind: 'hermes', name: '本机', status: 'ready', capabilities: [], limitation: null } },
      sessions: {
        'agent-1::session-1': { id: 'session-1', agent_id: 'agent-1', title: '运行中会话', workspace: '/repo', status: 'running', updated_at: '2026-09-07T10:00:00Z' },
        'agent-1::session-2': { id: 'session-2', agent_id: 'agent-1', title: '排队会话', workspace: '/repo', status: 'idle', updated_at: '2026-09-07T09:00:00Z' },
      },
      commands: {
        'agent-1::session-2::command-1': { id: 'command-1', agent_id: 'agent-1', session_id: 'session-2', action: 'enqueue', state: 'queued', text: '运行测试', attachments: [], created_at: '2026-09-07T10:01:00Z', error: null, target_id: null },
      },
    })

    render(<MantineProvider><MonitorPage /></MantineProvider>)

    expect(screen.getByText('2 个会话')).toBeInTheDocument()
    expect(screen.getByText('待机队列')).toBeInTheDocument()
    expect(screen.getByText('运行测试')).toBeInTheDocument()
  })
})
