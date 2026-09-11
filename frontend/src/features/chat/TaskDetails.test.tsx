import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Task } from '../../domain/types'
import { TaskDetails } from './TaskDetails'

const task: Task = {
  id: 'task-1',
  session_id: 'session-1',
  agent_id: 'agent-1',
  kind: 'background',
  title: '运行测试',
  status: 'running',
  progress: { completed: 2, total: 5 },
  command: 'pytest -q',
  logs: [{ id: 'log-1', text: '正在运行测试', level: 'info', created_at: '2026-09-07T00:00:00Z' }],
  target_id: 'job-1',
  created_at: '2026-09-07T00:00:00Z',
  updated_at: '2026-09-07T00:01:00Z',
}

describe('TaskDetails', () => {
  beforeEach(() => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it('shows source session, live log and supports copy, jump and return', () => {
    const onJump = vi.fn()
    const onClose = vi.fn()
    render(<MantineProvider><TaskDetails task={task} canStop={false} onJumpToLatest={onJump} onClose={onClose} /></MantineProvider>)

    expect(screen.getByText('会话 session-1')).toBeInTheDocument()
    expect(screen.getByText('正在运行测试')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '复制命令' }))
    fireEvent.click(screen.getByRole('button', { name: '跳到最新' }))
    fireEvent.click(screen.getByRole('button', { name: '返回' }))
    expect(onJump).toHaveBeenCalledOnce()
    expect(onClose).toHaveBeenCalledOnce()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('pytest -q')
  })

  it('requires explicit UI confirmation before stopping a supported task', async () => {
    const onStop = vi.fn()
    render(<MantineProvider><TaskDetails task={task} canStop onStop={onStop} onJumpToLatest={vi.fn()} onClose={vi.fn()} /></MantineProvider>)

    fireEvent.click(screen.getByRole('button', { name: '停止任务' }), { clientX: 420, clientY: 180 })
    const confirmation = await screen.findByRole('dialog', { name: '停止任务？' })
    expect(window.confirm).not.toHaveBeenCalled()
    expect(onStop).not.toHaveBeenCalled()
    expect(confirmation).toHaveStyle({ left: '428px', top: '188px' })

    fireEvent.click(within(confirmation).getByRole('button', { name: '停止任务' }))
    expect(onStop).toHaveBeenCalledWith(task)
  })
})
