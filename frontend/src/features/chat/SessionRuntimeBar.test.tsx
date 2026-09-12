import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Command, Task } from '../../domain/types'
import { SessionRuntimeBar } from './SessionRuntimeBar'
afterEach(cleanup)

const baseCommand: Command = {
  id: 'command-1',
  session_id: 'session-1',
  agent_id: 'agent-1',
  action: 'enqueue',
  state: 'queued',
  text: '运行测试',
  attachments: [],
  created_at: '2026-09-07T10:01:00Z',
  error: null,
  target_id: null,
}

const baseTask: Task = {
  id: 'task-1',
  session_id: 'session-1',
  agent_id: 'agent-1',
  kind: 'background',
  title: '运行测试',
  status: 'running',
  progress: { completed: 2, total: 5 },
  command: 'uv run pytest -q',
  logs: [{ id: 'log-1', text: '正在运行测试', level: 'info', created_at: '2026-09-07T10:01:00Z' }],
  target_id: 'task-target-1',
  created_at: '2026-09-07T10:00:00Z',
  updated_at: '2026-09-07T10:01:00Z',
}

describe('SessionRuntimeBar', () => {
  it('hides an empty bar and keeps the queue without duplicate Git metadata', () => {
    const { rerender } = render(<MantineProvider><SessionRuntimeBar commands={[]} /></MantineProvider>)
    expect(screen.queryByLabelText('运行摘要')).not.toBeInTheDocument()
    rerender(<MantineProvider><SessionRuntimeBar commands={[baseCommand]} /></MantineProvider>)
    expect(screen.getByText('1 个排队')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '展开运行详情' })).not.toBeInTheDocument()
    expect(screen.queryByText('分支')).not.toBeInTheDocument()
    expect(screen.queryByText('改动')).not.toBeInTheDocument()
  })

  it('only displays running background tasks and removes finished tasks', () => {
    const onTaskOpen = vi.fn()
    const inactiveTasks: Task[] = (['pending', 'waiting_approval', 'completed', 'failed', 'cancelled', 'unknown'] as const)
      .map((status) => ({ ...baseTask, id: status, title: status, status }))
    const tasks: Task[] = [...inactiveTasks, baseTask, { ...baseTask, id: 'second', title: '构建项目' }, { ...baseTask, id: 'tool', kind: 'tool', title: '普通工具' }]
    const { rerender } = render(<MantineProvider><SessionRuntimeBar commands={[]} tasks={tasks} onTaskOpen={onTaskOpen} /></MantineProvider>)
    expect(screen.getByText('2 个运行中')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '查看后台任务：2 个运行中' }))
    fireEvent.click(screen.getByRole('button', { name: '构建项目' }))
    expect(onTaskOpen).toHaveBeenLastCalledWith(tasks[7])
    for (const text of [...inactiveTasks.map((task) => task.title), '普通工具']) {
      expect(screen.queryByText(text)).not.toBeInTheDocument()
    }
    rerender(<MantineProvider><SessionRuntimeBar commands={[]} tasks={tasks.map((task) => ({ ...task, status: 'completed' }))} /></MantineProvider>)
    expect(screen.queryByLabelText('运行摘要')).not.toBeInTheDocument()
  })

  it('aggregates actual task statuses and exposes every todo and subagent without background tasks', () => {
    const onTaskOpen = vi.fn()
    const tasks: Task[] = [
      { ...baseTask, id: 'todo1', title: '分析', kind: 'todo', status: 'completed' },
      { ...baseTask, id: 'todo2', title: '实现', kind: 'todo' },
      { ...baseTask, id: 'todo3', title: '已移除步骤', kind: 'todo', status: 'cancelled' },
      { ...baseTask, id: 'agent1', title: '检查', kind: 'subagent', status: 'completed' },
      { ...baseTask, id: 'agent2', title: '验证', kind: 'subagent' },
    ]
    render(<MantineProvider><SessionRuntimeBar commands={[]} tasks={tasks} onTaskOpen={onTaskOpen} /></MantineProvider>)
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByText('1 个活动 / 2 个')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '查看待办：1 / 2' }))
    for (const task of tasks.filter((task) => task.status !== 'cancelled')) {
      fireEvent.click(screen.getByRole('button', { name: task.title }))
      expect(onTaskOpen).toHaveBeenLastCalledWith(task)
    }
    expect(screen.queryByText('已移除步骤')).not.toBeInTheDocument()
  })
})
