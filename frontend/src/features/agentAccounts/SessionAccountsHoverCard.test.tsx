import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Agent, Session } from '../../domain/types'
import { SessionAccountsHoverCard } from './SessionAccountsHoverCard'
import { agyAccountsApi, codexAccountsApi, grokAccountsApi } from './agyAccountsApi'

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
  const key = 'astrorder:agy-quota:v2:a'
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

it('shows the session platform without loading accounts on an unsupported Hermes session', async () => {
  const user = userEvent.setup()
  const accounts = vi.spyOn(agyAccountsApi, 'accounts')
  preview({ ...agent, kind: 'hermes' }, session)
  await user.hover(screen.getByRole('link', { name: '账号' }))
  expect(await screen.findByText('Hermes 尚未接入账号管理。')).toBeVisible()
  expect(accounts).not.toHaveBeenCalled()
})

it('loads Codex accounts, quota with reset times, and switches via codexAccountsApi on a Codex session', async () => {
  const user = userEvent.setup()
  const agySpy = vi.spyOn(agyAccountsApi, 'accounts')
  const resetMs = Date.parse('2026-11-26T08:00:00Z')
  let active = 'c1'
  vi.spyOn(codexAccountsApi, 'accounts').mockResolvedValue({
    items: [
      { id: 'c1', name: 'Codex 主力', email: 'main@example.com' },
      { id: 'c2', name: 'Codex 备用' },
    ],
  })
  vi.spyOn(codexAccountsApi, 'targetState').mockImplementation(async () => ({ current_account_id: active, quota_supported: true }))
  vi.spyOn(codexAccountsApi, 'quota').mockResolvedValue({
    as_of: '2026-11-26T02:00:00Z',
    plan: 'ChatGPT Plus',
    groups: [{ name: 'Codex', buckets: [{ id: '5h', name: '5 小时', window: '5h', remaining_fraction: 0.91, reset_at: resetMs }] }],
  })
  const switchSpy = vi.spyOn(codexAccountsApi, 'switch').mockImplementation(async (_, id) => {
    active = id
    return { account_id: id, restarted: true }
  })

  preview({ ...agent, id: 'dev-codex', kind: 'codex-ssh', name: 'Codex · 开发机' }, { ...session, agent_id: 'dev-codex' })
  await user.hover(screen.getByRole('link', { name: '账号' }))
  expect(await screen.findByText('Codex 额度')).toBeVisible()
  expect(agySpy).not.toHaveBeenCalled()

  await user.click(screen.getByRole('button', { name: '刷新额度' }))
  const list = await screen.findByRole('list', { name: '当前平台的账号' })
  expect((await within(list).findAllByText('91%')).length).toBeGreaterThan(0)
  expect((await within(list).findAllByText('Plus')).length).toBeGreaterThan(0)
  expect((await within(list).findAllByText(/重置$/)).length).toBeGreaterThan(0)

  await user.click(within(list).getByRole('button', { name: '切换到 Codex 备用' }))
  await waitFor(() => expect(switchSpy).toHaveBeenCalledWith('dev', 'c2', false))
})

it('loads Grok accounts and subscription summary via grokAccountsApi on a Grok session', async () => {
  const user = userEvent.setup()
  const agySpy = vi.spyOn(agyAccountsApi, 'accounts')
  vi.spyOn(grokAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'g1', name: 'Grok 账号' }] })
  vi.spyOn(grokAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'g1', quota_supported: true })
  vi.spyOn(grokAccountsApi, 'quota').mockResolvedValue({
    as_of: '2026-11-26T02:00:00Z',
    plan: 'SuperGrok',
    detail: '已认证（grok@x.ai）',
    groups: [],
  })

  preview({ ...agent, id: 'local-grok', kind: 'grok', name: 'Grok', connection_id: 'local' }, { ...session, agent_id: 'local-grok', connection_id: 'local' })
  await user.hover(screen.getByRole('link', { name: '账号' }))
  expect(await screen.findByText('Grok 额度')).toBeVisible()
  expect(agySpy).not.toHaveBeenCalled()

  await user.click(screen.getByRole('button', { name: '刷新额度' }))
  expect(await screen.findByText('SuperGrok')).toBeVisible()
  expect(await screen.findByText('已认证（grok@x.ai）')).toBeVisible()
})

it('displays reset times next to each quota period row when reset_at is present', async () => {
  const user = userEvent.setup()
  const fiveHourReset = Date.parse('2026-11-26T06:30:00Z')
  const weeklyGeminiReset = Date.parse('2026-12-01T00:00:00Z')
  const weeklyClaudeReset = Date.parse('2026-12-02T00:00:00Z')
  const snapshot = {
    as_of: '2026-11-26T01:30:00Z',
    groups: [
      {
        name: 'Gemini Models',
        buckets: [
          { id: '5h', name: '5h', window: 'five_hour', remaining_fraction: 0.977, reset_at: fiveHourReset },
          { id: 'weekly', name: 'Week', window: 'weekly', remaining_fraction: 0.617, reset_at: weeklyGeminiReset },
        ],
      },
      {
        name: 'Claude and GPT models',
        buckets: [
          { id: '5h', name: '5h', window: 'five_hour', remaining_fraction: 1 },
          { id: 'weekly', name: 'Week', window: 'weekly', remaining_fraction: 1, reset_at: weeklyClaudeReset },
        ],
      },
    ],
  }
  vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({ items: [{ id: 'u', name: 'guomina1995' }] })
  vi.spyOn(agyAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'u', quota_supported: true })
  localStorage.setItem('astrorder:agy-quota:v2:u', JSON.stringify(snapshot))

  preview(agent, session)
  await user.hover(screen.getByRole('link', { name: '账号' }))
  const list = await screen.findByRole('list', { name: '当前平台的账号' })
  const shortRow = within(await within(list).findByRole('group', { name: '5 小时额度' }))
  const weeklyRow = within(within(list).getByRole('group', { name: '每周额度' }))

  const shortReset = shortRow.getByText(/重置$/)
  expect(shortReset).toHaveAttribute('datetime', new Date(fiveHourReset).toISOString())

  // Weekly row has different resets for Gemini (61.7%) and Claude/GPT (100%): prioritizes the consumed Gemini reset and keeps both in tooltip
  const weeklyReset = weeklyRow.getByText(/重置$/)
  expect(weeklyReset).toHaveAttribute('datetime', new Date(weeklyGeminiReset).toISOString())
  expect(weeklyReset.getAttribute('title')).toContain('Gemini')
  expect(weeklyReset.getAttribute('title')).toContain('Claude / GPT')
})
