import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { EnvironmentConnections } from './EnvironmentConnections'
import { api } from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'
import { BackgroundTasks } from '../../components/BackgroundTasks'

vi.mock('../../api/client', () => ({ api: {
  getEnvironments: vi.fn(async () => ({ items: [{ id: 'local', name: '本机', method: 'local', discovered: true, agents: [] }] })),
  getConnections: vi.fn(async () => ({ ssh: { items: [] } })),
  getUpgradeTargets: vi.fn(async () => ({ items: [
    { id: 'codex', name: '本机 Codex', kind: 'codex', connection_id: 'local', current_version: '1.0', latest_version: 'latest', has_update: true },
    { id: 'hermes', name: '本机 Hermes', kind: 'hermes', connection_id: 'local', current_version: '1.0', latest_version: 'latest', has_update: true },
  ] })),
  upgradeAgentStream: vi.fn(),
} }))
afterEach(() => { cleanup(); vi.clearAllMocks(); useBackgroundTasks.setState({ tasks: {} }) })

it('continues after a failed target and retains its error and batch outcome', async () => {
  vi.mocked(api.upgradeAgentStream).mockRejectedValueOnce(new Error('npm: permission denied')).mockResolvedValueOnce({ ok: true, exit_code: 0 })
  render(<MantineProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><EnvironmentConnections /></QueryClientProvider></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '全节点升级' }))
  fireEvent.click(await screen.findByRole('button', { name: '开始升级选中 Agent (2)' }))
  expect(await screen.findByText('升级结束：成功 1，失败 1')).toBeInTheDocument()
  expect(screen.getByText(/本机 Codex 升级出错: npm: permission denied/)).toBeInTheDocument()
  expect(api.upgradeAgentStream).toHaveBeenCalledTimes(2)
  expect(Object.values(useBackgroundTasks.getState().tasks)[0].status).toBe('failed')
})

it('restores live logs after settings unmounts and keeps separate task histories', async () => {
  let chunk: ((value: string) => void) | undefined
  let finish: ((value: { ok: boolean; exit_code: number }) => void) | undefined
  vi.mocked(api.upgradeAgentStream).mockImplementation((_id, onChunk) => {
    chunk = onChunk
    return new Promise(resolve => { finish = resolve })
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = (settings: boolean) => <MantineProvider><QueryClientProvider client={client}>{settings && <EnvironmentConnections />}<BackgroundTasks /></QueryClientProvider></MantineProvider>
  const rendered = render(view(true))
  fireEvent.click(screen.getByRole('button', { name: '全节点升级' }))
  fireEvent.click(await screen.findByRole('button', { name: '开始升级选中 Agent (2)' }))
  act(() => chunk?.('first output\n'))
  rendered.rerender(view(false))
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText('批量升级 Agent (2)'))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('first output')
  expect(screen.queryByRole('dialog', { name: '后台任务' })).not.toBeInTheDocument()
  act(() => chunk?.('after settings closed\n'))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('after settings closed')
  fireEvent.click(screen.getByRole('button', { name: '放入后台运行' }))
  expect(Object.values(useBackgroundTasks.getState().tasks)[0].status).toBe('running')
  await act(async () => finish?.({ ok: true, exit_code: 0 }))
  await act(async () => finish?.({ ok: true, exit_code: 0 }))
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText('批量升级 Agent (2)'))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('升级成功')
  expect(Object.values(useBackgroundTasks.getState().tasks)[0].status).toBe('completed')
})
