import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { AgyAccountsPage, CodexAccountsPage, GrokAccountsPage } from './AgyAccountsPage'
import { agyAccountsApi, codexAccountsApi, grokAccountsApi } from './agyAccountsApi'
import { SessionAccountsHoverCard } from './SessionAccountsHoverCard'
import type { Agent, Session } from '../../domain/types'

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
})

it('shows the same saved quota and timestamp after refreshing from either view on different targets', async () => {
  const user = userEvent.setup()
  const agent: Agent = { id: 'agy-wsl', kind: 'antigravity-ssh', name: 'WSL', connection_id: 'wsl', status: 'ready', capabilities: [], limitation: null }
  const session: Session = { id: 's', agent_id: agent.id, connection_id: 'wsl', status: 'idle', title: '', workspace: null, updated_at: '' }
  vi.spyOn(agyAccountsApi, 'targets').mockResolvedValue({ items: [{ id: 'local', name: '本机' }, { id: 'wsl', name: 'WSL' }] })
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'a', name: '共享账号' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async env => ({ current_account_id: env === 'wsl' ? 'a' : '', quota_supported: true }))
  const quota = vi.spyOn(agyAccountsApi, 'quota').mockImplementation(async env => ({
    as_of: env === 'wsl' ? '2026-11-21T14:00:00Z' : '2026-11-21T15:00:00Z',
    groups: [{ name: 'Gemini', buckets: [{ id: 'w', name: '', window: 'weekly', remaining_fraction: env === 'wsl' ? .85 : .78 }] }],
  }))
  render(<MantineProvider env="test"><MemoryRouter initialEntries={['/agent-accounts?environment=wsl']}>
    <QueryClientProvider client={new QueryClient()}>
      <AgyAccountsPage /><SessionAccountsHoverCard link={<a href="/agent-accounts">账号</a>} agent={agent} session={session} />
    </QueryClientProvider>
  </MemoryRouter></MantineProvider>)
  const row = within(await screen.findByRole('row', { name: /共享账号/ }))
  const select = screen.getByLabelText('目标 Agent')
  await waitFor(() => expect(select).toHaveValue('wsl'))
  await user.selectOptions(select, 'local')
  const link = screen.getByRole('link', { name: '账号' })
  await user.hover(link)
  await user.click(await screen.findByRole('button', { name: '刷新额度' }))
  expect(await row.findByText('85%')).toBeVisible()
  expect(row.queryByText('当前使用')).not.toBeInTheDocument()
  await user.click(row.getByRole('button', { name: '刷新 共享账号 的额度' }))
  expect(await row.findByText('78%')).toBeVisible()
  await user.hover(link)
  const card = within(await screen.findByRole('list', { name: '当前平台的账号' }))
  expect(await card.findByText('78%')).toBeVisible()
  expect(card.getByText('当前使用')).toBeVisible()
  expect(card.getByRole('time')).toHaveAttribute('datetime', row.getByRole('time').getAttribute('datetime'))
  expect(quota.mock.calls.map(([env, id]) => [env, id])).toEqual([['wsl', 'a'], ['local', 'a']])
})

it('loads quotas and individual subscription tiers on demand and reopens with the last snapshots', async () => {
  vi.spyOn(agyAccountsApi, 'targets').mockResolvedValue({ items: [{ id: 'dev', name: 'AGY · 开发机' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'working', quota_supported: true })
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [
    { id: 'working', name: '工作账号' },
    { id: 'spare', name: '备用账号' },
    { id: 'expired', name: '测试账号' },
  ] })
  vi.spyOn(agyAccountsApi, 'quota').mockImplementation(async (_, id) => {
    if (id === 'expired') throw new Error('登录已失效')
    return { plan: id === 'working' ? 'Google AI Pro' : 'Free', as_of: '2026-11-21T06:00:00Z', groups: [{ name: 'Gemini', buckets: [
      { id: '5h', window: '5h', name: '', remaining_fraction: id === 'working' ? 0.52 : 1 },
    ] }] }
  })
  const client = new QueryClient()
  render(<MantineProvider><MemoryRouter><QueryClientProvider client={client}><AgyAccountsPage /></QueryClientProvider></MemoryRouter></MantineProvider>)
  const active = within(await screen.findByRole('row', { name: /工作账号/ }))
  const spare = within(await screen.findByRole('row', { name: /备用账号/ }))
  const expired = within(await screen.findByRole('row', { name: /测试账号/ }))
  expect(active.getByText('尚未刷新')).toBeVisible()
  expect(agyAccountsApi.targetState).toHaveBeenCalledWith('dev', expect.any(AbortSignal))
  expect(agyAccountsApi.quota).not.toHaveBeenCalled()
  fireEvent.click(active.getByRole('button', { name: '刷新 工作账号 的额度' }))
  expect(await active.findByText('52%')).toBeInTheDocument()
  expect(active.getByText('Google AI Pro')).toBeInTheDocument()
  expect(agyAccountsApi.quota).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: '刷新全部' }))
  expect(active.getByText('当前使用')).toBeInTheDocument()
  expect(await spare.findByText('100%')).toBeInTheDocument()
  expect(spare.getByText('Free')).toBeInTheDocument()
  expect(spare.getByRole('button', { name: '切换到 备用账号' })).toBeEnabled()
  expect(await expired.findByText('额度查询失败')).toBeInTheDocument()
  expect(expired.queryByRole('meter')).not.toBeInTheDocument()
  expect(agyAccountsApi.quota).toHaveBeenCalledTimes(4)
  cleanup()
  render(<MantineProvider><MemoryRouter><QueryClientProvider client={new QueryClient()}><AgyAccountsPage /></QueryClientProvider></MemoryRouter></MantineProvider>)
  expect(await screen.findByText('Google AI Pro')).toBeVisible()
  expect(agyAccountsApi.quota).toHaveBeenCalledTimes(4)
})

it('renders CodexAccountsPage and GrokAccountsPage with platform quotas and delete support', async () => {
  HTMLDialogElement.prototype.showModal = function showModal() { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function close() { this.removeAttribute('open') }

  vi.spyOn(codexAccountsApi, 'targets').mockResolvedValue({ items: [{ id: 'local', name: 'Codex · 本机' }] })
  vi.spyOn(codexAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'cx-1', quota_supported: true })
  vi.spyOn(codexAccountsApi, 'accounts').mockResolvedValue({
    items: [
      { id: 'cx-1', name: '主力 Codex', email: 'codex@example.com' },
      { id: 'cx-2', name: '备用 Codex' },
    ],
  })
  vi.spyOn(codexAccountsApi, 'quota').mockResolvedValue({
    plan: 'ChatGPT Plus',
    as_of: '2026-11-26T02:00:00Z',
    groups: [{ name: 'Codex', buckets: [{ id: '5h', name: '5 小时', window: '5h', remaining_fraction: 0.96, reset_at: Date.parse('2026-11-26T07:00:00Z') }] }],
  })
  const delCodex = vi.spyOn(codexAccountsApi, 'deleteAccount').mockResolvedValue({ deleted: true, account_id: 'cx-2' })

  render(<MantineProvider env="test"><MemoryRouter><QueryClientProvider client={new QueryClient()}>
    <CodexAccountsPage />
  </QueryClientProvider></MemoryRouter></MantineProvider>)

  const row = within(await screen.findByRole('row', { name: /主力 Codex/ }))
  expect(row.getByText('codex@example.com')).toBeVisible()
  fireEvent.click(row.getByRole('button', { name: '刷新 主力 Codex 的额度' }))
  expect(await row.findByText('96%')).toBeVisible()
  expect(row.getByText('ChatGPT Plus')).toBeVisible()

  const spareRow = within(screen.getByRole('row', { name: /备用 Codex/ }))
  fireEvent.click(spareRow.getByRole('button', { name: '删除 备用 Codex' }))
  fireEvent.click(await screen.findByRole('button', { name: '确认删除' }))
  await waitFor(() => expect(delCodex).toHaveBeenCalledWith('cx-2'))

  cleanup()

  vi.spyOn(grokAccountsApi, 'targets').mockResolvedValue({ items: [{ id: 'local', name: 'Grok · 本机' }] })
  vi.spyOn(grokAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'gk-1', quota_supported: true })
  vi.spyOn(grokAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'gk-1', name: 'Grok 主号' }] })
  vi.spyOn(grokAccountsApi, 'quota').mockResolvedValue({
    plan: 'SuperGrok',
    detail: '已认证（xai@example.com）',
    as_of: '2026-11-26T02:00:00Z',
    groups: [],
  })

  render(<MantineProvider env="test"><MemoryRouter><QueryClientProvider client={new QueryClient()}>
    <GrokAccountsPage />
  </QueryClientProvider></MemoryRouter></MantineProvider>)

  const grokRow = within(await screen.findByRole('row', { name: /Grok 主号/ }))
  fireEvent.click(grokRow.getByRole('button', { name: '刷新 Grok 主号 的额度' }))
  expect(await grokRow.findByText('SuperGrok')).toBeVisible()
  expect(grokRow.getByText('已认证（xai@example.com）')).toBeVisible()
})
