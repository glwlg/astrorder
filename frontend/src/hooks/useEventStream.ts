import { useEffect } from 'react'
import type { QueryClient } from '@tanstack/react-query'
import { connectEventStream, type EventStreamStatus } from '../api/eventStream'
import { useAstrorderStore } from '../state/store'

function storeStatus(status: EventStreamStatus) {
  const connection = status === 'connected' ? 'connected' : status === 'error' ? 'error' : status
  useAstrorderStore.getState().setConnection(connection)
}

export function useEventStream(authenticated: boolean, queryClient: QueryClient): void {
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
