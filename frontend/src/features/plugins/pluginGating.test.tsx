import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Sidebar } from '../../components/Sidebar'
import { SessionToolsMenu } from '../chat/SessionToolsMenu'
import { SidecarHost } from '../sidecar/SidecarHost'
import { ModelGatewaySettingsPage } from '../services/ModelGatewaySettingsPage'
import {
  BLACKBOARD_VIEWER_PLUGIN_ID,
  BROWSER_VIEWER_PLUGIN_ID,
  COMMAND_COMPRESSION_PLUGIN_ID,
  MEMORY_VIEWER_PLUGIN_ID,
  WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID,
  WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID,
  WORKSPACE_BACKUPS_PLUGIN_ID,
  WORKSPACE_GROUPS_PLUGIN_ID,
  WORKSPACE_MONITOR_PLUGIN_ID,
  XTERM_VIEWER_PLUGIN_ID,
} from './builtinPlugins'
import { usePluginSettingsStore } from './pluginSettingsStore'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'

const sampleSession: Session = {
  id: 'sess-1',
  agent_id: 'codex-1',
  title: 'Test Session',
  workspace: '/workspace/demo',
  status: 'idle',
  updated_at: '2026-11-24T00:00:00Z',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  usePluginSettingsStore.getState().reset()
  useSidecarStore.setState({ tabs: [], mountedTabs: {}, activeTabId: '', activeSessionKey: '' })
})

describe('Plugin gating across Sidecar, SessionToolsMenu, and ModelGatewaySettingsPage', () => {
  it('hides disabled Sidecar plugins from SidecarWelcomeMenu', () => {
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [BROWSER_VIEWER_PLUGIN_ID]: false,
        [XTERM_VIEWER_PLUGIN_ID]: false,
        [MEMORY_VIEWER_PLUGIN_ID]: false,
      },
      hydrated: true,
    })

    render(
      <MantineProvider>
        <SidecarHost
          session={sampleSession}
          commands={[]}
          messages={[]}
          approvals={[]}
          onApproval={vi.fn()}
          onCloseSidecar={vi.fn()}
        />
      </MantineProvider>,
    )

    expect(screen.getByText('文件')).toBeInTheDocument()
    expect(screen.getByText('黑板')).toBeInTheDocument()
    expect(screen.queryByText('浏览器')).toBeNull()
    expect(screen.queryByText('终端')).toBeNull()
    expect(screen.queryByText('知识记忆')).toBeNull()
  })

  it('hides SessionToolsMenu when both memory and blackboard plugins are disabled', () => {
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [MEMORY_VIEWER_PLUGIN_ID]: false,
        [BLACKBOARD_VIEWER_PLUGIN_ID]: false,
      },
      hydrated: true,
    })

    render(
      <MantineProvider>
        <SessionToolsMenu session={sampleSession} />
      </MantineProvider>,
    )

    expect(screen.queryByRole('button', { name: '更多会话工具' })).toBeNull()
  })

  it('gates OpenViking, BrowserEngine, and UsageGateway cards in ModelGatewaySettingsPage by plugin state', async () => {
    vi.spyOn(api, 'getLlmConfig').mockResolvedValue({
      base_url: '',
      model: '',
      reasoning: 'low',
      configured: false,
      masked_key: '',
    })
    vi.spyOn(api, 'getSystemOneConfig').mockResolvedValue({
      mode: 'official',
      model: '',
      configured: false,
      masked_key: '',
      gateway_configured: false,
      gateway_type: 'opencodex',
      gateway_url: '',
    })

    usePluginSettingsStore.setState({
      enabledPlugins: {
        [WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID]: false,
        [MEMORY_VIEWER_PLUGIN_ID]: false,
        [BROWSER_VIEWER_PLUGIN_ID]: false,
      },
      hydrated: true,
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <MantineProvider>
            <ModelGatewaySettingsPage />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    expect(screen.getByText('通用 LLM')).toBeInTheDocument()
    expect(screen.getByText('System One 模型服务')).toBeInTheDocument()
    expect(screen.queryByText('LLM 网关与模型同步')).toBeNull()
    expect(screen.queryByText('OpenViking 记忆与知识库网关')).toBeNull()
    expect(screen.queryByText('浏览器自动任务')).toBeNull()
  })

  it('hides disabled workspace nav modules, command-compression settings tab, and stops todayUsage polling in Sidebar', async () => {
    const usageSpy = vi.spyOn(api, 'getAnalyticsUsage')
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [WORKSPACE_GROUPS_PLUGIN_ID]: false,
        [WORKSPACE_MONITOR_PLUGIN_ID]: false,
        [WORKSPACE_ANALYTICS_GATEWAY_PLUGIN_ID]: false,
        [WORKSPACE_BACKUPS_PLUGIN_ID]: false,
        [WORKSPACE_AGENT_ACCOUNTS_PLUGIN_ID]: false,
        [COMMAND_COMPRESSION_PLUGIN_ID]: false,
      },
      hydrated: true,
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <MantineProvider>
            <Sidebar sessions={[]} agents={{}} projects={[]} onSelectSession={vi.fn()} />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    expect(screen.getByRole('link', { name: '会话' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '待办' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '群聊' })).toBeNull()
    expect(screen.queryByRole('link', { name: '矩阵' })).toBeNull()
    expect(screen.queryByRole('link', { name: '统计' })).toBeNull()
    expect(screen.queryByRole('link', { name: '备份' })).toBeNull()
    expect(screen.queryByRole('link', { name: '账号' })).toBeNull()
    expect(usageSpy).not.toHaveBeenCalled()

    // Opening Settings modal hides the "命令压缩" (RTK) tab when disabled
    fireEvent.click(screen.getByRole('button', { name: '打开设置' }))
    expect(await screen.findByRole('tab', { name: '插件' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: '命令压缩' })).toBeNull()
    expect(screen.queryByRole('tab', { name: 'RTK' })).toBeNull()
  })
})
