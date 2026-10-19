import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { MobileWorkspace } from './MobileWorkspace'

const clients: QueryClient[] = []
afterEach(() => { cleanup(); clients.forEach(client => client.clear()); clients.length = 0; vi.restoreAllMocks() })

it('prioritizes agents, switches hosts and keeps mobile RTK unmounted', async () => {
  useAstrorderStore.setState({ sessions: {}, agents: {}, connection: 'connected' })
  vi.spyOn(api, 'getOpenSessions').mockResolvedValue({ known_agent_ids: [], items: [] })
  vi.spyOn(api, 'getConnections').mockResolvedValue({ ssh: { items: [] } } as unknown as Awaited<ReturnType<typeof api.getConnections>>)
  vi.spyOn(api, 'getEnvironments').mockResolvedValue({ items: [
    { id: 'local', name: '本机', method: 'local', discovered: true, agents: [
      { kind: 'hermes', state: 'connected', available: true, detail: '就绪', executable: 'C:/Hermes/hermes.exe' },
      { kind: 'codex', state: 'error', available: true, detail: '需要重新登录', executable: 'C:/Codex/codex.exe' },
    ] },
    { id: 'remote', name: '远程主机', method: 'ssh', discovered: true, agents: [
      { kind: 'grok', state: 'discovered', available: true, detail: '已发现', executable: '/usr/bin/grok' },
    ] },
  ] })
  const rtk = vi.spyOn(api, 'getRtk')
  const connect = vi.spyOn(api, 'changeEnvironmentAgent').mockResolvedValue({})
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  render(<MantineProvider><QueryClientProvider client={client}><MemoryRouter initialEntries={['/mobile']}><MobileWorkspace /></MemoryRouter></QueryClientProvider></MantineProvider>)
  fireEvent.click(await screen.findByRole('button', { name: '应用设置' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '连接管理' }))
  const heading = await screen.findByRole('heading', { name: '连接管理' })
  const sheet = heading.closest('section')!
  expect(sheet).toHaveClass('m-connections-sheet')
  expect(await within(sheet).findByText('Hermes')).toBeVisible()
  expect(within(sheet).getByText('需要重新登录')).toBeVisible()
  expect(within(sheet).queryByText('RTK 命令行压缩')).not.toBeInTheDocument()
  expect(rtk).not.toHaveBeenCalled()
  expect(within(sheet).queryByRole('navigation', { name: '运行环境' })).not.toBeInTheDocument()
  fireEvent.change(within(sheet).getByRole('combobox', { name: '运行环境' }), { target: { value: 'remote' } })
  expect(within(sheet).getByText('Grok Build')).toBeVisible()
  expect(within(sheet).queryByText('Hermes')).not.toBeInTheDocument()
  fireEvent.click(within(sheet).getByRole('button', { name: '接入' }))
  expect(connect).toHaveBeenCalledWith('remote', 'grok', true)
  await waitFor(() => expect(within(sheet).getByRole('button', { name: '环境操作' })).toBeEnabled())
})
