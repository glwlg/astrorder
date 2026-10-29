import { api } from '../api/client'
import type { EventEnvelope, EventNotification } from './types'

export function notificationForEvent(event: EventEnvelope): EventNotification | null {
  if (event.type !== 'notification.created') return null
  const notice = event.data as unknown as EventNotification
  if (!notice.key || !notice.agent_id || !notice.session_id || !notice.title || !notice.message ||
    !['task_completed', 'task_failed', 'approval_pending'].includes(notice.kind)) return null
  return { ...notice, cursor: event.cursor }
}

const NOTIFICATION_ENABLED_KEY = 'astrorder:system_notification_enabled'
let pushSubscribed = false

export function isSystemNotificationEnabled(): boolean {
  try { return localStorage.getItem(NOTIFICATION_ENABLED_KEY) !== 'false' } catch { return true }
}

export function setSystemNotificationEnabled(enabled: boolean): void {
  try { localStorage.setItem(NOTIFICATION_ENABLED_KEY, String(enabled)) } catch {}
}

export type NotificationPermissionState = 'granted' | 'denied' | 'default' | 'unsupported'

function desktopNotify() {
  return typeof window !== 'undefined'
    ? (window as unknown as { astrorderDesktop?: { notify?: (notice: { title: string; body: string; tag: string }) => Promise<boolean> } }).astrorderDesktop?.notify
    : undefined
}

function mobileRoute(): boolean {
  return window.location.pathname.startsWith('/mobile') || window.innerWidth < 768 || window.matchMedia?.('(max-width: 767px)').matches
}

export function notificationPermission(): NotificationPermissionState {
  if (!isSystemNotificationEnabled()) return 'default'
  if (desktopNotify()) return 'granted'
  if (typeof Notification === 'undefined') return 'unsupported'
  // Browser permission alone does not mean that this device has subscribed.
  return Notification.permission === 'granted' && !pushSubscribed ? 'default' : Notification.permission
}

async function saveSubscription(subscription: PushSubscription): Promise<void> {
  const json = subscription.toJSON()
  if (!json.endpoint || !json.keys) throw new Error('浏览器未返回完整的通知订阅')
  await api.savePushSubscription({ endpoint: json.endpoint, keys: json.keys, origin: window.location.origin, mobile: mobileRoute() })
  pushSubscribed = true
  window.dispatchEvent(new Event('astrorder:notification-settings'))
}

export async function restorePushSubscription(): Promise<void> {
  if (desktopNotify() || !isSystemNotificationEnabled() || typeof Notification === 'undefined' || Notification.permission !== 'granted' || !('serviceWorker' in navigator)) return
  const registration = await navigator.serviceWorker.getRegistration()
  const subscription = await registration?.pushManager?.getSubscription()
  if (subscription) await saveSubscription(subscription)
}

export async function requestNotificationPermission(): Promise<NotificationPermissionState> {
  if (desktopNotify()) { setSystemNotificationEnabled(true); return 'granted' }
  if (typeof Notification === 'undefined') return 'unsupported'
  if (Notification.permission === 'denied') return 'denied'
  if (window.location.protocol !== 'https:' || !('serviceWorker' in navigator) || !('PushManager' in window)) {
    throw new Error('后台通知需要通过 HTTPS 打开星序，并使用支持推送的浏览器；iPhone 请先将星序添加到主屏幕。')
  }
  const permission = Notification.permission === 'granted' ? 'granted' : await Notification.requestPermission()
  if (permission !== 'granted') return permission
  const registration = await navigator.serviceWorker.ready
  const { public_key } = await api.getPushSettings()
  const decoded = atob(public_key.replace(/-/g, '+').replace(/_/g, '/'))
  const key = Uint8Array.from(decoded, char => char.charCodeAt(0))
  const subscription = await registration.pushManager.getSubscription() || await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key })
  await saveSubscription(subscription)
  setSystemNotificationEnabled(true)
  return 'granted'
}

export async function disableSystemNotifications(): Promise<void> {
  if (!desktopNotify() && 'serviceWorker' in navigator) {
    const registration = await navigator.serviceWorker.getRegistration()
    const subscription = await registration?.pushManager?.getSubscription()
    if (subscription) {
      await api.deletePushSubscription(subscription.endpoint)
      await subscription.unsubscribe()
    }
  }
  pushSubscribed = false
  setSystemNotificationEnabled(false)
  window.dispatchEvent(new Event('astrorder:notification-settings'))
}

export async function testSystemNotification(): Promise<void> {
  if (desktopNotify()) {
    const accepted = await deliverBrowserNotification({ key: `test-${Date.now()}`, kind: 'task_completed', title: '星序 · 测试通知', message: '任务完成和待回答问题将在这里提醒。', agent_id: 'system', session_id: 'system', created_at: new Date().toISOString() })
    if (!accepted) throw new Error('系统未接受通知，请检查 Windows 通知设置。')
    return
  }
  const registration = await navigator.serviceWorker.getRegistration()
  const subscription = await registration?.pushManager?.getSubscription()
  if (!subscription) throw new Error('请先启用后台通知')
  await api.testPushSubscription(subscription.endpoint)
}

export async function deliverBrowserNotification(notification: EventNotification): Promise<boolean> {
  if (!isSystemNotificationEnabled()) return false
  const notify = desktopNotify()
  try {
    if (notify) return await notify({ title: notification.title, body: notification.message, tag: notification.key })
    // Subscribed browsers receive their system alert from Web Push, including
    // while locked. The websocket still owns the in-app reminder.
    if (pushSubscribed) return true
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return false
    const prefix = mobileRoute() ? '/mobile' : ''
    const url = `${prefix}/chat/${encodeURIComponent(notification.session_id)}?agent_id=${encodeURIComponent(notification.agent_id)}`
    const registration = 'serviceWorker' in navigator ? await navigator.serviceWorker.getRegistration() : undefined
    if (registration) {
      await registration.showNotification(notification.title, { body: notification.message, tag: notification.key, data: { url }, icon: '/pwa-192.png' })
      return true
    }
    const n = new Notification(notification.title, { body: notification.message, tag: notification.key })
    n.onclick = event => {
      event.preventDefault()
      window.focus()
      window.history.pushState({}, '', url)
      window.dispatchEvent(new PopStateEvent('popstate'))
      n.close()
    }
    return true
  } catch {
    return false
  }
}
