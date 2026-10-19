import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { SessionConnectionNotice } from './SessionConnectionNotice'

afterEach(cleanup)
beforeEach(() => { vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })
const agent: Agent = { id: 'grok-remote', kind: 'grok', name: '开发机', status: 'disconnected', capabilities: ['chat'], limitation: '已主动断联，请在连接管理中重新接入', connection_id: 'remote' }
const session = { id: 'native-session', agent_id: agent.id, connection_id: 'remote' } as Session
function mount(value = session, retry = vi.fn(), error = 'Agent 已主动断联，请在连接管理中重新接入') {
  useAstrorderStore.setState({ agents: { [agent.id]: agent } })
  return { retry, ...render(<MantineProvider env="test"><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><SessionConnectionNotice session={value} error={error} onRetry={retry} /></QueryClientProvider></MantineProvider>) }
}
it('connects only after a click, targets the exact environment/kind and refreshes after confirmed connection', async () => {
  const connect = vi.spyOn(api, 'changeEnvironmentAgent').mockResolvedValue({})
  vi.spyOn(api, 'getEnvironments').mockResolvedValue({ items: [{ id: 'remote', name: '开发机', method: 'ssh', discovered: true, agents: [{ kind: 'grok', state: 'connected', available: true, detail: '' }] }] })
  vi.spyOn(api, 'getAgents').mockResolvedValue({ items: [{ ...agent, status: 'ready', limitation: null }] })
  const { retry } = mount()
  expect(connect).not.toHaveBeenCalled()
  expect(screen.queryByText(/连接管理/)).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '连接' }))
  await waitFor(() => expect(retry).toHaveBeenCalledOnce())
  expect(connect).toHaveBeenCalledExactlyOnceWith('remote', 'grok', true)
  expect(useAstrorderStore.getState().agents[agent.id].status).toBe('ready')
})
it('normalizes local environment and prevents duplicate clicks while connecting', async () => {
  let finish!: () => void
  const connect = vi.spyOn(api, 'changeEnvironmentAgent').mockImplementation(() => new Promise(resolve => { finish = () => resolve({}) }))
  vi.spyOn(api, 'getEnvironments').mockResolvedValue({ items: [{ id: 'local', name: '本机', method: 'local', discovered: true, agents: [{ kind: 'grok', state: 'connected', available: true, detail: '' }] }] })
  vi.spyOn(api, 'getAgents').mockResolvedValue({ items: [{ ...agent, status: 'ready', connection_id: undefined }] })
  const { retry } = mount({ ...session, connection_id: undefined })
  act(() => useAstrorderStore.setState({ agents: { [agent.id]: { ...agent, connection_id: undefined } } }))
  fireEvent.click(screen.getByRole('button', { name: '连接' }))
  fireEvent.click(screen.getByRole('button', { name: '连接中…' }))
  expect(connect).toHaveBeenCalledExactlyOnceWith('local', 'grok', true)
  finish()
  await waitFor(() => expect(retry).toHaveBeenCalledOnce())
})
it('keeps ordinary resource errors visible with retry instead of offering a connection', () => {
  const connect = vi.spyOn(api, 'changeEnvironmentAgent')
  const { retry } = mount(session, vi.fn(), '历史读取失败')
  act(() => useAstrorderStore.setState({ agents: { [agent.id]: { ...agent, status: 'ready' } } }))
  expect(screen.getByText('暂时无法读取会话')).toBeVisible()
  expect(screen.getByText('历史读取失败')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  expect(retry).toHaveBeenCalledOnce()
  expect(connect).not.toHaveBeenCalled()
})
it('keeps the error and reconnect action when the server has not confirmed readiness', async () => {
  vi.spyOn(api, 'changeEnvironmentAgent').mockResolvedValue({})
  vi.spyOn(api, 'getEnvironments').mockResolvedValue({ items: [{ id: 'remote', name: '开发机', method: 'ssh', discovered: true, agents: [{ kind: 'grok', state: 'error', available: true, detail: 'SSH 连接失败' }] }] })
  const { retry } = mount()
  fireEvent.click(screen.getByRole('button', { name: '连接' }))
  expect(await screen.findByText('SSH 连接失败')).toBeInTheDocument()
  expect(retry).not.toHaveBeenCalled()
  expect(useAstrorderStore.getState().agents[agent.id].status).toBe('disconnected')
  expect(screen.getByRole('button', { name: '连接' })).toBeEnabled()
})
