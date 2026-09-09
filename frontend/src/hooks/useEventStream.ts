import { useEffect } from 'react'
import { notifications as mantineNotifications } from '@mantine/notifications'
import type { QueryClient } from '@tanstack/react-query'
import { connectEventStream, type EventStreamStatus } from '../api/eventStream'
import { deliverBrowserNotification, notificationForEvent } from '../domain/notifications'
import { useAstrorderStore } from '../state/store'
import { api } from '../api/client'
import { nativeActivityEvent } from './useSessionOrder'

function storeStatus(status: EventStreamStatus) {
  const connection = status === 'connected' ? 'connected' : status === 'error' ? 'error' : status
  useAstrorderStore.getState().setConnection(connection)
}

export function useEventStream(authenticated: boolean, queryClient: QueryClient): void {
  useEffect(() => {
    if (!authenticated) return
    let stopped = false
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const result = await api.getUserActivity()
        if (!stopped) window.dispatchEvent(new CustomEvent(nativeActivityEvent, { detail: result.items }))
      } catch { /* A temporarily unavailable source must not discard its last activity. */ }
      if (!stopped) timer = setTimeout(() => void poll(), 5000)
    }
    void poll()
    return () => { stopped = true; clearTimeout(timer) }
  }, [authenticated])
  useEffect(() => {
    if (!authenticated) {
      useAstrorderStore.getState().setConnection('disconnected')
      return undefined
    }

    const stop = connectEventStream({
      after: useAstrorderStore.getState().cursor,
      onStatus: storeStatus,
      onEvent: (event) => {
        const store = useAstrorderStore.getState()
        const notification = event.cursor > store.cursor ? notificationForEvent(event) : null
        if (notification && store.addNotification(notification)) {
          mantineNotifications.show({
            id: notification.key,
            title: notification.title,
            message: notification.message,
            color: notification.kind === 'task_failed' ? 'red' : notification.kind === 'approval_pending' ? 'yellow' : 'teal',
            autoClose: 8000,
          })
          deliverBrowserNotification(notification)
        }
        store.applyEvent(event)
        if (useAstrorderStore.getState().resyncRequired) {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'messages'] })
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands'] })
        }
      },
    })
    return stop
  }, [authenticated, queryClient])
}
