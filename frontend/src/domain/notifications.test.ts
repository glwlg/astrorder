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

  it('does not notify for Codex command execution results', () => {
    expect(notificationForEvent(taskEvent('completed', 'codex:background:command-1'))).toBeNull()
    expect(notificationForEvent(taskEvent('failed', 'codex:background:command-2'))).toBeNull()
  })

  it('notifies once when a native question needs an answer', () => {
    const event: EventEnvelope = { id: 'question', cursor: 3, type: 'approval.upsert', agent_id: 'a', session_id: 's', data: { id: 'codex:91', state: 'pending', method: 'item/tool/requestUserInput', title: 'Codex 等待你的回答' } }
    const notification = notificationForEvent(event)!
    expect(notification.title).toBe('Codex 等待你的回答')
    expect(useAstrorderStore.getState().addNotification(notification)).toBe(true)
    expect(useAstrorderStore.getState().addNotification(notification)).toBe(false)
  })

  it('only notifies for failed terminal tasks and pending approvals', () => {
    const failed = notificationForEvent(taskEvent('failed'))
    const approval = notificationForEvent({
      id: 'approval-envelope-1',
      cursor: 2,
      type: 'approval.upsert',
      agent_id: 'agent-1',
      session_id: 'session-1',
      data: { id: 'approval-1', state: 'pending', title: '需要确认', detail: '允许继续' },
    })

    expect(notificationForEvent(taskEvent('completed'))).toBeNull()
    expect(failed?.key).toBe('agent-1::session-1::task::task-1::failed')
    expect(approval?.key).toBe('agent-1::session-1::approval::approval-1::pending')
    expect(notificationForEvent(taskEvent('running'))).toBeNull()
  })

  it.each(['codex', 'hermes'])('does not turn a cleared %s goal into a task failure', source => {
    const event = taskEvent('cancelled', `${source}:goal:current`)
    event.data.kind = 'todo'
    event.data.title = '目标'
    event.data.progress = { type: 'goal', source, blocking: false }
    expect(notificationForEvent(event)).toBeNull()
  })

  it('keeps real failures visible, including a blocked native goal', () => {
    const event = taskEvent('failed', 'hermes:goal:current')
    event.data.kind = 'todo'
    event.data.progress = { type: 'goal', source: 'hermes', native_status: 'blocked' }
    expect(notificationForEvent(event)?.kind).toBe('task_failed')
    expect(notificationForEvent(taskEvent('cancelled'))?.kind).toBe('task_failed')
  })

  it('lets the actionable approval event own observer approval notifications', () => {
    expect(notificationForEvent({
      id: 'observation', cursor: 3, type: 'native.observation', agent_id: 'agent-1', session_id: 'session-1',
      data: { id: 'native-request', event: 'PermissionRequest', observed_at: Date.now() / 1000, notification: true, approval_pending: true },
    })).toBeNull()
  })

  it('deduplicates repeated terminal events by stable event identity, not text or time', () => {
    const notification = notificationForEvent(taskEvent('failed'))
    expect(notification).not.toBeNull()
    const store = useAstrorderStore.getState()

    expect(store.addNotification(notification!)).toBe(true)
    expect(store.addNotification({ ...notification!, message: 'same task with different text' })).toBe(false)
    expect(Object.keys(useAstrorderStore.getState().notifications)).toEqual([notification!.key])
  })
})
