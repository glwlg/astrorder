import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { api } from '../../api/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AgentsPage } from './AgentsPage'
import { useAstrorderStore } from '../../state/store'

vi.mock('../../hooks/useAstrorderData', () => ({
  useRuntime: () => ({ data: { items: [] }, isLoading: false, error: null, refetch: vi.fn() }),
  useConnections: () => ({
    data: {
      local: {
        kind: 'hermes',
        state: 'discovered',
        available: true,
        agent_id: null,
        version: 'Hermes Agent v-test',
        detail: '发现本机 Hermes；尚未加载 Astrorder 连接器。',
      },
      ssh: { items: [], state: 'unconfigured', settings: null, detail: '尚未配置远程 SSH 连接。' },
    },
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useConnectionHistory: () => ({ data: undefined, isLoading: false, error: null, hasNextPage: false, fetchNextPage: vi.fn() }),
}))

vi.mock('../../api/client', () => ({
  ApiError: class ApiError extends Error { detail = this.message },
  api: {
    getEnvironments: vi.fn(async () => ({ items: [{ id: 'local', name: '本机', method: 'local', discovered: true, agents: [{ kind: 'hermes', available: true, state: 'discovered', detail: '' }, { kind: 'codex', available: true, state: 'disconnected', detail: '' }] }] })),
    getConnections: vi.fn(async () => ({ ssh: { items: [] } })),
    discoverEnvironment: vi.fn(async () => ({})),
    changeEnvironmentAgent: vi.fn(async () => ({})),
    connectLocalHermes: vi.fn(),
    disconnectLocalHermes: vi.fn(),
    saveSshConnection: vi.fn(async () => ({ id: 'new-ssh' })),
    testSshConnection: vi.fn(),
    connectSshConnection: vi.fn(),
    disconnectSshConnection: vi.fn(),
    launchRuntime: vi.fn(),
  },
}))

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <AgentsPage />
      </QueryClientProvider>
    </MantineProvider>,
  )
}

afterEach(() => { cleanup(); vi.clearAllMocks(); useAstrorderStore.getState().resetRuntime() })

describe('Agent settings connection controls', () => {
  it('discovers both local Agents and requires explicit choice before connection', async () => {
    renderPage()
    expect(await screen.findByText('Hermes', { exact: true })).toBeInTheDocument()
    expect(screen.getByText('Codex', { exact: true })).toBeInTheDocument()
    expect(api.changeEnvironmentAgent).not.toHaveBeenCalled()
    fireEvent.click(screen.getAllByRole('button', { name: '接入' })[1])
    await waitFor(() => expect(api.changeEnvironmentAgent).toHaveBeenCalledWith('local', 'codex', true))
  })
  it('shows the persisted connection failure instead of labeling a failed Agent as discovered', async () => {
    vi.mocked(api.getEnvironments).mockResolvedValueOnce({
      items: [{
        id: 'debian',
        name: 'Debian',
        method: 'ssh',
        discovered: true,
        agents: [{
          kind: 'hermes',
          available: true,
          state: 'error',
          detail: '远端 Astrorder bootstrap [project_activation_required]：Astrorder plugin is not already enabled.',
        }],
      }],
    })
    renderPage()
    expect(await screen.findByText('连接失败')).toBeInTheDocument()
    expect(screen.getByText('远端 Astrorder bootstrap [project_activation_required]：Astrorder plugin is not already enabled.')).toBeInTheDocument()
    expect(screen.queryByText('已发现')).not.toBeInTheDocument()
  })
  it('saves SSH without Agent-specific fields and discovers before any Agent is connected', async () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: '添加 SSH 连接' }))
    expect(await screen.findByLabelText('主机')).toBeInTheDocument()
    expect(screen.getByLabelText('SSH 配置别名')).toBeInTheDocument()
    expect(screen.queryByLabelText('远端 Hermes 路径')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('主机'), { target: { value: 'fixture-host' } })
    fireEvent.click(screen.getByRole('button', { name: '保存并发现 Agent' }))
    await waitFor(() => expect(api.discoverEnvironment).toHaveBeenCalledWith('new-ssh'))
    expect(api.changeEnvironmentAgent).not.toHaveBeenCalled()
    expect(api.saveSshConnection).toHaveBeenCalledWith(expect.objectContaining({ host: 'fixture-host', port: 22 }))
    expect(screen.queryByText('Inert integration fixture')).not.toBeInTheDocument()
  })
})
