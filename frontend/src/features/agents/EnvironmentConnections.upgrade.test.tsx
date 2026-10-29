import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { EnvironmentConnections } from './EnvironmentConnections'
import { api } from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'
import { trackUpgradeJob } from '../../state/upgradeTasks'
import type { UpgradeJob } from '../../api/client'
import { BackgroundTasks } from '../../components/BackgroundTasks'

vi.mock('../../api/client', () => ({ api: {
  getEnvironments: vi.fn(async () => ({ items: [{ id: 'local', name: '本机', method: 'local', discovered: true, agents: [] }] })),
  getConnections: vi.fn(async () => ({ ssh: { items: [] } })),
  getUpgradeTargets: vi.fn(async () => ({ items: [
    { id: 'codex', name: '本机 Codex', kind: 'codex', connection_id: 'local', current_version: '1.0', latest_version: 'latest', has_update: true },
    { id: 'hermes', name: '本机 Hermes', kind: 'hermes', connection_id: 'local', current_version: '1.0', latest_version: 'latest', has_update: true },
  ] })),
  startUpgradeJob: vi.fn(),
  getUpgradeJobs: vi.fn(async () => ({ items: [] })),
  cancelUpgradeJob: vi.fn(async () => ({ cancelled: true })),
} }))
afterEach(() => { cleanup(); vi.clearAllMocks(); useBackgroundTasks.setState({ tasks: {} }) })

const job: UpgradeJob = { id: 'batch-1', title: '批量升级 Agent (2)', detail: '正在执行版本升级…', status: 'running', log: '', created_at: '2026-10-28T01:00:00Z', current: 1, total: 2, target: '本机 Codex' }

it('submits the whole batch once and displays the server failure and outcome', async () => {
  vi.mocked(api.startUpgradeJob).mockResolvedValueOnce(job)
  render(<MantineProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><EnvironmentConnections /></QueryClientProvider></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '全节点升级' }))
  fireEvent.click(await screen.findByRole('button', { name: '开始升级选中 Agent (2)' }))
  await screen.findByText('正在升级: 本机 Codex')
  expect(api.startUpgradeJob).toHaveBeenCalledExactlyOnceWith(['codex', 'hermes'], '批量升级 Agent (2)')
  act(() => trackUpgradeJob({ ...job, status: 'failed', detail: '升级结束：成功 1，失败 1', log: '本机 Codex 升级出错: npm: permission denied' }))
  expect(await screen.findByText('升级结束：成功 1，失败 1')).toBeInTheDocument()
  expect(screen.getByText(/本机 Codex 升级出错: npm: permission denied/)).toBeInTheDocument()
})

it('keeps live logs after settings unmounts and cancels the server batch', async () => {
  vi.mocked(api.startUpgradeJob).mockResolvedValueOnce({ ...job, log: 'first output\n' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = (settings: boolean) => <MantineProvider><QueryClientProvider client={client}>{settings && <EnvironmentConnections />}<BackgroundTasks /></QueryClientProvider></MantineProvider>
  const rendered = render(view(true))
  fireEvent.click(screen.getByRole('button', { name: '全节点升级' }))
  fireEvent.click(await screen.findByRole('button', { name: '开始升级选中 Agent (2)' }))
  await screen.findByText('正在升级: 本机 Codex')
  rendered.rerender(view(false))
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText(job.title))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('first output')
  act(() => trackUpgradeJob({ ...job, log: 'first output\nafter settings closed\n' }))
  expect(screen.getByLabelText('升级日志内容')).toHaveTextContent('after settings closed')
  fireEvent.click(screen.getByRole('button', { name: '中止升级' }))
  expect(api.cancelUpgradeJob).toHaveBeenCalledWith(job.id)
  await act(async () => {})
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('running')
  act(() => trackUpgradeJob({ ...job, status: 'cancelled', detail: '升级已取消' }))
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('cancelled')
})
