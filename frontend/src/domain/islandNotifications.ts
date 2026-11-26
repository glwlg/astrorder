import { useSyncExternalStore } from 'react'
import { scopeKey } from './semantics'
import type { EventNotification } from './types'

const MAX_ISLAND_NOTIFICATIONS = 5

let hostCount = 0
let queue: EventNotification[] = []
const listeners = new Set<() => void>()
const hostListeners = new Set<() => void>()

function emitChange(): void {
  for (const listener of listeners) {
    listener()
  }
}

function emitHostChange(): void {
  for (const listener of hostListeners) {
    listener()
  }
}

export function registerIslandNotificationHost(): () => void {
  hostCount += 1
  emitHostChange()
  return () => {
    hostCount = Math.max(0, hostCount - 1)
    emitHostChange()
  }
}

export function hasIslandNotificationHost(): boolean {
  return hostCount > 0
}

function subscribeHost(listener: () => void): () => void {
  hostListeners.add(listener)
  return () => {
    hostListeners.delete(listener)
  }
}

export function useHasIslandNotificationHost(): boolean {
  return useSyncExternalStore(subscribeHost, hasIslandNotificationHost, hasIslandNotificationHost)
}

export function pushIslandNotification(notification: EventNotification): void {
  const deduped = queue.filter((item) => item.key !== notification.key)
  queue = [notification, ...deduped].slice(0, MAX_ISLAND_NOTIFICATIONS)
  emitChange()
}

export function dismissIslandNotification(key: string): void {
  const next = queue.filter((item) => item.key !== key)
  if (next.length === queue.length) return
  queue = next
  emitChange()
}

export function dismissIslandNotificationsForSession(
  agentId: string,
  sessionId: string,
): void {
  const target = scopeKey(agentId, sessionId)
  const next = queue.filter(
    (item) =>
      item.group_id ||
      scopeKey(item.agent_id, item.session_id) !== target,
  )
  if (next.length === queue.length) return
  queue = next
  emitChange()
}

export function clearIslandNotifications(): void {
  if (queue.length === 0) return
  queue = []
  emitChange()
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

function getSnapshot(): readonly EventNotification[] {
  return queue
}

export function useIslandNotifications(): readonly EventNotification[] {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
