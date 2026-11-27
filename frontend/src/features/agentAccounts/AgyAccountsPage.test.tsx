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

it('pauses AGY quota queries across account page and hover card while keeping state refresh and switching enabled', async () => {
  const user = userEvent.setup()
  const agent: Agent = { id: 'agy-wsl', kind: 'antigravity-ssh', name: 'WSL', connection_id: 'wsl', status: 'ready', capabilities: [], limitation: null }
  const session: Session = { id: 's', agent_id: agent.id, connection_id: 'wsl', status: 'idle', title: '', workspace: null, updated_at: '' }
  localStorage.setItem('astrorder:agy-quota:v2:a', JSON.stringify({
    as_of: '2026-11-21T14:00:00Z',
    plan: 'Google AI Pro',
    groups: [{ name: 'Gemini', buckets: [{ id: 'w', name: '', window: 'weekly', remaining_fraction: 0.85 }] }],
  }))
  vi.spyOn(agyAccountsApi, 'targets').mockResolvedValue({ items: [{ id: 'local', name: '本机' }, { id: 'wsl', name: 'WSL' }] })
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'a', name: '共享账号' }, { id: 'b', name: '备用账号' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async env => ({
    current_account_id: env === 'wsl' ? 'a' : '',
    quota_supported: false,
    quota_paused: true,
  }))
  const quota = vi.spyOn(agyAccountsApi, 'quota')
  render(<MantineProvider env="test"><MemoryRouter initialEntries={['/agent-accounts?environment=wsl']}>
    <QueryClientProvider client={new QueryClient()}>
      <AgyAccountsPage /><SessionAccountsHoverCard link={<a href="/agent-accounts">账号</a>} agent={agent} session={session} />
    </QueryClientProvider>
  </MemoryRouter></MantineProvider>)

  const row = within(await screen.findByRole('row', { name: /共享账号/ }))
  expect(screen.getByText('AGY 额度查询已暂停')).toBeVisible()
  expect(row.getByText('额度查询已暂停')).toBeVisible()
  expect(row.queryByText('85%')).not.toBeInTheDocument()
  expect(row.queryByRole('button', { name: /刷新 共享账号/ })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '刷新设置' })).not.toBeInTheDocument()
  expect(await row.findByRole('button', { name: '重新应用 共享账号' })).toBeEnabled()

  // Clicking 刷新状态 refreshes account state/library without invoking quota API.
  await user.click(screen.getByRole('button', { name: '刷新状态' }))
  expect(quota).not.toHaveBeenCalled()

  // Hover card suppresses quota refresh and stale quota bars while keeping quick switching available.
  const link = screen.getByRole('link', { name: '账号' })
  await user.hover(link)
  const card = within(await screen.findByRole('list', { name: '当前平台的账号' }))
  expect(screen.queryByRole('button', { name: '刷新额度' })).not.toBeInTheDocument()
  expect(card.queryByText('85%')).not.toBeInTheDocument()
  expect(card.getByRole('button', { name: '切换到 备用账号' })).toBeEnabled()
  expect(quota).not.toHaveBeenCalled()
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
