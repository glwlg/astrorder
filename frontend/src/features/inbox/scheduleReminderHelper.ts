import type { SessionScheduleInput } from '../../domain/types'
import { browserTimezone, formatWeekdayMask } from '../../hooks/useSessionSchedules'

export type ReminderTriggerMode = 'once' | 'daily' | 'weekdays' | 'weekly'

export const REMINDER_WEEKDAY_ITEMS = [
  { bit: 1, cronDay: 0, label: '日' },
  { bit: 2, cronDay: 1, label: '一' },
  { bit: 4, cronDay: 2, label: '二' },
  { bit: 8, cronDay: 3, label: '三' },
  { bit: 16, cronDay: 4, label: '四' },
  { bit: 32, cronDay: 5, label: '五' },
  { bit: 64, cronDay: 6, label: '六' },
] as const

export const WEEKDAYS_MASK = 62 // Mon(2) + Tue(4) + Wed(8) + Thu(16) + Fri(32)

export interface ReminderDraft {
  name: string
  prompt: string
  mode: ReminderTriggerMode
  time: string
  weekdayMask: number
  onceLocal: string
  sessionKey: string
}

export function defaultReminderOnceLocal(now = new Date()): string {
  const dt = new Date(now.getTime() + 60 * 60 * 1000)
  dt.setSeconds(0, 0)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())}T${pad(dt.getHours())}:${pad(dt.getMinutes())}`
}

export function createEmptyReminderDraft(defaultSessionKey = '', now = new Date()): ReminderDraft {
  return {
    name: '',
    prompt: '',
    mode: 'daily',
    time: '09:00',
    weekdayMask: WEEKDAYS_MASK,
    onceLocal: defaultReminderOnceLocal(now),
    sessionKey: defaultSessionKey,
  }
}

function parseTimeHHMM(raw: string): { hour: number; minute: number; normalized: string } | null {
  const match = raw.trim().match(/^(\d{1,2}):(\d{2})$/)
  if (!match) return null
  const hour = Number(match[1])
  const minute = Number(match[2])
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return null
  }
  return {
    hour,
    minute,
    normalized: `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`,
  }
}

export function reminderToCronExpr(
  draft: Pick<ReminderDraft, 'mode' | 'time' | 'weekdayMask'>,
): string | null {
  if (draft.mode === 'once') return null
  const parsed = parseTimeHHMM(draft.time)
  if (!parsed) return null
  if (draft.mode === 'daily') {
    return `${parsed.minute} ${parsed.hour} * * *`
  }
  if (draft.mode === 'weekdays') {
    return `${parsed.minute} ${parsed.hour} * * 1-5`
  }
  const mask = draft.weekdayMask & 127
  if (mask === 0) return null
  if (mask === 127) return `${parsed.minute} ${parsed.hour} * * *`
  if (mask === WEEKDAYS_MASK) return `${parsed.minute} ${parsed.hour} * * 1-5`
  const days = REMINDER_WEEKDAY_ITEMS.filter((item) => Boolean(mask & item.bit)).map((item) => item.cronDay)
  return `${parsed.minute} ${parsed.hour} * * ${days.join(',')}`
}

export function formatReminderHumanSummary(
  draft: Pick<ReminderDraft, 'mode' | 'time' | 'weekdayMask' | 'onceLocal'>,
): string {
  if (draft.mode === 'once') {
    if (!draft.onceLocal) return '请选择提醒日期和时间'
    return `单次提醒：${draft.onceLocal.replace('T', ' ')}`
  }
  const parsed = parseTimeHHMM(draft.time)
  const timeText = parsed ? parsed.normalized : '--:--'
  if (draft.mode === 'daily') return `每天 ${timeText} 自动提醒`
  if (draft.mode === 'weekdays') return `每个工作日（周一至周五）${timeText} 自动提醒`
  if ((draft.weekdayMask & 127) === 0) return '请至少选择一个星期'
  const daysText = formatWeekdayMask(draft.weekdayMask)
  if (daysText === '每天') return `每天 ${timeText} 自动提醒`
  if (daysText === '工作日') return `每个工作日（周一至周五）${timeText} 自动提醒`
  if (daysText === '周末') return `每周末 ${timeText} 自动提醒`
  return `每${daysText} ${timeText} 自动提醒`
}

export function validateReminderDraft(draft: ReminderDraft, now = new Date()): string | null {
  if (!draft.prompt.trim()) return '请填写提醒内容'
  if (!draft.sessionKey.trim()) return '请选择关联的目标对话'
  if (draft.mode === 'once') {
    if (!draft.onceLocal.trim()) return '请选择提醒日期和时间'
    const dt = new Date(draft.onceLocal)
    if (Number.isNaN(dt.getTime())) return '请选择有效的提醒日期和时间'
    if (dt.getTime() <= now.getTime()) return '一次性提醒时间需晚于当前时间'
    return null
  }
  if (!parseTimeHHMM(draft.time)) return '请选择有效的提醒时间'
  if (draft.mode === 'weekly' && (draft.weekdayMask & 127) === 0) {
    return '请至少选择一个星期'
  }
  return null
}

export function buildReminderSchedulePayload(
  draft: ReminderDraft,
  agentId: string,
  timezone = browserTimezone(),
): SessionScheduleInput {
  const prompt = draft.prompt.trim()
  const firstLine = prompt.split(/\r?\n/)[0]?.trim().slice(0, 40) || '定时提醒'
  const name = draft.name.trim() || firstLine

  if (draft.mode === 'once') {
    const dt = new Date(draft.onceLocal)
    return {
      agent_id: agentId,
      name,
      prompt,
      enabled: true,
      schedule_kind: 'once',
      run_at: Number.isNaN(dt.getTime()) ? '' : dt.toISOString(),
      timezone,
      when_busy: 'skip',
    }
  }

  const parsed = parseTimeHHMM(draft.time)
  const dailyTime = parsed?.normalized || '09:00'

  if (draft.mode === 'daily') {
    return {
      agent_id: agentId,
      name,
      prompt,
      enabled: true,
      schedule_kind: 'daily',
      daily_time: dailyTime,
      timezone,
      when_busy: 'skip',
    }
  }

  if (draft.mode === 'weekdays') {
    return {
      agent_id: agentId,
      name,
      prompt,
      enabled: true,
      schedule_kind: 'weekly',
      daily_time: dailyTime,
      weekday_mask: WEEKDAYS_MASK,
      timezone,
      when_busy: 'skip',
    }
  }

  return {
    agent_id: agentId,
    name,
    prompt,
    enabled: true,
    schedule_kind: 'weekly',
    daily_time: dailyTime,
    weekday_mask: draft.weekdayMask & 127,
    timezone,
    when_busy: 'skip',
  }
}
