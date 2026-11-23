import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session, SessionSwarm } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { useAstrorderStore } from '../../state/store'
import { MobileWorkspace } from './MobileWorkspace'

const agyAgent: Agent = {
  id: 'local-antigravity',
  kind: 'antigravity',
  name: 'Antigravity',
  status: 'ready',
  capabilities: ['chat', 'stop', 'history', 'events'],
  limitation: null,
}

const coordSession: Session = {
  id: 'coord-m-1',
  agent_id: 'local-antigravity',
  title: '移动端 Swarm 会话',
  workspace: '/work/astrorder',
  project_name: 'astrorder',
  status: 'idle',
  updated_at: '2026-11-22T12:00:00Z',
}

const workerSession: Session = {
  id: 'worker-m-1',
  agent_id: 'local-antigravity',
  title: 'Backend Worker',
  workspace: '/work/astrorder',
  project_name: 'astrorder',
  status: 'running',
  updated_at: '2026-11-22T12:01:00Z',
}

const mobileSwarm: SessionSwarm = {
  supported: true,
  available: true,
  active: true,
  kind: 'swarm',
  title: 'Mobile Swarm Team',
  coordinator_session_id: 'coord-m-1',
  current_session_id: 'coord-m-1',
  is_coordinator: true,
  summary: { total: 2, working: 1, waiting: 1, stopped: 0, killed: 0 },
  members: [
    {
      name: 'coordinator',
      role: '协调者',
      is_coordinator: true,
      conversation_id: 'coord-m-1',
      agent_id: 'local-antigravity',
      in_catalog: true,
      model: 'claude-sonnet-4-6',
      state: 'waiting',
    },
    {
      name: 'backend',
      role: 'worker',
      is_coordinator: false,
      conversation_id: 'worker-m-1',
      parent_conversation_id: 'coord-m-1',
      agent_id: 'local-antigravity',
      in_catalog: true,
      model: 'gemini-3-pro',
      workspace_mode: 'share',
      state: 'working',
      brief: 'Fix mobile API',
      last_message: 'Running tests',
    },
  ],
  board: [
    {
      id: 'mb-1',
      kind: 'brief',
      author: 'coordinator',
      targets: ['backend'],
      text: 'Fix mobile API',
      created_at: '2026-11-22T12:00:10Z',
    },
  ],
}

describe('MobileWorkspace Antigravity Swarm sheet', () => {
  beforeEach(() => {
    useAstrorderStore.setState({
      agents: { [agyAgent.id]: agyAgent },
      sessions: {
        [scopeKey(coordSession.agent_id, coordSession.id)]: coordSession,
        [scopeKey(workerSession.agent_id, workerSession.id)]: workerSession,
      },
      messages: {},
      commands: {},
      tasks: {},
      approvals: {},
      projects: {},
      connection: 'connected',
    })
    vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ items: [], live: [], known_agent_ids: [] })
    vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
    vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ model: 'claude-sonnet-4-6', provider: 'antigravity', model_read_only: true })
    vi.spyOn(api, 'getSessionUsage').mockResolvedValue({
      ok: true,
      session_id: 'coord-m-1',
      model: 'claude-sonnet-4-6',
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      last_input_tokens: 4200,
      context_window: 200000,
      context_available: true,
      used_percentage: 2.1,
    } as Awaited<ReturnType<typeof api.getSessionUsage>>)
    vi.spyOn(api, 'getSessionSwarm').mockResolvedValue(mobileSwarm)
    vi.spyOn(api, 'getPreferences').mockResolvedValue({
      session_pins: {},
      project_order: [],
      pinned_projects: [],
      appearance: {},
      updated_at: '',
    } as never)
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('omits Swarm chip, agent logo, and server name from mobile header meta and opens Swarm from session menu', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={['/mobile/chat/coord-m-1?agent_id=local-antigravity']}>
            <MobileWorkspace />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(api.getSessionSwarm).toHaveBeenCalledWith('coord-m-1', 'local-antigravity')
    })

    const metaBar = container.querySelector('.m-card-meta')
    expect(metaBar).not.toBeNull()
    expect(metaBar?.querySelector('.m-swarm-chip')).toBeNull()
    expect(metaBar?.querySelector('.agent-kind-badge')).toBeNull()
    expect(metaBar?.querySelector('.m-server-name')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Swarm 团队' })).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: '会话操作' }))
    const swarmMenuItem = await screen.findByRole('menuitem', { name: 'Swarm 团队' })
    fireEvent.click(swarmMenuItem)

    const dialog = await screen.findByRole('dialog', { name: 'Swarm 团队' })
    expect(dialog).toBeInTheDocument()
    expect(screen.getByText('Mobile Swarm Team')).toBeInTheDocument()
    expect(screen.getByText('backend')).toBeInTheDocument()
  })
})
