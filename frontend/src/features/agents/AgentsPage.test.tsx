import { MantineProvider, Modal, Tabs } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { api } from '../../api/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AgentsPage } from './AgentsPage'
import { SettingsDrawerScope } from '../../components/SettingsDrawer'
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
    getEnvironments: vi.fn(async () => ({ items: [{ id: 'local', name: '本机', method: 'local', discovered: true, agents: [{ kind: 'hermes', available: true, state: 'discovered', detail: '', daemon_mode: true }, { kind: 'codex', available: true, state: 'disconnected', detail: '', daemon_mode: true }] }] })),
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
    getAgentMcpStatus: vi.fn(async () => ({ enabled: true })),
  },
}))

function renderPage(inSettingsModal = false) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const closeSettings = vi.fn()
  const result = render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        {inSettingsModal ? <SettingsDrawerScope>{hasOpenDrawer => <Modal className="settings-modal" opened onClose={closeSettings} title="设置" closeOnEscape={!hasOpenDrawer}>
          <Tabs defaultValue="connections" className="settings-tabs" orientation="vertical">
            <Tabs.List><Tabs.Tab value="connections">连接</Tabs.Tab></Tabs.List>
            <Tabs.Panel value="connections" className="settings-tab-panel"><AgentsPage /></Tabs.Panel>
          </Tabs>
        </Modal>}</SettingsDrawerScope> : <AgentsPage />}
      </QueryClientProvider>
    </MantineProvider>,
  )
  return { ...result, closeSettings }
}

afterEach(() => { cleanup(); vi.clearAllMocks(); useAstrorderStore.getState().resetRuntime() })

describe('Agent settings connection controls', () => {
  it('closes only the nested drawer on Escape and returns focus to its trigger', async () => {
    const { closeSettings } = renderPage(true)
    const trigger = await screen.findByRole('button', { name: '配置 Hermes' })
    trigger.focus()
    fireEvent.click(trigger)
    const drawer = await screen.findByRole('dialog', { name: 'Hermes · 运行配置' })
    const close = within(drawer).getByRole('button', { name: '关闭运行配置' })
    fireEvent.keyDown(close, { key: 'Escape' })
    expect(closeSettings).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Hermes · 运行配置' })).not.toBeInTheDocument())
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(screen.getByRole('dialog', { name: '设置' })).toBeInTheDocument()
  })
  it('contains the configuration drawer and its dismissible mask inside the settings modal', async () => {
    renderPage(true)
    const settings = await screen.findByRole('dialog', { name: '设置' })
    fireEvent.click(await screen.findByRole('button', { name: '配置 Hermes' }))
    const drawer = await screen.findByRole('dialog', { name: 'Hermes · 运行配置' })
    expect(settings).toContainElement(drawer)
    const mask = settings.querySelector('.settings-contained-drawer .mantine-Drawer-overlay')
    expect(mask).not.toBeNull()
    fireEvent.click(mask!)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Hermes · 运行配置' })).not.toBeInTheDocument())
    expect(screen.getByRole('dialog', { name: '设置' })).toBeInTheDocument()
  })
  it('shows only the selected configuration group instead of stacking all settings', async () => {
    useAstrorderStore.setState({ agents: { local: { id: 'local', name: '本机 Hermes', kind: 'hermes', status: 'ready', connection_id: 'local', capabilities: ['chat'], limitation: null } } })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: '配置 Hermes' }))
    const drawer = await screen.findByRole('dialog', { name: 'Hermes · 运行配置' })
    expect(within(drawer).getByText('星序 MCP 调度授权')).toBeInTheDocument()
    expect(within(drawer).queryByText('支持能力与通道规格')).not.toBeInTheDocument()
    fireEvent.click(within(drawer).getByRole('tab', { name: '能力' }))
    expect(within(drawer).getByText('支持能力与通道规格')).toBeInTheDocument()
    expect(within(drawer).queryByText('星序 MCP 调度授权')).not.toBeInTheDocument()
    fireEvent.click(within(drawer).getByRole('tab', { name: '维护' }))
    expect(within(drawer).getByRole('button', { name: '检查并升级' })).toBeInTheDocument()
    expect(within(drawer).queryByText('支持能力与通道规格')).not.toBeInTheDocument()
  })
  it('keeps configuration in the selected agent drawer and connection actions in its menu', async () => {
    renderPage()
    await screen.findByText('Hermes', { exact: true })
    expect(screen.queryByText('Agent 状态')).not.toBeInTheDocument()
    expect(screen.queryByText('支持能力与通道规格')).not.toBeInTheDocument()
    expect(screen.queryByText('强制重启')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '配置 Hermes' }))
    const drawer = await screen.findByRole('dialog', { name: 'Hermes · 运行配置' })
    expect(within(drawer).getByText('接入此 Agent 后可管理星序授权、运行时能力与版本。')).toBeInTheDocument()
    fireEvent.click(within(drawer).getByRole('button', { name: '关闭运行配置' }))
    fireEvent.click(screen.getByRole('button', { name: 'Codex 更多操作' }))
    expect(await screen.findByRole('menuitem', { name: '强制重启' })).toBeInTheDocument()
    expect(api.changeEnvironmentAgent).not.toHaveBeenCalled()
  })
  it('selects a host without connecting or changing its agents', async () => {
    vi.mocked(api.getEnvironments).mockResolvedValueOnce({ items: [
      { id: 'local', name: '本机', method: 'local', discovered: true, agents: [{ kind: 'codex', available: true, state: 'discovered', detail: '本机详情' }] },
      { id: 'ssh:fixture', name: '远端测试环境', method: 'ssh', discovered: true, agents: [{ kind: 'hermes', available: true, state: 'discovered', detail: '远端详情' }] },
    ] })
    renderPage()
    expect(await screen.findByText('本机详情')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /远端测试环境/ }))
    expect(screen.getByText('远端详情')).toBeInTheDocument()
    expect(screen.queryByText('本机详情')).not.toBeInTheDocument()
    expect(api.changeEnvironmentAgent).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '接入' }))
    await waitFor(() => expect(api.changeEnvironmentAgent).toHaveBeenCalledWith('ssh:fixture', 'hermes', true))
  })
  it('discovers both local Agents and requires explicit choice before connection', async () => {
    renderPage()
    expect(await screen.findByText('Hermes', { exact: true })).toBeInTheDocument()
    expect(screen.getByText('Codex', { exact: true })).toBeInTheDocument()
    expect(screen.getByTestId('environment-daemon-mode-local-hermes')).toHaveTextContent('守护进程托管')
    expect(screen.getByTestId('environment-daemon-mode-local-codex')).toHaveTextContent('守护进程托管')
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
