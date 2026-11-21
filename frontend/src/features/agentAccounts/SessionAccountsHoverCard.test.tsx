import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Agent, Session } from '../../domain/types'
import { SessionAccountsHoverCard } from './SessionAccountsHoverCard'
import { agyAccountsApi } from './agyAccountsApi'

const agent: Agent = { id: 'dev-agy', kind: 'antigravity-ssh', name: 'AGY · 开发机', connection_id: 'dev', status: 'ready', capabilities: [], limitation: null }
const session: Session = { id: 'chat', title: '会话', agent_id: agent.id, connection_id: 'dev', status: 'idle', workspace: null, updated_at: '' }
function preview(owner: Agent, selected: Session) {
  return render(<MantineProvider env="test"><QueryClientProvider client={new QueryClient()}>
    <SessionAccountsHoverCard link={<a href="/agent-accounts">账号</a>} agent={owner} session={selected} />
  </QueryClientProvider></MantineProvider>)
}
afterEach(() => { localStorage.clear(); vi.restoreAllMocks() })

it('loads on hover, uses target snapshots and switches only the selected machine without forcing', async () => {
  const user = userEvent.setup()
  let active = 'a'
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'a', name: '主账号' }, { id: 'b', name: '备用' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async () => ({ current_account_id: active, quota_supported: true }))
  const quota = vi.spyOn(agyAccountsApi, 'quota')
  const apply = vi.spyOn(agyAccountsApi, 'switch').mockImplementation(async (_, id) => { active = id; return { account_id: id, restarted: true } })
  const snapshot = { as_of: '2026-11-21T04:00:00Z', groups: [
    { name: 'Gemini Models', buckets: [
      { id: 'weekly', name: 'Week', window: 'weekly', remaining_fraction: 0.21 },
      { id: '5h', name: '', window: 'five_hour', remaining_fraction: 0 },
    ] },
    { name: 'Gemini 4 Argon', buckets: [{ id: 'weekly', name: 'Week', window: 'week', remaining_fraction: 0.53 }] },
  ] }
  quota.mockResolvedValue(snapshot)
  localStorage.setItem('astrorder:agy-quota:v1:dev:b', JSON.stringify(snapshot))
  preview(agent, session)
  expect(agyAccountsApi.accounts).not.toHaveBeenCalled()
  await user.hover(screen.getByRole('link', { name: '账号' }))
  const list = await screen.findByRole('list', { name: '当前平台的账号' })
  expect(await within(list).findByText('53%')).toBeVisible()
  expect(within(list).getByRole('meter', { name: 'Gemini Models · 5 小时剩余额度' })).toHaveTextContent('0%')
  expect(within(list).getByRole('meter', { name: 'Gemini 4 Argon · 每周剩余额度' })).toHaveTextContent('53%')
  expect(within(list).queryByText('21%')).not.toBeInTheDocument()
  expect(within(list).getAllByRole('listitem')).toHaveLength(2)
  expect(quota).not.toHaveBeenCalled()
  expect(apply).not.toHaveBeenCalled()
  await user.click(within(list).getByRole('button', { name: '切换到 备用' }))
  await waitFor(() => expect(apply).toHaveBeenCalledExactlyOnceWith('dev', 'b', false))
  await waitFor(() => expect(within(list).getByText('备用').closest('li')).toHaveTextContent('当前使用'))
  await user.click(screen.getByRole('button', { name: '刷新额度' }))
  await waitFor(() => expect(within(list).getAllByRole('meter')).toHaveLength(4))
  expect(quota.mock.calls.map(([target, id]) => [target, id])).toEqual([['dev', 'a'], ['dev', 'b']])
})

it('shows the session platform without loading AGY accounts on a Codex session', async () => {
  const user = userEvent.setup()
  const accounts = vi.spyOn(agyAccountsApi, 'accounts')
  preview({ ...agent, kind: 'codex' }, session)
  await user.hover(screen.getByRole('link', { name: '账号' }))
  expect(await screen.findByText('Codex 尚未接入账号管理。')).toBeVisible()
  expect(accounts).not.toHaveBeenCalled()
})
