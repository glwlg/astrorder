import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EventEnvelope } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useEventStream } from './useEventStream'

const mocks = vi.hoisted(() => ({
  connectEventStream: vi.fn(),
  show: vi.fn(),
}))

vi.mock('../api/eventStream', () => ({ connectEventStream: mocks.connectEventStream }))
vi.mock('@mantine/notifications', () => ({ notifications: { show: mocks.show } }))

afterEach(() => {
  useAstrorderStore.getState().resetRuntime()
  mocks.connectEventStream.mockReset()
  mocks.show.mockReset()
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

describe('event stream notifications', () => {
  it('does not toast replayed completion or tool progress', () => {
    useAstrorderStore.setState({ cursor: 5 })
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((next) => { options = next; return vi.fn() })
    const view = render(<StreamHarness />)
    act(() => {
      options?.onEvent(taskEvent('old'))
      const tool = taskEvent('tool')
      tool.cursor = 6
      tool.data.kind = 'tool'
      options?.onEvent(tool)
    })
    expect(mocks.show).not.toHaveBeenCalled()
    expect(useAstrorderStore.getState().cursor).toBe(6)
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

  it('shows one notification for repeated failures and none for normal completion', () => {
    vi.stubGlobal('Notification', { permission: 'default', requestPermission: vi.fn() })
    let options: { onEvent: (event: EventEnvelope) => void } | undefined
    mocks.connectEventStream.mockImplementation((nextOptions) => {
      options = nextOptions
      return vi.fn()
    })
    render(<QueryClientProvider client={new QueryClient()}><StreamHarness /></QueryClientProvider>)

    act(() => {
      options?.onEvent(taskEvent('completed'))
      const failed = taskEvent('envelope-1', 'failed')
      failed.cursor = 2
      options?.onEvent(failed)
      options?.onEvent({ ...failed, id: 'envelope-2', cursor: 3 })
    })

    expect(mocks.show).toHaveBeenCalledOnce()
    expect(vi.mocked(Notification.requestPermission)).not.toHaveBeenCalled()
    expect(Object.keys(useAstrorderStore.getState().notifications)).toHaveLength(1)
  })
})
