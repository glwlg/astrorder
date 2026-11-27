import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { AgyQuotaRefreshProvider, useAccountPageRefreshTarget } from './AgyQuotaRefreshProvider'
import { agyAccountsApi, codexAccountsApi, type AccountPlatform } from './agyAccountsApi'
import { resetUserIdleForTest } from '../../hooks/useUserIdle'

function AccountsPageTarget({ id, platform = 'antigravity' }: { id: string; platform?: AccountPlatform }) {
  useAccountPageRefreshTarget(id, false, platform)
  return null
}

afterEach(() => {
  cleanup()
  resetUserIdleForTest()
  localStorage.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

it('suppresses scheduled Antigravity quota refresh even when auto-refresh settings exist in localStorage', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  localStorage.setItem('astrorder:account-refresh:antigravity:v1', JSON.stringify({ all: 1, current: 1 }))
  const stateSpy = vi.spyOn(agyAccountsApi, 'targetState').mockResolvedValue({ current_account_id: 'dev-user', quota_supported: false, quota_paused: true })
  const quotaSpy = vi.spyOn(agyAccountsApi, 'quota').mockResolvedValue({ as_of: '2026-11-27T00:00:00Z', groups: [] })
  const client = new QueryClient()
  render(<QueryClientProvider client={client}>
    <AgyQuotaRefreshProvider sessionEnvironment="dev" sessionPlatform="antigravity">
      <AccountsPageTarget id="dev" platform="antigravity" />
    </AgyQuotaRefreshProvider>
  </QueryClientProvider>)

  await act(() => vi.advanceTimersByTimeAsync(15 * 60_000))
  expect(stateSpy).not.toHaveBeenCalled()
  expect(quotaSpy).not.toHaveBeenCalled()
})

it('refreshes only the active Codex account across chat and the account page, with the hovercard closed', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  localStorage.setItem('astrorder:account-refresh:codex:v1', JSON.stringify({ all: 0, current: 5 }))
  const library = vi.spyOn(codexAccountsApi, 'accounts')
  vi.spyOn(codexAccountsApi, 'targetState').mockImplementation(async env => ({ current_account_id: `${env}-user`, quota_supported: true }))
  const quota = vi.spyOn(codexAccountsApi, 'quota').mockResolvedValue({ as_of: '2026-11-21T04:00:00Z', groups: [] })
  const client = new QueryClient()
  const view = (session: string, page?: string) => <QueryClientProvider client={client}>
    <AgyQuotaRefreshProvider sessionEnvironment={session} sessionPlatform="codex">
      {page && <AccountsPageTarget id={page} platform="codex" />}
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
  expect(localStorage.getItem('astrorder:account-quota:v1:codex:dev-user')).toContain('as_of')
  ui.rerender(view('wsl'))
  await advance(5)
  expect(quota.mock.calls.map(([env, id]) => [env, id])).toEqual([['dev', 'dev-user'], ['wsl', 'wsl-user']])
  expect(library).not.toHaveBeenCalled()
})

it('pauses scheduled Codex quota refresh after overnight user inactivity and resumes on return', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  localStorage.setItem('astrorder:account-refresh:codex:v1', JSON.stringify({ all: 0, current: 5 }))
  vi.spyOn(codexAccountsApi, 'targetState').mockImplementation(async env => ({ current_account_id: `${env}-user`, quota_supported: true }))
  const quota = vi.spyOn(codexAccountsApi, 'quota').mockResolvedValue({ as_of: '2026-11-25T00:00:00Z', groups: [] })
  const client = new QueryClient()
  render(<QueryClientProvider client={client}>
    <AgyQuotaRefreshProvider sessionEnvironment="dev" sessionPlatform="codex">{null}</AgyQuotaRefreshProvider>
  </QueryClientProvider>)
  const advance = (minutes: number) => act(() => vi.advanceTimersByTimeAsync(minutes * 60_000))

  // While within the 15-minute active window, 5m and 10m refreshes fire normally.
  await advance(14)
  expect(quota).toHaveBeenCalledTimes(2)

  // Past 15 minutes with no interaction (e.g. overnight unattended desktop), refreshes pause completely.
  await advance(60 * 8)
  expect(quota).toHaveBeenCalledTimes(2)

  // When the user returns and interacts, one catch-up refresh runs cleanly.
  await act(async () => {
    window.dispatchEvent(new Event('pointerdown'))
    await vi.advanceTimersByTimeAsync(0)
  })
  expect(quota).toHaveBeenCalledTimes(3)
})
