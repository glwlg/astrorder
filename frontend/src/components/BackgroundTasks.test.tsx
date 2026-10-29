import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useBackgroundTasks } from '../state/backgroundTasks'
import { BackgroundTasks } from './BackgroundTasks'
vi.mock('../state/upgradeTasks', () => ({ useUpgradeTaskRecovery: () => '' }))

afterEach(() => {
  cleanup()
  for (const id of Object.keys(useBackgroundTasks.getState().tasks)) useBackgroundTasks.getState().dismiss(id)
})

it('shows active background tasks without causing a render loop', () => {
  const cancel = vi.fn()
  useBackgroundTasks.getState().add({ title: '转交会话', detail: '正在生成摘要', cancel })
  render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  expect(screen.getByRole('dialog', { name: '后台任务' })).toHaveTextContent('正在生成摘要')
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  expect(cancel).toHaveBeenCalledOnce()
  expect(screen.getByRole('dialog', { name: '后台任务' })).toHaveTextContent('已取消')
})

it('keeps logs and cancellation scoped to their own task', async () => {
  const firstCancel = vi.fn(), secondCancel = vi.fn()
  const first = useBackgroundTasks.getState().add({ title: '旧升级', detail: '已结束', log: 'old output', cancel: firstCancel })
  useBackgroundTasks.getState().complete(first, '已结束')
  useBackgroundTasks.getState().add({ title: '新升级', detail: '运行中', log: 'new output', cancel: secondCancel })
  render(<MantineProvider><BackgroundTasks /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText('旧升级'))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('old output')
  expect(screen.getByLabelText('升级日志内容')).not.toHaveTextContent('new output')
  fireEvent.click(screen.getByRole('button', { name: /^关闭$/ }))
  fireEvent.click(screen.getByRole('button', { name: '后台任务' }))
  fireEvent.click(screen.getByText('新升级'))
  expect(await screen.findByLabelText('升级日志内容')).toHaveTextContent('new output')
  fireEvent.click(screen.getByRole('button', { name: '中止升级' }))
  expect(secondCancel).toHaveBeenCalledOnce()
  expect(firstCancel).not.toHaveBeenCalled()
  act(() => useBackgroundTasks.getState().clearFinished())
  expect(Object.values(useBackgroundTasks.getState().tasks)).toHaveLength(0)
})
