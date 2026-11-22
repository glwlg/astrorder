import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { AgyAccountsPage } from './AgyAccountsPage'
import { agyAccountsApi } from './agyAccountsApi'
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
