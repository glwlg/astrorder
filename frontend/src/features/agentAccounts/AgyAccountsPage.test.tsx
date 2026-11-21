import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, it, vi } from 'vitest'
import { AgyAccountsPage } from './AgyAccountsPage'
import { agyAccountsApi } from './agyAccountsApi'

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
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
  render(<MantineProvider><QueryClientProvider client={client}><AgyAccountsPage /></QueryClientProvider></MantineProvider>)
  const active = within(await screen.findByRole('row', { name: /工作账号/ }))
  const spare = within(await screen.findByRole('row', { name: /备用账号/ }))
  const expired = within(await screen.findByRole('row', { name: /测试账号/ }))
  expect(active.getByText('尚未刷新')).toBeVisible()
  expect(agyAccountsApi.targetState).not.toHaveBeenCalled()
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
  render(<MantineProvider><QueryClientProvider client={new QueryClient()}><AgyAccountsPage /></QueryClientProvider></MantineProvider>)
  expect(await screen.findByText('Google AI Pro')).toBeVisible()
  expect(agyAccountsApi.quota).toHaveBeenCalledTimes(4)
})
