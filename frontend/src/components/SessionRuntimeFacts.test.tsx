import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Session } from '../domain/types'
import { GitStatusBar } from '../features/chat/GitStatusBar'
import { SessionRuntimeFacts } from './SessionRuntimeFacts'

const session: Session = {
  id: 'session-1', agent_id: 'codex-wsl', connection_id: 'wsl-connection',
  title: 'Session', workspace: '/home/user/project', status: 'running',
  updated_at: '2026-11-04T00:00:00Z',
}

function mockRuntime() {
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'magpie', model: 'codex/model', branch: 'old-native-branch' })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ local: {}, ssh: { items: [] } } as unknown as Awaited<ReturnType<typeof api.getConnections>>)
  vi.spyOn(api, 'getSessionUsage').mockRejectedValue(new Error('unavailable'))
}

function renderFacts(value: Session = session, withComposer = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MantineProvider env="test"><QueryClientProvider client={client}>
      {withComposer && <GitStatusBar session={value} />}
      <SessionRuntimeFacts session={value} />
    </QueryClientProvider></MantineProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('shares the exact workspace Git status with the composer and refreshes both after a branch change', async () => {
  mockRuntime()
  vi.spyOn(api, 'getModelQuota').mockResolvedValue({ model: 'model', providers: [], reports: [], accounts: [] })
  let branch = 'master'
  const fetcher = vi.fn(async (_input: unknown, init?: RequestInit) => {
    if (init?.method === 'POST') branch = JSON.parse(String(init.body)).branch
    return new Response(JSON.stringify({ branch, branches: ['master', 'review'], changed_files: 0, insertions: 0, deletions: 0 }), { status: 200 })
  })
  vi.stubGlobal('fetch', fetcher)
  const user = userEvent.setup()
  renderFacts(session, true)

  const facts = screen.getByLabelText('运行信息')
  expect(await within(facts).findByText('master')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toHaveTextContent('master')
  expect(within(facts).queryByText('old-native-branch')).not.toBeInTheDocument()
  const initialReads = fetcher.mock.calls.filter(([input]) => String(input).startsWith('/api/v1/git/status?'))
  expect(initialReads).toHaveLength(1)
  expect(String(initialReads[0][0])).toContain('session_id=session-1')
  expect(String(initialReads[0][0])).toContain('connection_id=wsl-connection')

  await user.click(screen.getByRole('button', { name: '切换或创建 Git 分支' }))
  await user.click(await screen.findByRole('menuitem', { name: 'review' }))
  await waitFor(() => expect(within(facts).getByText('review')).toBeInTheDocument())
  expect(screen.getByRole('button', { name: '切换或创建 Git 分支' })).toHaveTextContent('review')
})

it('shows all returned quota windows, the gateway last-used account, and unknown values honestly', async () => {
  mockRuntime()
  vi.spyOn(api, 'getModelQuota').mockResolvedValue({
    model: 'model', providers: ['codex'], reports: [],
    accounts: [
      { id: 'first', email: 'first@example.test', plan: 'plus', readAt: '2026-10-16T00:45:00Z', quota: {
        shortPercent: 0, customWindows: [{ label: '5 hours', percent: 0 }, { label: '7 days', percent: 7 }],
      } },
      { id: 'second', email: 'second@example.test', plan: 'pro', last: true, asOf: '2026-10-16T00:30:00Z', quota: {
        weeklyPercent: 89, customWindows: [{ label: '7 days', percent: 89 }, { label: '模型专用', percent: 35 }],
      } },
      { id: 'pending', email: 'pending@example.test', readDeferred: true, quota: null },
    ],
  })
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
    branch: 'master', branches: ['master'], changed_files: 0, insertions: 0, deletions: 0,
  }), { status: 200 })))
  renderFacts()

  const first = (await screen.findByText(/first@example\.test/)).closest('.quota-group') as HTMLElement
  const second = screen.getByText(/second@example\.test/).closest('.quota-group') as HTMLElement
  const pending = screen.getByText(/pending@example\.test/).closest('.quota-group') as HTMLElement
  expect(within(first).getByText('5 小时')).toBeInTheDocument()
  expect(within(first).getByText('每周')).toBeInTheDocument()
  expect(within(first).getByText('已用 7%')).toBeInTheDocument()
  expect(within(first).getByText(/^读取于 /)).toBeInTheDocument()
  expect(within(second).getByText('网关最近使用')).toBeInTheDocument()
  expect(within(second).getAllByText('每周')).toHaveLength(1)
  expect(within(second).getByText('已用 89%')).toBeInTheDocument()
  expect(within(second).getByText('模型专用')).toBeInTheDocument()
  expect(within(second).getByText(/^缓存读数 · /)).toBeInTheDocument()
  expect(within(pending).getByText('本次未刷新')).toBeInTheDocument()
  expect(within(pending).queryByText('已用 0%')).not.toBeInTheDocument()
})

it('reports an unavailable branch instead of displaying a stale native branch', async () => {
  mockRuntime()
  vi.spyOn(api, 'getModelQuota').mockResolvedValue({ model: 'model', providers: [], reports: [], accounts: [] })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 422 })))
  renderFacts()

  const facts = screen.getByLabelText('运行信息')
  expect(await within(facts).findByText('分支不可用')).toBeInTheDocument()
  expect(within(facts).queryByText('old-native-branch')).not.toBeInTheDocument()
})

it('keeps the native branch when the session has no workspace', async () => {
  mockRuntime()
  vi.spyOn(api, 'getModelQuota').mockResolvedValue({ model: 'model', providers: [], reports: [], accounts: [] })
  const fetcher = vi.fn()
  vi.stubGlobal('fetch', fetcher)
  renderFacts({ ...session, workspace: null })

  expect(await within(screen.getByLabelText('运行信息')).findByText('old-native-branch')).toBeInTheDocument()
  expect(fetcher).not.toHaveBeenCalled()
})

it('keeps fractional readings compact and shares the header activity status', async () => {
  mockRuntime()
  vi.mocked(api.getSessionUsage).mockResolvedValue({
    ok: true, session_id: session.id, model: 'model', context_window: 243000,
    last_input_tokens: 103300, used_percentage: 42.51028806584362,
    total_tokens: 400000, input_tokens: 380000, output_tokens: 20000,
    cached_tokens: 368600, reasoning_tokens: 0, cache_hit_rate: 97.00033962186885,
  })
  vi.spyOn(api, 'getModelQuota').mockResolvedValue({ model: 'model', providers: [], reports: [], accounts: [] })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })))
  renderFacts({ ...session, status: 'error' })

  expect(await screen.findByText(/103.3k \/ 243k \(42.5%\)/)).toBeInTheDocument()
  expect(screen.getByText(/缓存命中率 97.0%/)).toBeInTheDocument()
  expect(screen.getByText('空闲')).toBeInTheDocument()
  expect(screen.queryByText('异常')).not.toBeInTheDocument()
})
