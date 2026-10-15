import { useEffect } from 'react'
import { notifications } from '@mantine/notifications'
import { useAstrorderStore } from '../state/store'
import { sharedServerOutbox } from '../features/mobile/serverOutbox'

/** Migrate all locally staged sessions even when no conversation is mounted. */
export function useServerOutboxSync(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return
    let stopped = false
    const outbox = sharedServerOutbox()
    const migrate = () => {
      if (stopped || useAstrorderStore.getState().connection !== 'connected') return
      void outbox.load().then(() => outbox.migrate()).catch(error => {
        if (!stopped) notifications.show({ id: 'queue-sync-error', color: 'red', message: error instanceof Error ? error.message : '待发消息同步失败' })
      })
    }
    const unsubscribe = useAstrorderStore.subscribe((state, previous) => {
      if (state.connection !== previous.connection) migrate()
    })
    const timer = setInterval(migrate, 10000)
    window.addEventListener('online', migrate)
    migrate()
    return () => { stopped = true; unsubscribe(); clearInterval(timer); window.removeEventListener('online', migrate) }
  }, [enabled])
}
