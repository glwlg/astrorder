import { useEffect, useEffectEvent } from 'react'
import { useLocalStorage } from '@mantine/hooks'

export type RefreshScope = 'all' | 'current'
export type RefreshSettings = Record<RefreshScope, number>
export const refreshMinutes = [0, 1, 5, 10, 15, 30, 60]

export function useRefreshSettings(platform: string) {
  return useLocalStorage<RefreshSettings>({ key: `astrorder:account-refresh:${platform}:v1`,
    defaultValue: { all: 0, current: 0 }, getInitialValueInEffect: false })
}

export function useScheduledRefresh(environment: string, settings: RefreshSettings, refresh: (scope: RefreshScope) => void) {
  const onTick = useEffectEvent(refresh)
  const { all, current } = settings
  useEffect(() => {
    const ms = (minutes: number) => minutes > 0 && refreshMinutes.includes(minutes) ? minutes * 60_000 : Infinity
    const allMs = ms(all), currentMs = ms(current)
    if (!environment || Math.min(allMs, currentMs) === Infinity) return
    let timer: ReturnType<typeof setTimeout>
    function start() {
      clearTimeout(timer)
      if (document.hidden) return
      let nextAll = Date.now() + allMs, nextCurrent = Date.now() + currentMs
      function schedule() {
        timer = setTimeout(() => {
          if (document.hidden) return
          const now = Date.now()
          const full = now >= nextAll
          if (full) nextAll = now + allMs
          if (now >= nextCurrent || full) nextCurrent = now + currentMs
          onTick(full ? 'all' : 'current')
          schedule()
        }, Math.max(0, Math.min(nextAll, nextCurrent) - Date.now()))
      }
      schedule()
    }
    start()
    document.addEventListener('visibilitychange', start)
    return () => { clearTimeout(timer); document.removeEventListener('visibilitychange', start) }
  }, [all, current, environment])
}
