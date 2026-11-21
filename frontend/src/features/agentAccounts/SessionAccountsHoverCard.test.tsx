import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
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

it.each(['state', 'quota'])('finishes a manual refresh when the card closes during the %s read', async phase => {
  const user = userEvent.setup()
  const old = { as_of: '2026-11-21T04:00:00Z', groups: [{ name: 'Gemini', buckets: [
    { id: 'w', name: 'Week', window: 'weekly', remaining_fraction: .011 },
  ] }] }
  const fresh = { ...old, as_of: '2026-11-21T14:18:00Z', groups: [{ ...old.groups[0],
    buckets: [{ ...old.groups[0].buckets[0], remaining_fraction: 1 }] }] }
  const key = 'astrorder:agy-quota:v1:dev:a'
  localStorage.setItem(key, JSON.stringify(old))
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'a', name: '工作账号' }] })
  let finish!: () => void
  const gate = new Promise<void>(resolve => { finish = resolve })
  const state = { current_account_id: 'a', quota_supported: true }
  const stateRead = vi.spyOn(agyAccountsApi, 'targetState').mockResolvedValueOnce(state)
    .mockImplementation(async () => { if (phase === 'state') await gate; return state })
  const quotaRead = vi.spyOn(agyAccountsApi, 'quota').mockImplementation(async () => {
    if (phase === 'quota') await gate
    return fresh
  })

  preview(agent, session)
  const link = screen.getByRole('link', { name: '账号' })
  await user.hover(link)
  await screen.findByText('1.1%')
  await user.click(screen.getByRole('button', { name: '刷新额度' }))
  await waitFor(() => phase === 'state' ? expect(stateRead).toHaveBeenCalledTimes(2) : expect(quotaRead).toHaveBeenCalledTimes(1))
  await user.hover(document.body)
  await waitFor(() => expect(screen.queryByRole('list', { name: '当前平台的账号' })).not.toBeInTheDocument())
  await act(async () => { finish(); await gate })
  await waitFor(() => expect(JSON.parse(localStorage.getItem(key)!)).toEqual(fresh))
  await user.hover(link)
  expect(await screen.findByText('100%')).toBeVisible()
  expect(quotaRead).toHaveBeenCalledTimes(1)
})

it('loads on hover, uses target snapshots and switches only the selected machine without forcing', async () => {
  const user = userEvent.setup()
  let active = 'a'
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'a', name: '主账号' }, { id: 'b', name: '备用' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async () => ({ current_account_id: active, quota_supported: true }))
  const quota = vi.spyOn(agyAccountsApi, 'quota')
  const apply = vi.spyOn(agyAccountsApi, 'switch').mockImplementation(async (_, id) => { active = id; return { account_id: id, restarted: true } })
  const snapshot = { as_of: '2026-11-21T04:00:00Z', groups: [
    { name: 'Gemini Models', buckets: [
      { id: 'weekly', name: 'Week', window: 'weekly', remaining_fraction: 0 },
      { id: '5h', name: '', window: 'five_hour', remaining_fraction: 0.91 },
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
  const short = within(within(list).getByRole('group', { name: '5 小时额度' }))
  const weekly = within(within(list).getByRole('group', { name: '每周额度' }))
  expect(short.getAllByRole('meter')).toHaveLength(1)
  expect(short.getByRole('meter', { name: 'Gemini Models · 5 小时剩余额度' })).toHaveTextContent('91%')
  expect(weekly.getByRole('meter', { name: 'Gemini Models · 每周剩余额度' })).toHaveTextContent('0%')
  expect(weekly.getByRole('meter', { name: 'Gemini 4 Argon · 每周剩余额度' })).toHaveTextContent('53%')
  expect(within(list).getAllByRole('listitem')).toHaveLength(2)
  expect(quota).not.toHaveBeenCalled()
  expect(apply).not.toHaveBeenCalled()
  await user.click(within(list).getByRole('button', { name: '切换到 备用' }))
  await waitFor(() => expect(apply).toHaveBeenCalledExactlyOnceWith('dev', 'b', false))
  await waitFor(() => expect(within(list).getByText('备用').closest('li')).toHaveTextContent('当前使用'))
  await user.click(screen.getByRole('button', { name: '刷新额度' }))
  await waitFor(() => expect(within(list).getAllByRole('meter')).toHaveLength(6))
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
