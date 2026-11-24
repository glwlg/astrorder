import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { ActionInboxView } from './ActionInboxView'

describe('ActionInboxView', () => {
  beforeEach(() => {
    localStorage.clear()
    useAstrorderStore.getState().resetRuntime()
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('approves a pending request inline and sends quick replies on ready sessions', async () => {
    const createCommand = vi.spyOn(api, 'createCommand').mockImplementation(async (payload) => ({
      ...payload,
      state: 'accepted',
      attachments: [],
      created_at: '2026-11-23T10:10:00Z',
      error: null,
    }))

    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 10,
      agents: [{
        id: 'agent-1',
        kind: 'antigravity',
        name: 'Antigravity',
        status: 'ready',
        capabilities: ['chat', 'approvals'],
        limitation: null,
      }],
      sessions: [
        { id: 's-app', agent_id: 'agent-1', title: '审批会话', workspace: '/repo', status: 'waiting_approval', updated_at: '2026-11-23T10:00:00Z' },
        { id: 's-err', agent_id: 'agent-1', title: '失败会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-23T10:03:00Z' },
        { id: 's-slept', agent_id: 'agent-1', title: '休眠旧会话', workspace: '/repo', status: 'error', updated_at: '2026-11-20T10:00:00Z' },
        { id: 's-done', agent_id: 'agent-1', title: '完工会话', workspace: '/repo', status: 'idle', updated_at: '2026-11-23T10:05:00Z' },
      ],
    })
    useAstrorderStore.setState({
      approvals: {
        'ap-1': {
          id: 'ap-1',
          agent_id: 'agent-1',
          session_id: 's-app',
          title: '运行测试脚本',
          detail: 'npm test',
          state: 'pending',
          target_id: 'ap-1',
          data: { created_at: '2026-11-23T10:01:00Z' },
        },
      },
      notifications: {
        'n-err': {
          key: 'n-err',
          agent_id: 'agent-1',
          session_id: 's-err',
          kind: 'task_failed',
          title: '失败会话',
          message: '单测执行失败',
          created_at: '2026-11-23T10:03:00Z',
          cursor: 9,
        },
        'n-1': {
          key: 'n-1',
          agent_id: 'agent-1',
          session_id: 's-done',
          kind: 'task_completed',
          title: '完工会话',
          message: '前端构建和单测已全部通过',
          created_at: '2026-11-23T10:05:00Z',
          cursor: 10,
        },
      },
    })

    const onOpenSession = vi.fn()
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <ActionInboxView onOpenSession={onOpenSession} />
        </QueryClientProvider>
      </MantineProvider>,
    )

    expect(screen.getByText('审批会话')).toBeInTheDocument()
    expect(screen.getByText('失败会话')).toBeInTheDocument()
    expect(screen.getByText('完工会话')).toBeInTheDocument()
    expect(screen.queryByText('休眠旧会话')).not.toBeInTheDocument()

    // Inline approval
    fireEvent.click(screen.getByRole('button', { name: '批准执行' }))
    await waitFor(() => expect(createCommand).toHaveBeenCalledWith(expect.objectContaining({
      agent_id: 'agent-1',
      session_id: 's-app',
      action: 'approve',
      target_id: 'ap-1',
    })))

    // Inline quick reply on completed session
    const replyInput = screen.getByLabelText('快捷回复 完工会话')
    fireEvent.change(replyInput, { target: { value: '请直接提交推送' } })
    fireEvent.submit(replyInput.closest('form')!)
    await waitFor(() => expect(createCommand).toHaveBeenCalledWith(expect.objectContaining({
      agent_id: 'agent-1',
      session_id: 's-done',
      action: 'send',
      text: '请直接提交推送',
    })))
  })

  it('clears an async question card immediately once answered via command receipt or native history', async () => {
    const qMsg = {
      id: 'antigravity:s-mon:live:1',
      agent_id: 'agent-1',
      session_id: 's-mon',
      role: 'assistant' as const,
      kind: 'message' as const,
      text: '请选择改造节奏',
      attachments: [],
      created_at: '2026-11-24T02:55:00Z',
      command_id: null,
      tool: null,
      delivery: 'async' as const,
      answered: false,
      questions: [{ title: '本次改造范围按哪种节奏推进?', options: ['方案A', '方案B'] }],
    }
    vi.spyOn(api, 'createCommand').mockResolvedValue({
      id: `async-answer:${qMsg.id}`,
      agent_id: 'agent-1',
      session_id: 's-mon',
      action: 'send',
      target_id: qMsg.id,
      text: '方案A',
      attachments: [],
      state: 'accepted',
      created_at: '2026-11-24T02:55:05Z',
      error: null,
    })
    vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [qMsg], next_cursor: null })

    useAstrorderStore.getState().hydrateBootstrap({
      protocol_version: 1,
      cursor: 1,
      agents: [{
        id: 'agent-1',
        kind: 'antigravity',
        name: 'Antigravity',
        status: 'ready',
        capabilities: ['chat', 'approvals'],
        limitation: null,
      }],
      sessions: [
        { id: 's-mon', agent_id: 'agent-1', title: '监控Agent', workspace: '/repo', status: 'idle', updated_at: '2026-11-24T02:55:00Z' },
      ],
    })
    useAstrorderStore.getState().mergeMessages('agent-1', 's-mon', [qMsg])

    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <ActionInboxView onOpenSession={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    )

    expect(screen.getByText('监控Agent')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: '方案A' }))
    fireEvent.click(screen.getByRole('button', { name: '发送回答' }))

    await waitFor(() => expect(screen.queryByText('监控Agent')).not.toBeInTheDocument())
    expect(screen.getByText('全部处理完毕')).toBeInTheDocument()
  })
})
