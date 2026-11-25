import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { NewSessionDialog } from '../../components/NewSessionDialog'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { EnvironmentConnections } from '../agents/EnvironmentConnections'
import { SessionConnectionNotice } from '../chat/SessionConnectionNotice'
import { UsageGatewaySettingsCard } from '../services/UsageGatewaySettingsCard'
import { AGENT_CODEX_PLUGIN_ID, AGENT_GROK_PLUGIN_ID, AGENT_HERMES_PLUGIN_ID } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

const codexAgent: Agent = {
  id: 'local-codex',
  name: 'Codex',
  kind: 'codex',
  status: 'ready',
  capabilities: ['chat'],
  limitation: null,
}

const hermesAgent: Agent = {
  id: 'local-hermes',
  name: 'Hermes',
  kind: 'hermes',
  status: 'ready',
  capabilities: ['chat'],
  limitation: null,
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  usePluginSettingsStore.getState().reset()
  useAstrorderStore.setState({ agents: {}, sessions: {} })
})

describe('Agent plugin gating', () => {
  it('filters disabled Agent plugins out of EnvironmentConnections', async () => {
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [AGENT_CODEX_PLUGIN_ID]: false,
        [AGENT_GROK_PLUGIN_ID]: false,
        [AGENT_HERMES_PLUGIN_ID]: true,
      },
      hydrated: true,
    })

    vi.spyOn(api, 'getEnvironments').mockResolvedValue({
      items: [
        {
          id: 'local',
          name: '本机',
          method: 'local',
          discovered: true,
          agents: [
            { kind: 'hermes', available: true, state: 'connected', detail: '已接入' },
            { kind: 'codex', available: true, state: 'disconnected', detail: '未接入' },
            { kind: 'grok', available: false, state: 'offline', detail: '未安装' },
          ],
        },
      ],
    })
    vi.spyOn(api, 'getConnections').mockResolvedValue({
      local: { kind: 'hermes', state: 'connected', available: true, agent_id: 'local-hermes', version: null, session_id: null, detail: '' },
      ssh: { items: [], state: 'unconfigured', settings: null, detail: '' },
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MantineProvider>
          <EnvironmentConnections />
        </MantineProvider>
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Hermes')).toBeInTheDocument()
    expect(screen.queryByText('Codex')).toBeNull()
    expect(screen.queryByText('Grok')).toBeNull()
  })

  it('filters disabled Agent plugins out of NewSessionDialog', () => {
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [AGENT_CODEX_PLUGIN_ID]: false,
        [AGENT_HERMES_PLUGIN_ID]: true,
      },
      hydrated: true,
    })

    render(
      <MantineProvider>
        <NewSessionDialog
          agents={{ [codexAgent.id]: codexAgent, [hermesAgent.id]: hermesAgent }}
          project={null}
          onClose={() => {}}
          onCreated={() => {}}
        />
      </MantineProvider>,
    )

    // Only Hermes remains so it auto-selects Hermes and does not show Codex
    expect(screen.getByText(/Hermes/)).toBeInTheDocument()
    expect(screen.queryByText(/Codex/)).toBeNull()
  })

  it('shows read-only re-enable notice in SessionConnectionNotice when agent plugin is disabled', () => {
    usePluginSettingsStore.setState({
      enabledPlugins: { [AGENT_CODEX_PLUGIN_ID]: false },
      hydrated: true,
    })
    useAstrorderStore.setState({
      agents: { [codexAgent.id]: codexAgent },
    })

    const historicalSession: Session = {
      id: 'sess-old',
      agent_id: codexAgent.id,
      title: 'Old Codex Session',
      workspace: null,
      status: 'idle',
      updated_at: '2026-11-24T00:00:00Z',
    }

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <MantineProvider>
          <SessionConnectionNotice session={historicalSession} />
        </MantineProvider>
      </QueryClientProvider>,
    )

    expect(screen.getByText('Codex 插件已停用')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新启用' })).toBeInTheDocument()
  })

  it('hides disabled Agent checkboxes in UsageGatewaySettingsCard sync targets', async () => {
    usePluginSettingsStore.setState({
      enabledPlugins: {
        [AGENT_CODEX_PLUGIN_ID]: true,
        [AGENT_HERMES_PLUGIN_ID]: false,
        [AGENT_GROK_PLUGIN_ID]: false,
      },
      hydrated: true,
    })

    vi.spyOn(api, 'getAnalyticsConfig').mockResolvedValue({
      gateway_type: 'magpie',
      management_url: 'https://mag.example.com',
      inference_url: 'http://127.0.0.1:3425/v1',
      masked_key: 'sk-test',
      target_overrides: {},
    })
    vi.spyOn(api, 'getModelSyncTargets').mockResolvedValue({
      items: [
        { id: 'local', kind: 'local', name: '本机', agents: ['codex', 'grok', 'hermes', 'antigravity'] },
      ],
    })
    vi.spyOn(api, 'getModelSyncJobs').mockResolvedValue({ items: [] })

    render(
      <MantineProvider>
        <UsageGatewaySettingsCard />
      </MantineProvider>,
    )

    expect(await screen.findByLabelText('Codex')).toBeInTheDocument()
    expect(screen.getByLabelText('Antigravity (agy)')).toBeInTheDocument()
    expect(screen.queryByLabelText('Grok')).toBeNull()
    expect(screen.queryByLabelText('Hermes')).toBeNull()
  })
})
