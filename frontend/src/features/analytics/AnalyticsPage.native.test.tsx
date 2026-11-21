import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { AnalyticsPage } from './AnalyticsPage'
import { Sidebar } from '../../components/Sidebar'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import userEvent from '@testing-library/user-event'

vi.mock('../../api/client', () => ({ api: { getAnalyticsConfig: vi.fn(), getAnalyticsUsage: vi.fn(), getAgentUsage: vi.fn() } }))
afterEach(() => { cleanup(); vi.resetAllMocks(); delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop })
const config = { gateway_type: 'magpie', management_url: 'https://gateway.example/', statistics_url: 'https://gateway.example/#usage' }
const mount = () => render(<MantineProvider><AnalyticsPage /></MantineProvider>)

it('embeds the configured gateway page without fetching usage details', async () => {
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  mount()
  expect(await screen.findByTitle('网关统计页')).toHaveAttribute('src', config.statistics_url)
  expect(screen.getByRole('link', { name: '外部打开' })).toHaveAttribute('href', config.statistics_url)
  expect(api.getAnalyticsUsage).not.toHaveBeenCalled()
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue({ ...config, statistics_url: 'https://other.example/stats' } as never)
  fireEvent.click(screen.getByRole('button', { name: '刷新' }))
  expect(await screen.findByTitle('网关统计页')).toHaveAttribute('src', 'https://other.example/stats')
})

it('reloads the page address when gateway settings are saved', async () => {
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  mount()
  await screen.findByTitle('网关统计页')
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue({ ...config, gateway_type: 'opencodex', statistics_url: 'https://ocx.example/#usage' } as never)
  fireEvent(window, new Event('astrorder:gateway-config-changed'))
  await waitFor(() => expect(screen.getByTitle('网关统计页')).toHaveAttribute('src', 'https://ocx.example/#usage'))
})

it('opens the system browser through the desktop bridge', async () => {
  const openExternal = vi.fn().mockResolvedValue(undefined)
  ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { openExternal }
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  mount()
  fireEvent.click(await screen.findByRole('link', { name: '外部打开' }))
  expect(openExternal).toHaveBeenCalledWith(config.statistics_url)
})

it('uses a native top-level page on desktop and hides it on navigation', async () => {
  const showStatistics = vi.fn().mockResolvedValue(undefined)
  const hideStatistics = vi.fn().mockResolvedValue(undefined)
  ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { showStatistics, hideStatistics }
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  const view = mount()
  await screen.findByTitle('网关统计页')
  expect(showStatistics).toHaveBeenCalledWith(config.statistics_url, expect.objectContaining({ width: expect.any(Number), height: expect.any(Number) }), false)
  expect(screen.queryByTitle('网关统计页')?.tagName).not.toBe('IFRAME')
  const dialog = document.createElement('div')
  dialog.setAttribute('role', 'dialog')
  document.body.append(dialog)
  await waitFor(() => expect(hideStatistics).toHaveBeenCalled())
  const shownBefore = showStatistics.mock.calls.length
  dialog.remove()
  await waitFor(() => expect(showStatistics.mock.calls.length).toBeGreaterThan(shownBefore))
  view.unmount()
  expect(hideStatistics).toHaveBeenCalled()
})

it('hides the native gateway view when switching to CLI usage', async () => {
  const showStatistics = vi.fn().mockResolvedValue(undefined)
  const hideStatistics = vi.fn().mockResolvedValue(undefined)
  ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { showStatistics, hideStatistics }
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  vi.mocked(api.getAgentUsage).mockResolvedValue({
    range: '7d', summary: { calls: 0, succeeded: 0, failed: 0, active_agents: 0 }, agents: [], capabilities: [], recent: [],
  })
  mount()
  await waitFor(() => expect(showStatistics).toHaveBeenCalled())
  fireEvent.click(screen.getByRole('tab', { name: 'CLI 使用' }))
  expect(await screen.findByText('此时间范围内暂无调用记录')).toBeInTheDocument()
  expect(hideStatistics).toHaveBeenCalled()
  expect(screen.queryByTitle('网关统计页')).not.toBeInTheDocument()
})

it('keeps the native statistics page visible while its usage preview is hovered', async () => {
  const showStatistics = vi.fn().mockResolvedValue(undefined)
  const hideStatistics = vi.fn().mockResolvedValue(undefined)
  ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { showStatistics, hideStatistics }
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  vi.mocked(api.getAnalyticsUsage).mockResolvedValue({ range: 'today', summary: { requests: 1, totalTokens: 100 }, models: [], days: [] } as never)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(<MantineProvider><QueryClientProvider client={client}><MemoryRouter initialEntries={['/analytics']}>
    <Sidebar sessions={[]} agents={{}} onSelectSession={vi.fn()} />
    <AnalyticsPage />
  </MemoryRouter></QueryClientProvider></MantineProvider>)
  await waitFor(() => expect(showStatistics).toHaveBeenCalled())
  const before = hideStatistics.mock.calls.length
  const user = userEvent.setup()
  const tab = screen.getByRole('link', { name: '统计' })
  await user.hover(tab)
  await screen.findByText('今日模型用量')
  // Flush the native view's MutationObserver after Mantine mounts its portal.
  await new Promise(resolve => setTimeout(resolve, 0))
  expect(hideStatistics.mock.calls.length).toBe(before)
  await user.unhover(tab)
  await waitFor(() => expect(screen.queryByText('今日模型用量')).not.toBeInTheDocument())
  expect(hideStatistics.mock.calls.length).toBe(before)
  view.unmount()
  client.clear()
})

it('uses the management address for existing configurations', async () => {
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue({ ...config, statistics_url: '' } as never)
  mount()
  expect(await screen.findByTitle('网关统计页')).toHaveAttribute('src', config.management_url)
})

it('hides the native gateway view when switching to Session Token usage and supports server filtering', async () => {
  const showStatistics = vi.fn().mockResolvedValue(undefined)
  const hideStatistics = vi.fn().mockResolvedValue(undefined)
  ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { showStatistics, hideStatistics }
  vi.mocked(api.getAnalyticsConfig).mockResolvedValue(config as never)
  const servers = [
    { id: 'local', name: '本机' },
    { id: 'wsl', name: 'WSL' },
    { id: 'debian', name: 'Debian' },
  ]
  vi.mocked(api.getAnalyticsUsage).mockResolvedValue({
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
    servers,
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
  })
  mount()
  await waitFor(() => expect(showStatistics).toHaveBeenCalled())
  fireEvent.click(screen.getByRole('tab', { name: '会话 Token' }))
  expect(await screen.findByText('ACP 升级会话')).toBeInTheDocument()
  expect(api.getAnalyticsUsage).toHaveBeenCalledWith({ range: '7d', surface: 'all', source: 'sessions' })
  expect(hideStatistics).toHaveBeenCalled()
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

it('reports configuration errors without leaving a blank page', async () => {
  vi.mocked(api.getAnalyticsConfig).mockRejectedValue(new Error('配置读取失败'))
  mount()
  expect(await screen.findByText('配置读取失败')).toBeInTheDocument()
  expect(screen.queryByTitle('网关统计页')).not.toBeInTheDocument()
})
