import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useBackgroundTasks } from '../state/backgroundTasks'
import { BackgroundTasks } from './BackgroundTasks'

afterEach(() => {
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
