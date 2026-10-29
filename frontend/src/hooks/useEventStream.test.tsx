import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EventEnvelope } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useEventStream } from './useEventStream'

const mocks = vi.hoisted(() => ({
  connectEventStream: vi.fn(),
  show: vi.fn(),
  hide: vi.fn(),
}))

vi.mock('../api/eventStream', () => ({ connectEventStream: mocks.connectEventStream }))
vi.mock('@mantine/notifications', () => ({ notifications: { show: mocks.show, hide: mocks.hide } }))

afterEach(() => {
  useAstrorderStore.getState().resetRuntime()
  mocks.connectEventStream.mockReset()
  mocks.show.mockReset()
  mocks.hide.mockReset()
})

function taskEvent(envelopeId: string, status = 'completed'): EventEnvelope {
  return {
    id: envelopeId,
    cursor: 1,
    type: 'task.upsert',
    agent_id: 'agent-1',
    session_id: 'session-1',
    data: {
      id: 'task-1',
      agent_id: 'agent-1',
      session_id: 'session-1',
      kind: 'background',
      title: '后台构建',
      status,
      progress: null,
      command: null,
      logs: [],
      target_id: 'target-1',
      created_at: '2026-09-07T00:00:00Z',
      updated_at: '2026-09-07T00:01:00Z',
    },
  }
}

function StreamHarness({ client = new QueryClient() }: { client?: QueryClient }) {
  useEventStream(true, client)
  return null
}

function noticeEvent(kind: 'task_completed' | 'task_failed', cursor: number): EventEnvelope {
  return {
    id: `notice-${cursor}`, cursor, type: 'notification.created', agent_id: 'agent-1', session_id: 'session-1',
    data: { key: `notice-${kind}`, kind, title: '任务通知', message: '测试任务', agent_id: 'agent-1', session_id: 'session-1', created_at: new Date().toISOString() },
  }
}

describe('event stream notifications', () => {
  it('does not toast replayed completion or tool progress', () => {
    useAstrorderStore.setState({ cursor: 5 })
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    act(() => {
      options?.onEvent(noticeEvent('task_completed', 1))
      const tool = taskEvent('tool')
      tool.cursor = 6
      tool.data.kind = 'tool'
      options?.onEvent(tool)
    })
    expect(mocks.show).not.toHaveBeenCalled()
    expect(useAstrorderStore.getState().cursor).toBe(6)
    view.unmount()
  })

  it('applies an empty-goal tombstone without a toast or saved failure notification', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    act(() => {
      const cleared = taskEvent('empty-goal', 'cancelled')
      cleared.data = { ...cleared.data, id: 'codex:goal:current', kind: 'todo', title: '目标', progress: { type: 'goal', source: 'codex', blocking: false } }
      options?.onEvent(cleared)
    })
    expect(Object.values(useAstrorderStore.getState().tasks)).toEqual([expect.objectContaining({ id: 'codex:goal:current', status: 'cancelled' })])
    expect(mocks.show).not.toHaveBeenCalled()
    expect(Object.keys(useAstrorderStore.getState().notifications)).toHaveLength(0)
    view.unmount()
  })

  it('reconciles history, receipts and tasks on connection resume', () => {
    let options: { onResume: () => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    act(() => options?.onResume())
    for (const resource of ['bootstrap', 'messages', 'commands', 'tasks']) {
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', resource] })
    }
    view.unmount()
  })

  it('refreshes the transcript when a command completes', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    render(<StreamHarness client={client} />)

    act(() => {
      options?.onEvent({
        id: 'command-completed',
        cursor: 1,
        type: 'command.upsert',
        agent_id: 'agent-1',
        session_id: 'session-1',
        data: { state: 'completed' },
      })
    })

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'],
    })
  })

  it('refreshes native history when a Codex turn completes without a command receipt', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    act(() => options?.onEvent({ id: 'native-end', cursor: 0, type: 'codex.notification', agent_id: 'agent-1', session_id: 'session-1', data: { frame: { method: 'turn/completed', params: { threadId: 'session-1', turn: { id: 'turn', status: 'completed' } } } } }))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'] })
    view.unmount()
  })

  it('coalesces hook refreshes and follows activity received during a pending history read', async () => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    let release!: () => void
    const invalidate = vi.spyOn(client, 'invalidateQueries')
      .mockImplementationOnce(() => new Promise<void>(resolve => { release = resolve }))
      .mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    const hook = () => options?.onEvent({ id: 'hook', cursor: 0, type: 'transcript.invalidate', agent_id: 'agent-1', session_id: 'session-1', data: { hook_event: 'PostToolUse' } })
    try {
      act(() => { hook(); hook(); hook() })
      await act(async () => { await vi.advanceTimersByTimeAsync(250) })
      expect(invalidate).toHaveBeenCalledTimes(1)
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'] }, { cancelRefetch: false })
      act(() => { hook(); hook() })
      await act(async () => { await vi.advanceTimersByTimeAsync(250); release() })
      await act(async () => { await vi.advanceTimersByTimeAsync(250) })
      expect(invalidate).toHaveBeenCalledTimes(2)
      act(() => hook())
      view.unmount()
      await vi.advanceTimersByTimeAsync(250)
      expect(invalidate).toHaveBeenCalledTimes(2)
    } finally {
      view.unmount()
      vi.useRealTimers()
    }
  })

  it('refreshes native history to position an unanchored Codex turn', async () => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    try {
      act(() => options?.onEvent({ id: 'unanchored', cursor: 0, type: 'message.upsert', agent_id: 'agent-1', session_id: 'session-1', data: { id: 'reply', agent_id: 'agent-1', session_id: 'session-1', role: 'assistant', kind: 'message', text: 'progress', turn_id: 'unknown-turn', created_at: '2026-10-16T02:00:00Z', attachments: [], tool: null, command_id: null } }))
      await act(async () => { await vi.advanceTimersByTimeAsync(250) })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'] }, { cancelRefetch: false })
    } finally { view.unmount(); vi.useRealTimers() }
  })

  it('refreshes native history on a running-to-idle session transition', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    act(() => {
      options?.onEvent({ id: 'running', cursor: 1, type: 'session.upsert', agent_id: 'agent-1', session_id: 'session-1', data: { id: 'session-1', agent_id: 'agent-1', status: 'running' } })
      options?.onEvent({ id: 'idle', cursor: 2, type: 'session.upsert', agent_id: 'agent-1', session_id: 'session-1', data: { id: 'session-1', agent_id: 'agent-1', status: 'idle' } })
    })
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'] })
    view.unmount()
  })

  it('shows completion and failure notices once despite repeated delivery', () => {
    vi.stubGlobal('Notification', { permission: 'default', requestPermission: vi.fn() })
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((nextOptions) => {
      options = nextOptions
      return vi.fn()
    })
    render(<QueryClientProvider client={new QueryClient()}><StreamHarness /></QueryClientProvider>)

    act(() => {
      options?.onEvent(noticeEvent('task_completed', 1))
      const failed = noticeEvent('task_failed', 2)
      options?.onEvent(failed)
      options?.onEvent({ ...failed, id: 'envelope-2', cursor: 3 })
    })

    expect(mocks.show).toHaveBeenCalledTimes(2)
    expect(vi.mocked(Notification.requestPermission)).not.toHaveBeenCalled()
    expect(Object.keys(useAstrorderStore.getState().notifications)).toHaveLength(2)
  })

  it('navigates internally to mobile route when clicked on mobile without opening external window', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((nextOptions) => {
      options = nextOptions
      return vi.fn()
    })
    const navigate = vi.fn()
    window.history.pushState({}, '', '/mobile/chat/old-session')
    function CustomHarness() {
      useEventStream(true, new QueryClient(), navigate)
      return null
    }
    render(
      <QueryClientProvider client={new QueryClient()}>
        <CustomHarness />
      </QueryClientProvider>,
    )
    act(() => {
      const failed = noticeEvent('task_failed', 2)
      options?.onEvent(failed)
    })
    expect(mocks.show).toHaveBeenCalledOnce()
    const toastProps = mocks.show.mock.calls[0][0]
    expect(typeof toastProps.onClick).toBe('function')
    toastProps.onClick()
    expect(navigate).toHaveBeenCalledWith('/mobile/chat/session-1?agent_id=agent-1')
  })
})
