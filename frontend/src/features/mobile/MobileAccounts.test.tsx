import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { agyAccountsApi } from '../agentAccounts/agyAccountsApi'
import { MobileWorkspace } from './MobileWorkspace'

const clients: QueryClient[] = []

beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function close() {
    this.removeAttribute('open')
  }
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  clients.forEach(client => client.clear())
  clients.length = 0
  vi.restoreAllMocks()
})

describe('Mobile account switching', () => {
  it('opens from app settings, defaults to the active AGY machine, refreshes quotas, and switches accounts cleanly', async () => {
    const wslAgent: Agent = {
      id: 'agy-wsl',
      kind: 'antigravity-ssh',
      name: 'WSL · Antigravity',
      connection_id: 'wsl',
      status: 'ready',
      capabilities: ['chat', 'stop'],
      limitation: null,
    }
    const wslSession: Session = {
      id: 'sess-1',
      agent_id: wslAgent.id,
      connection_id: 'wsl',
      title: '移动端账号测试',
      status: 'idle',
      workspace: '/home/luwei/workspace',
      updated_at: '2026-11-22T12:00:00Z',
    }
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.setState({
      agents: { [wslAgent.id]: wslAgent },
      sessions: { [`${wslAgent.id}::${wslSession.id}`]: wslSession },
      connection: 'connected',
    })

    vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [wslAgent.id], items: [] })
    vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
    vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'antigravity', model: 'gemini-4', label: 'gemini-4' } as never)
    vi.spyOn(api, 'getSessionUsage').mockResolvedValue({ context_available: false, context_window: 0, last_input_tokens: 0 } as never)

    let activeByEnv: Record<string, string> = { local: 'acc-1', wsl: 'acc-1' }
    vi.spyOn(agyAccountsApi, 'targets').mockResolvedValue({
      items: [
        { id: 'local', name: '本机' },
        { id: 'wsl', name: 'WSL' },
      ],
    })
    vi.spyOn(agyAccountsApi, 'accounts').mockResolvedValue({
      items: [
        { id: 'acc-1', name: '主工作号' },
        { id: 'acc-2', name: '备用 Pro' },
      ],
    })
    vi.spyOn(agyAccountsApi, 'targetState').mockImplementation(async env => ({
      current_account_id: activeByEnv[env] ?? '',
      quota_supported: true,
    }))
    const quotaSpy = vi.spyOn(agyAccountsApi, 'quota').mockResolvedValue({
      as_of: '2026-11-22T08:30:00Z',
      plan: 'Google AI Pro',
      groups: [
        {
          name: 'Gemini Models',
          buckets: [
            { id: '5h', name: '5h', window: 'five_hour', remaining_fraction: 0.88 },
            { id: 'week', name: 'Week', window: 'weekly', remaining_fraction: 0.42 },
          ],
        },
      ],
    })
    const switchSpy = vi.spyOn(agyAccountsApi, 'switch').mockImplementation(async (env, id) => {
      activeByEnv = { ...activeByEnv, [env]: id }
      return { account_id: id, restarted: true }
    })

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    clients.push(client)

    render(
      <MantineProvider>
        <QueryClientProvider client={client}>
          <MemoryRouter initialEntries={[`/mobile/chat/${wslSession.id}?agent_id=${wslAgent.id}`]}>
            <MobileWorkspace />
          </MemoryRouter>
        </QueryClientProvider>
      </MantineProvider>,
    )

    fireEvent.click(await screen.findByRole('button', { name: '应用设置' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '账号切换' }))

    const sheet = (await screen.findByRole('dialog', { name: '账号切换' })) as HTMLElement
    expect(sheet).toHaveClass('m-accounts-sheet')

    // Automatically selects the active session's WSL target machine
    const select = within(sheet).getByRole('combobox', { name: '目标 Agent' })
    await waitFor(() => expect(select).toHaveValue('wsl'))

    // Renders mobile card list (no wide desktop table)
    expect(within(sheet).queryByRole('table')).not.toBeInTheDocument()
    const list = await within(sheet).findByRole('list', { name: 'AGY 账号列表' })
    expect(within(list).getByText('主工作号')).toBeVisible()
    expect(within(list).getByText('备用 Pro')).toBeVisible()
    expect(within(list).getByText('主工作号').closest('article')).toHaveTextContent('当前使用')

    // Refresh all quotas and verify tiles render
    fireEvent.click(within(sheet).getByRole('button', { name: '刷新全部' }))
    await waitFor(() => expect(quotaSpy).toHaveBeenCalledWith('wsl', 'acc-1', expect.any(AbortSignal)))
    expect((await within(list).findAllByText('88%')).length).toBeGreaterThan(0)
    expect((await within(list).findAllByText('Pro')).length).toBeGreaterThan(0)

    // Switch to backup account via confirmation dialog
    fireEvent.click(within(list).getByRole('button', { name: '切换到 备用 Pro' }))
    const confirmBtn = await screen.findByRole('button', { name: '确认切换' })
    fireEvent.click(confirmBtn)

    await waitFor(() => expect(switchSpy).toHaveBeenCalledWith('wsl', 'acc-2', false))
    await waitFor(() => expect(within(list).getByText('备用 Pro').closest('article')).toHaveTextContent('当前使用'))

    // Switching machine dropdown switches target cleanly
    fireEvent.change(select, { target: { value: 'local' } })
    await waitFor(() => expect(within(list).getByText('主工作号').closest('article')).toHaveTextContent('当前使用'))
  })
})
