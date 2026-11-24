import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import { clearIslandNotifications, hasIslandNotificationHost, pushIslandNotification } from '../domain/islandNotifications'
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
    clearIslandNotifications()
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

    await userEvent.click(screen.getByRole('button', { name: /展开运行中会话/ }))
    expect(screen.getByRole('dialog', { name: '跨会话实时动态' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '进入矩阵' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '暂时收起状态提示' }))
    expect(screen.queryByText('后台编译任务')).not.toBeInTheDocument()
  })

  it('displays session count wording when multiple background sessions are running', () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 4,
      agents: [{ id: 'codex', name: 'Codex', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { id: 'active-session', agent_id: 'codex', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-21T08:00:00Z' },
        { id: 'bg-1', agent_id: 'codex', title: '任务一', workspace: '/repo', status: 'running', updated_at: '2026-11-21T08:05:00Z' },
        { id: 'bg-2', agent_id: 'codex', title: '任务二', workspace: '/repo', status: 'running', updated_at: '2026-11-21T08:06:00Z' },
      ],
    })

    renderIsland()
    expect(screen.getByText('2 个会话运行中')).toBeInTheDocument()
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

  it('pops session notifications inside the Dynamic Island, morphs back to running pill on dismiss, and jumps on click', async () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 5,
      agents: [{ id: 'agy', name: 'Antigravity', kind: 'antigravity', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { id: 'active-session', agent_id: 'agy', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-24T08:00:00Z' },
        { id: 'alert-center', agent_id: 'agy', title: '告警中心', workspace: '/opscore', status: 'idle', updated_at: '2026-11-24T08:05:00Z' },
        { id: 'bg-1', agent_id: 'agy', title: '后台编译任务', workspace: '/repo', status: 'running', updated_at: '2026-11-24T08:06:00Z' },
      ],
    })

    renderIsland('/chat/active-session?agent_id=agy', 'agy::active-session')
    expect(hasIslandNotificationHost()).toBe(true)
    expect(screen.getByText('后台编译任务')).toBeInTheDocument()

    act(() => {
      pushIslandNotification({
        key: 'notice-alert-1',
        kind: 'task_completed',
        title: '会话已完成',
        message: '告警中心 · 会话已完成',
        agent_id: 'agy',
        session_id: 'alert-center',
        created_at: '2026-11-24T08:07:00Z',
      })
    })

    // Deduplicated copy: bold session title + status detail without repeating "会话已完成"
    expect(screen.getByText('告警中心')).toBeInTheDocument()
    expect(screen.getByText('会话已完成')).toBeInTheDocument()

    // Dismissing morphs smoothly back to the background running session pill
    await userEvent.click(screen.getByRole('button', { name: '关闭通知' }))
    expect(screen.queryByText('告警中心')).not.toBeInTheDocument()
    expect(screen.getByText('后台编译任务')).toBeInTheDocument()

    // Pushing again and clicking navigates directly to the notified session and clears the notice
    act(() => {
      pushIslandNotification({
        key: 'notice-alert-2',
        kind: 'task_completed',
        title: '会话已完成',
        message: '告警中心 · 会话已完成',
        agent_id: 'agy',
        session_id: 'alert-center',
        created_at: '2026-11-24T08:08:00Z',
      })
    })

    await userEvent.click(screen.getByRole('button', { name: '前往' }))
    expect(screen.getByTestId('island-loc')).toHaveTextContent('/chat/alert-center?agent_id=agy')
    expect(screen.queryByText('告警中心')).not.toBeInTheDocument()
  })

  it('pops Dynamic Island notification even when no background sessions are running and shows queue count', () => {
    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 6,
      agents: [{ id: 'agy', name: 'Antigravity', kind: 'antigravity', status: 'ready', capabilities: ['chat'], limitation: null }],
      sessions: [
        { id: 'active-session', agent_id: 'agy', title: '当前会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-24T08:00:00Z' },
      ],
    })

    const { container } = renderIsland('/chat/active-session?agent_id=agy', 'agy::active-session')
    expect(container.querySelector('.attention-island')).toBeNull()

    act(() => {
      pushIslandNotification({
        key: 'n-1',
        kind: 'task_completed',
        title: '会话已完成',
        message: '监控Agent · 会话已完成',
        agent_id: 'agy',
        session_id: 's-1',
        created_at: '2026-11-24T08:10:00Z',
      })
      pushIslandNotification({
        key: 'n-2',
        kind: 'task_failed',
        title: '任务失败',
        message: '代码审查 · 执行失败',
        agent_id: 'agy',
        session_id: 's-2',
        created_at: '2026-11-24T08:11:00Z',
      })
    })

    expect(container.querySelector('.attention-island.is-notice.is-notice-error')).not.toBeNull()
    expect(screen.getByText('任务失败')).toBeInTheDocument()
    expect(screen.getByText('+1')).toBeInTheDocument()
  })
})
