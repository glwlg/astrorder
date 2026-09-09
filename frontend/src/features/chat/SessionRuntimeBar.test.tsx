import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Command, Message, Task } from '../../domain/types'
import { SessionRuntimeBar } from './SessionRuntimeBar'
afterEach(cleanup)

const baseMessage: Message = {
  id: 'tool-1',
  session_id: 'session-1',
  agent_id: 'agent-1',
  role: 'tool',
  kind: 'tool',
  text: '工作区状态已读取',
  attachments: [],
  created_at: '2026-09-07T10:00:00Z',
  command_id: null,
  tool: {
    name: 'git.status',
    branch: 'feature/stitch',
    changed_files: 3,
    todo: { completed: 2, total: 5 },
    subagents: { running: 1, total: 2 },
  },
}

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
  it('renders reported runtime metadata and expands public activity only', () => {
    render(
      <MantineProvider>
        <SessionRuntimeBar messages={[baseMessage]} commands={[baseCommand]} />
      </MantineProvider>,
    )

    expect(screen.getByText('feature/stitch')).toBeInTheDocument()
    expect(screen.getByText('3 个文件')).toBeInTheDocument()
    expect(screen.getAllByText('2 / 5')).not.toHaveLength(0)
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByText('1 个排队')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '展开运行详情' }))
    expect(screen.getByText('git.status')).toBeInTheDocument()
    expect(screen.getByText('工作区状态已读取')).toBeInTheDocument()
  })

  it('does not invent values when the connector reports no runtime metadata', () => {
    render(
      <MantineProvider>
        <SessionRuntimeBar messages={[]} commands={[]} />
      </MantineProvider>,
    )

    expect(screen.getAllByText('未报告')).toHaveLength(5)
    expect(screen.queryByText('main')).not.toBeInTheDocument()
    expect(screen.getByText('暂无排队')).toBeInTheDocument()
  })

  it('opens a real task from the background-task summary instead of parsing a demo value', () => {
    const onTaskOpen = vi.fn()
    render(
      <MantineProvider>
        <SessionRuntimeBar messages={[]} commands={[]} tasks={[baseTask]} onTaskOpen={onTaskOpen} />
      </MantineProvider>,
    )

    expect(screen.getAllByText('2 / 5')).not.toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: '查看后台任务：运行测试' }))
    expect(onTaskOpen).toHaveBeenCalledWith(baseTask)
  })
  it('uses native branch metadata instead of guessing from chat or an old tool result', () => {
    render(<MantineProvider><SessionRuntimeBar messages={[baseMessage]} commands={[]} nativeBranch="native/current" /></MantineProvider>)
    expect(screen.getByText('native/current')).toBeInTheDocument()
    expect(screen.queryByText('feature/stitch')).not.toBeInTheDocument()
  })
})
