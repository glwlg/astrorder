import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { AnalyticsPage } from './AnalyticsPage'

vi.mock('../../api/client', () => ({ api: { getAnalyticsUsage: vi.fn(), getAgentUsage: vi.fn() } }))
afterEach(() => { cleanup(); vi.resetAllMocks() })
const mount = () => render(<MantineProvider><AnalyticsPage /></MantineProvider>)

const sampleSessionUsage = {
  gatewayType: 'sessions',
  range: '7d',
  surface: 'all',
  connectionId: 'all',
  since: 0,
  generatedAt: '2026-11-18T09:30:00Z',
  summary: {
    requests: 15,
    totalTokens: 250_000,
    inputTokens: 30_000,
    outputTokens: 10_000,
    cacheReadInputTokens: 210_000,
    reasoningOutputTokens: 4_000,
    cacheHitRate: 0.875,
    activeServers: 3,
    activeAgents: 1,
    activeSessions: 2,
  },
  days: [{ date: '2026-11-18', requests: 15, totalTokens: 250_000 }],
  models: [{
    provider: 'antigravity',
    model: 'gemini-4-carbon-high',
    requests: 15,
    totalTokens: 250_000,
    inputTokens: 30_000,
    outputTokens: 10_000,
    cacheReadInputTokens: 210_000,
    shareRatio: 1,
  } as never],
  providers: [],
  accounts: [],
  servers: [
    { id: 'local', name: '本机' },
    { id: 'wsl', name: 'WSL' },
    { id: 'debian', name: 'Debian' },
  ],
  directAgents: [{
    agentId: 'ssh-antigravity-wsl',
    name: 'Antigravity · WSL',
    kind: 'antigravity',
    connectionId: 'wsl',
    connectionName: 'WSL',
    sessions: 2,
    requests: 15,
    totalTokens: 250_000,
    inputTokens: 30_000,
    outputTokens: 10_000,
    cacheReadInputTokens: 210_000,
    cacheCreationInputTokens: 0,
    reasoningOutputTokens: 4_000,
    cacheHitRate: 0.875,
    shareRatio: 1,
  }],
  directSessions: [{
    sessionId: '52794208-5f2e-4120-a6b1-99e5ee29c7f1',
    agentId: 'ssh-antigravity-wsl',
    agentName: 'Antigravity · WSL',
    agentKind: 'antigravity',
    connectionId: 'wsl',
    connectionName: 'WSL',
    title: 'ACP 升级会话',
    workspace: '/home/fixture/workspace/example-org/astrorder',
    model: 'gemini-4-carbon-high',
    updatedAt: '2026-11-18T09:30:00Z',
    requests: 15,
    inputTokens: 30_000,
    outputTokens: 10_000,
    cacheReadInputTokens: 210_000,
    cacheCreationInputTokens: 0,
    reasoningOutputTokens: 4_000,
    totalTokens: 250_000,
  }],
  historyTruncated: false,
  truncatedPrefixBytes: 0,
  entriesTruncated: false,
  entriesDropped: 0,
}

it('defaults to Session Token usage without embedding a third-party gateway page and supports server filtering', async () => {
  vi.mocked(api.getAnalyticsUsage).mockResolvedValue(sampleSessionUsage as never)
  mount()
  expect(screen.queryByRole('tab', { name: '网关统计' })).not.toBeInTheDocument()
  expect(await screen.findByText('ACP 升级会话')).toBeInTheDocument()
  expect(api.getAnalyticsUsage).toHaveBeenCalledWith({ range: '7d', surface: 'all', source: 'sessions' })
  expect(screen.queryByTitle('网关统计页')).not.toBeInTheDocument()
  expect(screen.getByRole('region', { name: '会话消耗排行' })).toHaveAttribute('tabindex', '0')

  const serverFilter = screen.getByRole('radiogroup', { name: '服务器筛选' })
  fireEvent.click(within(serverFilter).getByRole('radio', { name: 'WSL' }))
  await waitFor(() => {
    expect(api.getAnalyticsUsage).toHaveBeenCalledWith({
      range: '7d',
      surface: 'all',
      source: 'sessions',
      connection_id: 'wsl',
    })
  })
  fireEvent.click(within(serverFilter).getByRole('radio', { name: '全部服务器' }))
  await waitFor(() => {
    expect(api.getAnalyticsUsage).toHaveBeenLastCalledWith({
      range: '7d',
      surface: 'all',
      source: 'sessions',
    })
  })
})

it('switches cleanly to CLI usage tab', async () => {
  vi.mocked(api.getAnalyticsUsage).mockResolvedValue(sampleSessionUsage as never)
  vi.mocked(api.getAgentUsage).mockResolvedValue({
    range: '7d',
    summary: { calls: 0, succeeded: 0, failed: 0, active_agents: 0 },
    agents: [],
    capabilities: [],
    recent: [],
  })
  mount()
  await screen.findByText('ACP 升级会话')
  fireEvent.click(screen.getByRole('tab', { name: 'CLI 使用' }))
  expect(await screen.findByText('此时间范围内暂无调用记录')).toBeInTheDocument()
})
