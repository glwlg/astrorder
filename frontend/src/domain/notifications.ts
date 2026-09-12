import type { EventEnvelope, EventNotification } from './types'

function textValue(value: unknown, fallback: string): string {
  return typeof value === 'string' && value.trim() ? value.trim() : fallback
}

function safeLabel(value: string): string {
  return value
    .replace(/(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization|private[_ -]?key)\s*[:=]\s*[^\s,;]+/gi, '$1=[REDACTED]')
    .replaceAll('\r', ' ')
    .replaceAll('\n', ' ')
    .replaceAll(String.fromCharCode(0), ' ')
    .slice(0, 160)
}

function scope(event: EventEnvelope): { agentId: string; sessionId: string } | null {
  const agentId = typeof event.agent_id === 'string' && event.agent_id ? event.agent_id : typeof event.data.agent_id === 'string' ? event.data.agent_id : ''
  const sessionId = typeof event.session_id === 'string' && event.session_id ? event.session_id : typeof event.data.session_id === 'string' ? event.data.session_id : ''
  return agentId && sessionId ? { agentId, sessionId } : null
}

export function notificationForEvent(event: EventEnvelope): EventNotification | null {
  const identity = scope(event)
  if (!identity) return null

  if (event.type === 'native.observation') {
    const name = event.data.event
    const observed = event.data.observed_at
    if (event.data.approval_pending === true || event.data.notification !== true || typeof observed !== 'number' || Math.abs(Date.now() / 1000 - observed) > 60) return null
    if (!['Interrupt', 'PermissionRequest'].includes(String(name))) return null
    const approval = name === 'PermissionRequest'
    return {
      key: `${identity.agentId}::${identity.sessionId}::observation::${String(event.data.id)}`,
      kind: approval ? 'approval_pending' : 'task_failed',
      title: approval ? '原生端等待审批' : '原生轮次已中断',
      message: approval ? '请在原生客户端处理；观察钩子不会代为授权。' : '本轮未正常完成。',
      agent_id: identity.agentId, session_id: identity.sessionId, created_at: new Date(observed * 1000).toISOString(),
    }
  }

  if (event.type === 'task.upsert') {
    const taskId = typeof event.data.id === 'string' ? event.data.id : ''
    if (event.data.kind === 'tool' || taskId.startsWith('codex:background:')) return null
    const status = typeof event.data.status === 'string' ? event.data.status : ''
    if (!taskId || !['failed', 'cancelled'].includes(status)) return null
    const title = safeLabel(textValue(event.data.title, '后台任务'))
    const failed = status !== 'completed'
    return {
      key: `${identity.agentId}::${identity.sessionId}::task::${taskId}::${status === 'cancelled' ? 'failed' : status}`,
      kind: failed ? 'task_failed' : 'task_completed',
      title: failed ? '任务失败' : '任务完成',
      message: `${title}${status === 'cancelled' ? '已取消' : failed ? '未完成' : '已完成'}。`,
      agent_id: identity.agentId,
      session_id: identity.sessionId,
      created_at: new Date().toISOString(),
    }
  }

  if (event.type === 'approval.upsert' && event.data.state === 'pending') {
    const approvalId = typeof event.data.id === 'string' ? event.data.id : ''
    if (!approvalId) return null
    return {
      key: `${identity.agentId}::${identity.sessionId}::approval::${approvalId}::pending`,
      kind: 'approval_pending',
      title: '需要审批',
      message: `${safeLabel(textValue(event.data.title, '有一项操作等待确认'))}。默认不会代为允许。`,
      agent_id: identity.agentId,
      session_id: identity.sessionId,
      created_at: new Date().toISOString(),
    }
  }

  return null
}

export type NotificationPermissionState = 'granted' | 'denied' | 'default' | 'unsupported'

export function notificationPermission(): NotificationPermissionState {
  if (typeof Notification === 'undefined') return 'unsupported'
  return Notification.permission
}

export async function requestNotificationPermission(): Promise<NotificationPermissionState> {
  if (typeof Notification === 'undefined') return 'unsupported'
  if (Notification.permission !== 'default') return Notification.permission
  return Notification.requestPermission()
}

export function deliverBrowserNotification(notification: EventNotification): boolean {
  if (notificationPermission() !== 'granted') return false
  new Notification(notification.title, { body: notification.message, tag: notification.key })
  return true
}
