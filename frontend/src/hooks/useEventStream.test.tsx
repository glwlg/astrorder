import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EventEnvelope, Message, Session } from '../domain/types'
import { api } from '../api/client'
import { useAstrorderStore } from '../state/store'
import { useSessionResources } from './useAstrorderData'
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

  it('refreshes the matching transcript as soon as a completion notification arrives', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    act(() => options?.onEvent(noticeEvent('task_completed', 1)))
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages', 'agent-1', 'session-1'] })
    view.unmount()
  })

  it('shows the completed native reply when a notification overtakes queued stream output', async () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const session: Session = { id: 'session-1', agent_id: 'agent-1', title: '测试', status: 'running', workspace: null, updated_at: '2026-10-16T02:00:00Z' }
    const user: Message = { id: 'user', agent_id: session.agent_id, session_id: session.id, role: 'user', kind: 'message', text: '问题', turn_id: 'turn', created_at: session.updated_at, attachments: [], tool: null, command_id: null }
    const reply: Message = { ...user, id: 'reply', role: 'assistant', text: '前半完整' }
    vi.spyOn(api, 'renewSessionView').mockResolvedValue({ ok: true })
    vi.spyOn(api, 'syncSession').mockResolvedValue(session)
    const getMessages = vi.spyOn(api, 'getMessages').mockResolvedValue({ items: [user], next_cursor: null })
    vi.spyOn(api, 'getCommands').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'getTasks').mockResolvedValue({ items: [] })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    function LiveView() {
      useEventStream(true, client)
      const resources = useSessionResources(session, true)
      return <div data-testid="answer">{resources.visibleMessages.find(message => message.id === reply.id)?.text}</div>
    }
    const view = render(<QueryClientProvider client={client}><LiveView /></QueryClientProvider>)
    try {
      await waitFor(() => expect(getMessages).toHaveBeenCalled())
      act(() => {
        options?.onEvent({ id: 'first', cursor: 0, type: 'message.delta', agent_id: session.agent_id, session_id: session.id, data: { ...reply, text: '前' } })
        options?.onEvent({ id: 'second', cursor: 0, type: 'message.delta', agent_id: session.agent_id, session_id: session.id, data: { ...reply, text: '半' } })
      })
      getMessages.mockResolvedValue({ items: [user, reply], next_cursor: null })
      act(() => options?.onEvent(noticeEvent('task_completed', 1)))
      await waitFor(() => expect(screen.getByTestId('answer')).toHaveTextContent(/^前半完整$/))
      act(() => {
        options?.onEvent({ id: 'third', cursor: 0, type: 'message.delta', agent_id: session.agent_id, session_id: session.id, data: { ...reply, text: '完' } })
        options?.onEvent({ id: 'fourth', cursor: 0, type: 'message.delta', agent_id: session.agent_id, session_id: session.id, data: { ...reply, text: '整' } })
      })
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 30)) })
      expect(screen.getByTestId('answer')).toHaveTextContent(/^前半完整$/)
    } finally {
      view.unmount()
      client.clear()
      vi.restoreAllMocks()
    }
  })

  it('renders a burst of deltas together and flushes the completed message immediately', async () => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    const updates = vi.fn()
    const unsubscribe = useAstrorderStore.subscribe(updates)
    const data = { id: 'reply', agent_id: 'agent-1', session_id: 'session-1', role: 'assistant', kind: 'message', text: '字', turn_id: 'turn', created_at: '2026-10-16T02:00:00Z', attachments: [], tool: null, command_id: null }
    try {
      act(() => {
        for (let i = 0; i < 100; i++) {
          options?.onEvent({ id: `delta-${i}`, cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data })
          options?.onEvent({ id: `native-${i}`, cursor: 0, type: 'codex.notification', agent_id: 'agent-1', session_id: 'session-1', data: { frame: { method: 'item/agentMessage/delta' } } })
        }
      })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('字')
      await act(async () => { await vi.advanceTimersByTimeAsync(20) })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('字'.repeat(100))
      expect(updates.mock.calls.length).toBeLessThan(10)
      act(() => {
        options?.onEvent({ id: 'delta-last', cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data: { ...data, text: '尾' } })
        options?.onEvent({ id: 'complete', cursor: 0, type: 'message.upsert', agent_id: 'agent-1', session_id: 'session-1', data: { ...data, text: '完整回复' } })
      })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('完整回复')
      act(() => options?.onEvent({ id: 'delta-2', cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data }))
      await act(async () => { await vi.advanceTimersByTimeAsync(20) })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('完整回复')
    } finally {
      unsubscribe()
      view.unmount()
      vi.useRealTimers()
    }
  })

  it.each([
    ['hermes.stream', { frame: { delta: '字' } }],
    ['grok.notification', { frame: { params: { update: { sessionUpdate: 'agent_message_chunk' } } } }],
  ])('batches normalized output alongside %s raw frames', async (type, raw) => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    const updates = vi.fn()
    const unsubscribe = useAstrorderStore.subscribe(updates)
    const data = { id: 'reply', agent_id: 'agent-1', session_id: 'session-1', role: 'assistant', kind: 'message', text: '字', created_at: '2026-10-16T02:00:00Z', attachments: [], tool: null, command_id: null }
    try {
      act(() => {
        for (let i = 0; i < 20; i++) {
          options?.onEvent({ id: `delta-${i}`, cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data })
          options?.onEvent({ id: `raw-${i}`, cursor: 0, type, agent_id: 'agent-1', session_id: 'session-1', data: raw })
        }
      })
      await act(async () => { await vi.advanceTimersByTimeAsync(20) })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('字'.repeat(20))
      expect(updates.mock.calls.length).toBeLessThanOrEqual(2)
    } finally {
      unsubscribe()
      view.unmount()
      vi.useRealTimers()
    }
  })

  it('flushes pending output on disconnect and unmount', () => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void; onStatus: (status: 'disconnected') => void } | undefined
    mocks.connectEventStream.mockImplementation(next => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    const data = { id: 'reply', agent_id: 'agent-1', session_id: 'session-1', role: 'assistant', kind: 'message', text: '一', created_at: '2026-10-16T02:00:00Z', attachments: [], tool: null, command_id: null }
    try {
      act(() => {
        options?.onEvent({ id: 'first', cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data })
        options?.onEvent({ id: 'second', cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data: { ...data, text: '二' } })
        options?.onStatus('disconnected')
      })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('一二')
      act(() => {
        options?.onEvent({ id: 'third', cursor: 0, type: 'message.delta', agent_id: 'agent-1', session_id: 'session-1', data: { ...data, text: '三' } })
        view.unmount()
      })
      expect(useAstrorderStore.getState().messages['agent-1::session-1'].reply.text).toBe('一二三')
    } finally {
      view.unmount()
      vi.useRealTimers()
    }
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

  it('opens the group from a group reminder without jumping into the member session', () => {
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const navigate = vi.fn()
    const client = new QueryClient()
    window.history.pushState({}, '', '/chat/old-session')
    function GroupHarness() { useEventStream(true, client, navigate); return null }
    render(<GroupHarness />)
    act(() => {
      const event = noticeEvent('task_completed', 8)
      options?.onEvent({ ...event, data: { ...event.data, group_id: 'group-review', title: '复核讨论' } })
    })
    expect(mocks.show).toHaveBeenCalledOnce()
    const toast = mocks.show.mock.calls[0][0]
    expect(toast.title).toBe('复核讨论')
    toast.onClick()
    expect(navigate).toHaveBeenCalledWith('/groups/group-review')
    expect(navigate).not.toHaveBeenCalledWith(expect.stringContaining('/chat/'))
  })

  it('throttles duplicate end-of-turn message and token usage invalidation storms with leading edge', async () => {
    vi.useFakeTimers()
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    try {
      act(() => {
        // Leading edge fires immediately on the first completion event
        options?.onEvent(noticeEvent('task_completed', 10))
        // Burst of overlapping completion signals within the 1500ms cooldown window
        options?.onEvent({ id: 'cmd-done', cursor: 11, type: 'command.upsert', agent_id: 'agent-1', session_id: 'session-1', data: { id: 'cmd-1', agent_id: 'agent-1', session_id: 'session-1', state: 'completed', kind: 'prompt', input: 'hi', output: 'ok', created_at: '2026-10-16T02:00:00Z', updated_at: '2026-10-16T02:00:01Z' } })
        options?.onEvent({ id: 'turn-done', cursor: 12, type: 'codex.notification', agent_id: 'agent-1', session_id: 'session-1', data: { frame: { method: 'turn/completed' } } })
        options?.onEvent({ id: 'stop-hook', cursor: 13, type: 'native.observation', agent_id: 'agent-1', session_id: 'session-1', data: { event: 'Stop' } })
      })
      const messageCalls = () => invalidate.mock.calls.filter(([arg]) => {
        const key = (arg as { queryKey?: unknown[] } | undefined)?.queryKey
        return Array.isArray(key) && key[1] === 'messages' && key[2] === 'agent-1' && key[3] === 'session-1'
      })
      expect(messageCalls()).toHaveLength(1)

      // Trailing refresh fires once after the 1500ms window
      await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
      expect(messageCalls()).toHaveLength(2)

      // Token usage bursts collapse across the 2500ms cooldown window
      act(() => {
        options?.onEvent({ id: 'u1', cursor: 14, type: 'token.usage', agent_id: 'agent-1', session_id: 'session-usage-burst', data: {} })
        options?.onEvent({ id: 'u2', cursor: 15, type: 'token.usage', agent_id: 'agent-1', session_id: 'session-usage-burst', data: {} })
        options?.onEvent({ id: 'u3', cursor: 16, type: 'token.usage', agent_id: 'agent-1', session_id: 'session-usage-burst', data: {} })
      })
      const usageCalls = () => invalidate.mock.calls.filter(([arg]) => {
        const key = (arg as { queryKey?: unknown[] } | undefined)?.queryKey
        return Array.isArray(key) && key[1] === 'session_usage' && key[2] === 'session-usage-burst' && key[3] === 'agent-1'
      })
      expect(usageCalls()).toHaveLength(1)
      await act(async () => { await vi.advanceTimersByTimeAsync(2500) })
      expect(usageCalls()).toHaveLength(2)
    } finally {
      view.unmount()
      vi.useRealTimers()
    }
  })

  it('skips brief live-socket wake invalidations and coalesces rapid reconnect storms', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-24T04:00:00Z'))
    let visibility: DocumentVisibilityState = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
    let options: { onResume: (reason?: 'reconnect' | 'wake') => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const client = new QueryClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries').mockResolvedValue()
    const view = render(<StreamHarness client={client} />)
    try {
      // Brief hide (5s) with an open socket must NOT invalidate queries on wake.
      visibility = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(5_000)
      visibility = 'visible'
      act(() => options?.onResume('wake'))
      expect(invalidate).not.toHaveBeenCalled()

      // Long hide (20s) with an open socket gently refreshes only active messages + tasks.
      visibility = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      vi.advanceTimersByTime(20_000)
      visibility = 'visible'
      act(() => {
        options?.onResume('wake')
        // Immediate duplicate wake (e.g. online + visibilitychange) is coalesced.
        options?.onResume('wake')
      })
      expect(invalidate).toHaveBeenCalledTimes(2)
      expect(invalidate).toHaveBeenCalledWith(
        { queryKey: ['astrorder', 'messages'], refetchType: 'active' },
        { cancelRefetch: false },
      )
      expect(invalidate).toHaveBeenCalledWith(
        { queryKey: ['astrorder', 'tasks'], refetchType: 'active' },
        { cancelRefetch: false },
      )

      // Rapid reconnect calls within 3s only fire one full resync wave.
      invalidate.mockClear()
      act(() => {
        options?.onResume('reconnect')
        options?.onResume('reconnect')
      })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'bootstrap'] })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'messages'] })
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ['astrorder', 'learning'] })
      expect(invalidate).toHaveBeenCalledTimes(9)
    } finally {
      view.unmount()
      vi.restoreAllMocks()
      vi.useRealTimers()
    }
  })
})
