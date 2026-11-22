import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AgyQuotaRefreshProvider, useAccountPageRefreshTarget } from './AgyQuotaRefreshProvider'
import { agyAccountsApi } from './agyAccountsApi'

function AccountsPageTarget({ id }: { id: string }) {
  useAccountPageRefreshTarget(id, false)
  return null
}

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

it('refreshes only the active account across chat and the account page, with the hovercard closed', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  localStorage.setItem('astrorder:account-refresh:antigravity:v1', JSON.stringify({ all: 0, current: 5 }))
  const library = vi.spyOn(agyAccountsApi, 'accounts')
  vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async env => ({ current_account_id: `${env}-user`, quota_supported: true }))
  const quota = vi.spyOn(agyAccountsApi, 'quota').mockResolvedValue({ as_of: '2026-11-21T04:00:00Z', groups: [] })
  const client = new QueryClient()
  const view = (session: string, page?: string) => <QueryClientProvider client={client}>
    <AgyQuotaRefreshProvider sessionEnvironment={session}>
      {page && <AccountsPageTarget id={page} />}
    </AgyQuotaRefreshProvider>
  </QueryClientProvider>
  const ui = render(view('dev'))
  const advance = (minutes: number) => act(() => vi.advanceTimersByTimeAsync(minutes * 60_000))
  await advance(4)
  ui.rerender(view(''))
  await advance(3)
  expect(quota).not.toHaveBeenCalled()
  ui.rerender(view('', 'dev'))
  await advance(0)
  expect(quota.mock.calls.map(([env, id]) => [env, id])).toEqual([['dev', 'dev-user']])
  expect(localStorage.getItem('astrorder:agy-quota:v2:dev-user')).toContain('as_of')
  ui.rerender(view('wsl'))
  await advance(5)
  expect(quota.mock.calls.map(([env, id]) => [env, id])).toEqual([['dev', 'dev-user'], ['wsl', 'wsl-user']])
  expect(library).not.toHaveBeenCalled()
})
