import { useEffect, useEffectEvent, useMemo } from 'react'
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
  const deadlines = useMemo(() => new Map<string, { all: number; current: number }>(), [all, current])
  useEffect(() => {
    const ms = (minutes: number) => minutes > 0 && refreshMinutes.includes(minutes) ? minutes * 60_000 : Infinity
    const allMs = ms(all), currentMs = ms(current)
    if (!environment || Math.min(allMs, currentMs) === Infinity) return
    let timer: ReturnType<typeof setTimeout>
    const due = deadlines.get(environment) ?? { all: Date.now() + allMs, current: Date.now() + currentMs }
    deadlines.set(environment, due)
    function schedule() {
      clearTimeout(timer)
      if (document.hidden) return
      const now = Date.now()
      if (now >= Math.min(due.all, due.current)) {
        const full = now >= due.all
        if (full) due.all = now + allMs
        due.current = now + currentMs
        onTick(full ? 'all' : 'current')
      }
      timer = setTimeout(schedule, Math.max(0, Math.min(due.all, due.current) - Date.now()))
    }
    schedule()
    document.addEventListener('visibilitychange', schedule)
    return () => { clearTimeout(timer); document.removeEventListener('visibilitychange', schedule) }
  }, [all, current, deadlines, environment])
}
