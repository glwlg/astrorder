import { beforeEach, describe, expect, it } from 'vitest'
import type { EventEnvelope } from './types'
import { notificationForEvent } from './notifications'
import { useAstrorderStore } from '../state/store'

function taskEvent(status: string, id = 'task-1'): EventEnvelope {
  return {
    id: `envelope-${status}-${id}`,
    cursor: 1,
    type: 'task.upsert',
    agent_id: 'agent-1',
    session_id: 'session-1',
    data: {
      id,
      agent_id: 'agent-1',
      session_id: 'session-1',
      kind: 'background',
      title: '后台构建',
      status,
      progress: null,
      command: 'npm run build',
      logs: [],
      target_id: 'target-1',
      created_at: '2026-09-07T00:00:00Z',
      updated_at: '2026-09-07T00:01:00Z',
    },
  }
}

describe('event notifications', () => {
  beforeEach(() => useAstrorderStore.getState().resetRuntime())

  it('keeps tool completion in the transcript instead of showing a toast', () => {
    const event = taskEvent('completed')
    event.data.kind = 'tool'
    expect(notificationForEvent(event)).toBeNull()
  })

  it('creates stable source-session keys for terminal tasks and pending approvals', () => {
    const completed = notificationForEvent(taskEvent('completed'))
    const failed = notificationForEvent(taskEvent('failed'))
    const approval = notificationForEvent({
      id: 'approval-envelope-1',
      cursor: 2,
      type: 'approval.upsert',
      agent_id: 'agent-1',
      session_id: 'session-1',
      data: { id: 'approval-1', state: 'pending', title: '需要确认', detail: '允许继续' },
    })

    expect(completed?.key).toBe('agent-1::session-1::task::task-1::completed')
    expect(failed?.key).toBe('agent-1::session-1::task::task-1::failed')
    expect(approval?.key).toBe('agent-1::session-1::approval::approval-1::pending')
    expect(notificationForEvent(taskEvent('running'))).toBeNull()
  })

  it('deduplicates repeated terminal events by stable event identity, not text or time', () => {
    const notification = notificationForEvent(taskEvent('completed'))
    expect(notification).not.toBeNull()
    const store = useAstrorderStore.getState()

    expect(store.addNotification(notification!)).toBe(true)
    expect(store.addNotification({ ...notification!, message: 'same task with different text' })).toBe(false)
    expect(Object.keys(useAstrorderStore.getState().notifications)).toEqual([notification!.key])
  })
})
