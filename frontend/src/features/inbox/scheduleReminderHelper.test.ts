import { describe, expect, it } from 'vitest'
import {
  buildReminderSchedulePayload,
  createEmptyReminderDraft,
  formatReminderHumanSummary,
  reminderToCronExpr,
  validateReminderDraft,
  WEEKDAYS_MASK,
} from './scheduleReminderHelper'

describe('scheduleReminderHelper', () => {
  it('converts daily, weekdays, weekly, and once reminders to expected cron expressions', () => {
    expect(reminderToCronExpr({ mode: 'daily', time: '09:30', weekdayMask: 0 })).toBe('30 9 * * *')
    expect(reminderToCronExpr({ mode: 'weekdays', time: '08:00', weekdayMask: 0 })).toBe('0 8 * * 1-5')
    // Mon(2) + Wed(8) + Fri(32) = 42
    expect(reminderToCronExpr({ mode: 'weekly', time: '14:15', weekdayMask: 2 | 8 | 32 })).toBe('15 14 * * 1,3,5')
    expect(reminderToCronExpr({ mode: 'once', time: '09:00', weekdayMask: 0 })).toBeNull()
  })

  it('builds backend-compatible SessionScheduleInput payloads for all trigger modes', () => {
    const base = {
      ...createEmptyReminderDraft('agent-1::sess-1'),
      name: '',
      prompt: '整理今日待办与明日议程\n并同步给团队',
      time: '08:45',
    }

    const daily = buildReminderSchedulePayload({ ...base, mode: 'daily' }, 'agent-1', 'Asia/Shanghai')
    expect(daily).toEqual({
      agent_id: 'agent-1',
      name: '整理今日待办与明日议程',
      prompt: '整理今日待办与明日议程\n并同步给团队',
      enabled: true,
      schedule_kind: 'daily',
      daily_time: '08:45',
      timezone: 'Asia/Shanghai',
      when_busy: 'skip',
    })

    const weekdays = buildReminderSchedulePayload(
      { ...base, name: '工作日早会提醒', mode: 'weekdays' },
      'agent-1',
      'Asia/Shanghai',
    )
    expect(weekdays).toMatchObject({
      name: '工作日早会提醒',
      schedule_kind: 'weekly',
      daily_time: '08:45',
      weekday_mask: WEEKDAYS_MASK,
    })

    const weekly = buildReminderSchedulePayload(
      { ...base, mode: 'weekly', weekdayMask: 2 | 16 }, // Mon + Thu
      'agent-1',
      'Asia/Shanghai',
    )
    expect(weekly).toMatchObject({
      schedule_kind: 'weekly',
      daily_time: '08:45',
      weekday_mask: 18,
    })

    const once = buildReminderSchedulePayload(
      { ...base, mode: 'once', onceLocal: '2026-12-01T10:30' },
      'agent-1',
      'Asia/Shanghai',
    )
    expect(once.schedule_kind).toBe('once')
    expect(once.run_at).toBe(new Date('2026-12-01T10:30').toISOString())
  })

  it('validates reminder drafts and formats human-readable summaries', () => {
    const now = new Date('2026-11-27T10:00:00Z')
    const draft = createEmptyReminderDraft('agent-1::sess-1', now)
    expect(validateReminderDraft(draft, now)).toBe('请填写提醒内容')
    expect(validateReminderDraft({ ...draft, prompt: '发周报', sessionKey: '' }, now)).toBe('请选择关联的目标对话')
    expect(
      validateReminderDraft(
        { ...draft, prompt: '开会', mode: 'once', onceLocal: '2026-11-27T09:00:00Z' },
        now,
      ),
    ).toBe('一次性提醒时间需晚于当前时间')
    expect(
      validateReminderDraft({ ...draft, prompt: '周例会', mode: 'weekly', weekdayMask: 0 }, now),
    ).toBe('请至少选择一个星期')
    expect(
      validateReminderDraft({ ...draft, prompt: '发周报', mode: 'weekdays', time: '09:30' }, now),
    ).toBeNull()

    expect(formatReminderHumanSummary({ mode: 'daily', time: '09:30', weekdayMask: 0, onceLocal: '' })).toBe(
      '每天 09:30 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekdays', time: '09:00', weekdayMask: 62, onceLocal: '' })).toBe(
      '每个工作日（周一至周五）09:00 自动提醒',
    )
    // Weekly mode must output natural Chinese without duplicated "每周工作日" / "每周每天"
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '09:00', weekdayMask: 62, onceLocal: '' })).toBe(
      '每个工作日（周一至周五）09:00 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '10:00', weekdayMask: 65, onceLocal: '' })).toBe(
      '每周末 10:00 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '08:00', weekdayMask: 127, onceLocal: '' })).toBe(
      '每天 08:00 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '14:30', weekdayMask: 2, onceLocal: '' })).toBe(
      '每周一 14:30 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '14:30', weekdayMask: 2 | 8, onceLocal: '' })).toBe(
      '每周一、周三 14:30 自动提醒',
    )
    expect(formatReminderHumanSummary({ mode: 'weekly', time: '09:00', weekdayMask: 0, onceLocal: '' })).toBe(
      '请至少选择一个星期',
    )
  })
})
