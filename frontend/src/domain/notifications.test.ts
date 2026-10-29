import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { EventEnvelope } from './types'
import { deliverBrowserNotification, disableSystemNotifications, notificationForEvent, notificationPermission, restorePushSubscription, setSystemNotificationEnabled, testSystemNotification } from './notifications'
import { useAstrorderStore } from '../state/store'
import { api } from '../api/client'

const event: EventEnvelope = {
  id: 'notice-1', cursor: 7, type: 'notification.created', agent_id: 'a', session_id: 's',
  data: { key: 'notice-1', kind: 'task_completed', title: '会话已完成', message: '测试 · 会话已完成', agent_id: 'a', session_id: 's', created_at: '2026-10-28T00:00:00Z' },
}

describe('canonical notifications', () => {
  beforeEach(() => { useAstrorderStore.getState().resetRuntime(); setSystemNotificationEnabled(true) })
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
  it.each(['task_completed', 'task_failed', 'approval_pending'])('accepts and deduplicates %s from the server', kind => {
    const notice = notificationForEvent({ ...event, data: { ...event.data, kind } })!
    expect(notice.kind).toBe(kind)
    expect(useAstrorderStore.getState().addNotification(notice)).toBe(true)
    expect(useAstrorderStore.getState().addNotification({ ...notice, message: 'changed' })).toBe(false)
  })
  it('leaves raw native classification to the server and rejects malformed notices', () => {
    for (const type of ['message.upsert', 'approval.upsert', 'task.upsert', 'codex.notification', 'native.observation']) {
      expect(notificationForEvent({ ...event, type })).toBeNull()
    }
    expect(notificationForEvent({ ...event, data: { ...event.data, kind: 'unexpected' } })).toBeNull()
    expect(notificationForEvent({ ...event, data: { ...event.data, key: '' } })).toBeNull()
  })
  it('restores subscriptions without asking permission, avoids duplicate websocket alerts, and unsubscribes', async () => {
    const requestPermission = vi.fn()
    const notificationConstructor = vi.fn()
    Object.assign(notificationConstructor, { permission: 'granted', requestPermission })
    vi.stubGlobal('Notification', notificationConstructor)
    const subscription = { endpoint: 'https://push.example/device', toJSON: () => ({ endpoint: 'https://push.example/device', keys: { auth: 'auth', p256dh: 'public' } }), unsubscribe: vi.fn().mockResolvedValue(true) }
    vi.stubGlobal('navigator', { serviceWorker: { getRegistration: vi.fn().mockResolvedValue({ pushManager: { getSubscription: vi.fn().mockResolvedValue(subscription) } }) } })
    const save = vi.spyOn(api, 'savePushSubscription').mockResolvedValue({ enabled: true })
    const remove = vi.spyOn(api, 'deletePushSubscription').mockResolvedValue({ enabled: false })
    const test = vi.spyOn(api, 'testPushSubscription').mockResolvedValue({ accepted: true })
    await restorePushSubscription()
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ endpoint: subscription.endpoint }))
    expect(notificationPermission()).toBe('granted')
    expect(requestPermission).not.toHaveBeenCalled()
    await deliverBrowserNotification(notificationForEvent(event)!)
    expect(notificationConstructor).not.toHaveBeenCalled()
    await testSystemNotification()
    expect(test).toHaveBeenCalledWith(subscription.endpoint)
    await disableSystemNotifications()
    expect(remove).toHaveBeenCalledWith(subscription.endpoint)
    expect(subscription.unsubscribe).toHaveBeenCalledOnce()
    expect(notificationPermission()).toBe('default')
  })
})
