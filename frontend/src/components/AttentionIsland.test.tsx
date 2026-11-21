import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import { useAstrorderStore } from '../state/store'
import { AttentionIsland } from './AttentionIsland'

function LocationEcho() {
  const location = useLocation()
  return <output data-testid="island-loc">{`${location.pathname}${location.search}`}</output>
}

function renderIsland(initialEntry = '/chat/active-session?agent_id=codex', activeSessionKey = 'codex::active-session') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <AttentionIsland activeSessionKey={activeSessionKey} />
          <LocationEcho />
        </MemoryRouter>
      </QueryClientProvider>
    </MantineProvider>,
  )
}

describe('AttentionIsland', () => {
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    useAstrorderStore.getState().resetRuntime()
  })

  it('renders nothing when there are no pending approvals or background activity', () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 0,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat', 'approvals'], limitation: null }],
      sessions: [{ id: 'active-session', agent_id: 'codex', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-21T08:00:00Z' }],
    })
    const { container } = renderIsland()
    expect(container.querySelector('.attention-island')).toBeNull()
  })

  it('surfaces a pending approval from another session and allows in-place approval', async () => {
    const createCommand = vi.spyOn(api, 'createCommand').mockImplementation(async (payload) => ({
      ...payload,
      state: 'accepted',
      attachments: [],
      created_at: '2026-11-21T08:05:00Z',
      error: null,
    }))
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat', 'approvals'], limitation: null }],
      sessions: [
        { id: 'active-session', agent_id: 'codex', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-21T08:00:00Z' },
        { id: 'other-session', agent_id: 'codex', title: 'OpsCore 发布', workspace: '/opscore', status: 'waiting_approval', updated_at: '2026-11-21T08:05:00Z' },
      ],
      approvals: [
        {
          id: 'appr-1',
          agent_id: 'codex',
          session_id: 'other-session',
          title: '执行 rm -rf dist',
          detail: 'rm -rf dist',
          state: 'pending',
          target_id: 'call-1',
          data: {},
        },
      ],
    })

    renderIsland()
    expect(screen.getByText('OpsCore 发布')).toBeInTheDocument()
    expect(screen.getByText('执行 rm -rf dist')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '批准' }))
    await waitFor(() =>
      expect(createCommand).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: 'codex',
          session_id: 'other-session',
          action: 'approve',
          target_id: 'call-1',
        }),
      ),
    )
    // Stays on current session without forcing route switch
    expect(screen.getByTestId('island-loc')).toHaveTextContent('/chat/active-session?agent_id=codex')
  })

  it('shows running background sessions pulse pill, opens tray, and lets users jump or dismiss', async () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 2,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { id: 'active-session', agent_id: 'codex', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-21T08:00:00Z' },
        { id: 'bg-1', agent_id: 'codex', title: '后台编译任务', workspace: '/repo', project_name: 'astrorder', status: 'running', updated_at: '2026-11-21T08:05:00Z' },
      ],
    })

    renderIsland()
    expect(screen.getByText('后台编译任务')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /展开运行中星辰/ }))
    expect(screen.getByRole('dialog', { name: '跨会话实时动态' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '进入矩阵' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '暂时收起状态提示' }))
    expect(screen.queryByText('后台编译任务')).not.toBeInTheDocument()
  })

  it('suppresses running pulse on /monitor while still showing pending approvals', () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 3,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [{ id: 'bg-1', agent_id: 'codex', title: '后台任务', workspace: '/repo', status: 'running', updated_at: '2026-11-21T08:05:00Z' }],
    })
    const { container } = renderIsland('/monitor', undefined)
    expect(container.querySelector('.attention-island')).toBeNull()
  })
})
