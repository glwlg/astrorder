import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { AGENT_CODEX_PLUGIN_ID, AGENT_GROK_PLUGIN_ID } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { AgentEnableModal } from './AgentEnableModal'
import { ToolchainInstallModal } from './ToolchainInstallModal'

vi.mock('./EmbeddedTerminalPane', () => ({
  EmbeddedTerminalPane: ({ environmentId, command }: { environmentId: string; command?: string }) => (
    <div data-testid="embedded-terminal" data-env={environmentId}>
      {command}
    </div>
  ),
}))

function wrap(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={client}>
      <MantineProvider>{ui}</MantineProvider>
    </QueryClientProvider>
  )
}

describe('AgentEnableModal & ToolchainInstallModal', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    usePluginSettingsStore.getState().reset()
    usePluginSettingsStore.setState({
      enabledPlugins: { [AGENT_CODEX_PLUGIN_ID]: false, [AGENT_GROK_PLUGIN_ID]: false },
      hydrated: true,
    })
  })

  it('shows guided install and path validation when agent is not installed', async () => {
    vi.spyOn(api, 'getToolchainTargets').mockResolvedValue({
      items: [{ id: 'local', kind: 'local', name: '本机' }],
    })
    vi.spyOn(api, 'getToolchainStatus').mockResolvedValue({
      environment_id: 'local',
      target_kind: 'local',
      target_name: '本机',
      os: 'linux',
      checked_at: '2026-11-25T00:00:00Z',
      items: [
        {
          id: 'node',
          kind: 'tool',
          name: 'Node.js / npm',
          description: 'Node runtime',
          installed: false,
          recipes: [{ id: 'node-fnm', label: 'fnm', mode: 'background', command: 'install-node' }],
        },
        {
          id: 'codex',
          kind: 'agent',
          plugin_id: AGENT_CODEX_PLUGIN_ID,
          name: 'OpenAI Codex',
          description: 'Codex CLI',
          installed: false,
          dependencies: ['node'],
          missing_dependencies: ['node'],
          auth_command: 'codex login',
          recipes: [{ id: 'codex-npm', label: 'npm', mode: 'background', command: 'npm i -g @openai/codex@latest' }],
        },
      ],
    })
    vi.spyOn(api, 'validateToolchainExecutable').mockResolvedValue({
      valid: true,
      resolved_path: '/opt/custom/codex',
      version: '0.50.0',
    })

    render(wrap(<AgentEnableModal opened kind="codex" onClose={() => {}} />))

    await screen.findByText('未检测到')
    expect(screen.getByText(/方式一：辅助安装/)).toBeInTheDocument()
    expect(screen.getByText(/方式二：指定已安装路径/)).toBeInTheDocument()

    const pathInput = screen.getByLabelText('可执行文件绝对路径')
    fireEvent.change(pathInput, { target: { value: '/opt/custom/codex' } })
    fireEvent.click(screen.getByRole('button', { name: '校验' }))

    await screen.findByText(/校验通过（版本 0\.50\.0）/)
  })

  it('switches between background install log mode and interactive terminal mode', async () => {
    const startSpy = vi.spyOn(api, 'startToolchainInstallJob').mockResolvedValue({
      id: 'install-1',
      title: '安装 OpenAI Codex',
      environment_id: 'local',
      tool_ids: ['node', 'codex'],
      status: 'completed',
      detail: '安装结束：成功 2，失败 0',
      log: '[DONE] 安装成功',
      created_at: '2026-11-25T00:00:00Z',
      current: 2,
      total: 2,
      target: 'OpenAI Codex',
    })

    render(
      wrap(
        <ToolchainInstallModal
          opened
          onClose={() => {}}
          environmentId="local"
          environmentName="本机"
          item={{
            id: 'codex',
            kind: 'agent',
            name: 'OpenAI Codex',
            description: 'Codex',
            installed: false,
            dependencies: ['node'],
            missing_dependencies: ['node'],
            auth_command: 'codex login',
            recipes: [{ id: 'codex-npm', label: 'npm', mode: 'background', command: 'npm i -g @openai/codex@latest' }],
          }}
          allItems={[
            {
              id: 'node',
              kind: 'tool',
              name: 'Node.js / npm',
              description: 'Node',
              installed: false,
              recipes: [{ id: 'node-fnm', label: 'fnm', mode: 'background', command: 'install-node' }],
            },
          ]}
        />,
      ),
    )

    expect(screen.getByText(/检测到缺失前置依赖：Node\.js \/ npm/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '开始安装' }))

    await waitFor(() => {
      expect(startSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          environment_id: 'local',
          tool_ids: ['codex'],
          include_deps: true,
        }),
      )
    })

    fireEvent.click(screen.getByText('内置交互终端（PTY）'))
    expect(await screen.findByTestId('embedded-terminal')).toHaveTextContent('install-node && npm i -g @openai/codex@latest')
  })
})
