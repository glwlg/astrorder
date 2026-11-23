import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Session, SessionSchedule } from '../domain/types'

export function useSessionSchedules(session: Session | null | undefined) {
  return useQuery({
    queryKey: ['astrorder', 'session-schedules', session?.agent_id, session?.id],
    queryFn: async () => {
      const res = await api.listSessionSchedules(session!.id, session!.agent_id)
      return res.items || []
    },
    enabled: Boolean(session?.id && session?.agent_id),
    staleTime: 10000,
    retry: false,
    refetchOnWindowFocus: false,
  })
}

export function useAllSchedules(enabled: boolean) {
  return useQuery({
    queryKey: ['astrorder', 'session-schedules', 'all'],
    queryFn: async () => {
      const res = await api.listAllSchedules()
      return res.items || []
    },
    enabled,
    staleTime: 5000,
    retry: false,
    refetchOnWindowFocus: false,
  })
}

const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六']

export function formatWeekdayMask(mask: number): string {
  if (mask === 62) return '工作日'
  if (mask === 65) return '周末'
  if (mask === 127) return '每天'
  const picked: string[] = []
  for (let i = 0; i < 7; i++) {
    if (mask & (1 << i)) picked.push(`周${WEEKDAY_LABELS[i]}`)
  }
  return picked.join('、') || '每周'
}

export function formatScheduleSummary(item: Pick<SessionSchedule, 'schedule_kind' | 'daily_time' | 'weekday_mask' | 'interval_minutes' | 'run_at' | 'cron_expr'>): string {
  switch (item.schedule_kind) {
    case 'daily':
      return `每天 ${item.daily_time || '09:00'}`
    case 'weekly':
      return `${formatWeekdayMask(item.weekday_mask)} ${item.daily_time || '09:00'}`
    case 'interval': {
      const mins = item.interval_minutes || 60
      if (mins >= 1440 && mins % 1440 === 0) return `每 ${mins / 1440} 天`
      if (mins >= 60 && mins % 60 === 0) return `每 ${mins / 60} 小时`
      return `每 ${mins} 分钟`
    }
    case 'once': {
      if (!item.run_at) return '一次性'
      const dt = new Date(item.run_at)
      if (Number.isNaN(dt.getTime())) return '一次性'
      return `单次 ${formatShortDateTime(dt)}`
    }
    case 'cron':
      return `Cron: ${item.cron_expr}`
    default:
      return item.schedule_kind
  }
}

export function formatShortDateTime(date: Date): string {
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const mm = String(date.getMinutes()).padStart(2, '0')
  return `${m}-${d} ${hh}:${mm}`
}

export function formatNextRunChip(iso?: string): string {
  if (!iso) return ''
  const dt = new Date(iso)
  if (Number.isNaN(dt.getTime())) return ''
  const diffMs = dt.getTime() - Date.now()
  if (diffMs <= 0) return '即将执行'
  const diffMin = Math.round(diffMs / 60000)
  if (diffMin < 1) return '<1 分钟后'
  if (diffMin < 60) return `${diffMin} 分钟后`
  const now = new Date()
  const hh = String(dt.getHours()).padStart(2, '0')
  const mm = String(dt.getMinutes()).padStart(2, '0')
  const sameDay =
    dt.getFullYear() === now.getFullYear() &&
    dt.getMonth() === now.getMonth() &&
    dt.getDate() === now.getDate()
  if (sameDay) return `今天 ${hh}:${mm}`
  const tomorrow = new Date(now)
  tomorrow.setDate(now.getDate() + 1)
  const isTomorrow =
    dt.getFullYear() === tomorrow.getFullYear() &&
    dt.getMonth() === tomorrow.getMonth() &&
    dt.getDate() === tomorrow.getDate()
  if (isTomorrow) return `明天 ${hh}:${mm}`
  return formatShortDateTime(dt)
}

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'Local'
  } catch {
    return 'Local'
  }
}
