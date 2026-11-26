import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, type UpgradeJob } from '../api/client'
import { BackgroundTasks } from '../components/BackgroundTasks'
import { useBackgroundTasks } from './backgroundTasks'
import { trackUpgradeJob } from './upgradeTasks'

vi.mock('../api/client', () => ({ api: { getUpgradeJobs: vi.fn(), cancelUpgradeJob: vi.fn() } }))
const job: UpgradeJob = { id: 'upgrade-1', title: '批量升级 Agent (2)', status: 'running', detail: '正在升级 Codex', log: 'before refresh\n', created_at: '2026-10-28T01:00:00Z', current: 1, total: 2, target: 'Codex' }
beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(api.getUpgradeJobs).mockResolvedValue({ items: [{ ...job }] })
  vi.mocked(api.cancelUpgradeJob).mockResolvedValue({ cancelled: true })
  useBackgroundTasks.setState({ tasks: {} })
})
afterEach(() => { cleanup(); vi.useRealTimers(); vi.clearAllMocks(); useBackgroundTasks.setState({ tasks: {} }) })

it('restores live jobs and logs after a full store reset, then observes completion', async () => {
  const mount = () => render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  let view = mount()
  await act(async () => {})
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('running')
  view.unmount()
  useBackgroundTasks.setState({ tasks: {} })
  vi.mocked(api.getUpgradeJobs).mockResolvedValue({ items: [{ ...job, log: job.log + 'after refresh\n', current: 2, target: 'Hermes' }] })
  view = mount()
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText(job.title))
  await act(async () => { await vi.advanceTimersByTimeAsync(200) })
  expect(screen.getByLabelText('升级日志内容')).toHaveTextContent('before refresh')
  expect(screen.getByLabelText('升级日志内容')).toHaveTextContent('after refresh')
  vi.mocked(api.getUpgradeJobs).mockResolvedValue({ items: [{ ...job, status: 'completed', detail: '升级结束：成功 2，失败 0', log: 'done' }] })
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('completed')
  act(() => useBackgroundTasks.getState().clearFinished())
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(useBackgroundTasks.getState().tasks).toEqual({})
  view.unmount()
})

it('cancels the recovered server job and waits for its actual cancellation result', async () => {
  render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  await act(async () => {})
  expect(api.cancelUpgradeJob).toHaveBeenCalledWith(job.id)
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('running')
  vi.mocked(api.getUpgradeJobs).mockResolvedValue({ items: [{ ...job, status: 'cancelled', detail: '升级已取消' }] })
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('cancelled')
})

it('shows status read errors and recovers without starting the upgrade again', async () => {
  vi.mocked(api.getUpgradeJobs).mockRejectedValueOnce(new Error('connection lost'))
  render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  expect(screen.getByRole('alert')).toHaveTextContent('connection lost')
  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('running')
})

it('stops polling when no upgrade jobs are running', async () => {
  vi.mocked(api.getUpgradeJobs).mockResolvedValue({ items: [] })
  render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  await act(async () => {})
  expect(api.getUpgradeJobs).toHaveBeenCalledTimes(1)
  await act(async () => { await vi.advanceTimersByTimeAsync(15_000) })
  expect(api.getUpgradeJobs).toHaveBeenCalledTimes(1)
})

it('polls live progress and completion in standalone windows without BackgroundTasks mounted', async () => {
  vi.mocked(api.getUpgradeJobs)
    .mockResolvedValueOnce({ items: [{ ...job, current: 1, total: 2, target: 'Codex', log: '[1/2] Codex\n' }] })
    .mockResolvedValueOnce({ items: [{ ...job, current: 2, total: 2, target: 'Hermes', status: 'completed', detail: '升级结束：成功 2，失败 0', log: '[1/2] Codex\n[2/2] Hermes\n[DONE]\n' }] })

  act(() => {
    trackUpgradeJob({ ...job, current: 0, total: 2, target: '', log: '' })
  })
  expect(useBackgroundTasks.getState().tasks[job.id].log).toBe('')

  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(useBackgroundTasks.getState().tasks[job.id].upgrade).toEqual({ current: 1, total: 2, target: 'Codex' })
  expect(useBackgroundTasks.getState().tasks[job.id].log).toContain('[1/2] Codex')

  await act(async () => { await vi.advanceTimersByTimeAsync(1200) })
  expect(useBackgroundTasks.getState().tasks[job.id].status).toBe('completed')
  expect(useBackgroundTasks.getState().tasks[job.id].detail).toBe('升级结束：成功 2，失败 0')

  const callsAfterComplete = vi.mocked(api.getUpgradeJobs).mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(vi.mocked(api.getUpgradeJobs).mock.calls.length).toBe(callsAfterComplete)
})
