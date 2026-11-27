import { useEffect, useMemo, useState } from 'react'
import { Modal, Switch, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQueryClient } from '@tanstack/react-query'
import {
  IconArrowLeft,
  IconArrowUpRight,
  IconClock,
  IconEdit,
  IconListDetails,
  IconPlayerPlay,
  IconPlus,
  IconTrash,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import type {
  Agent,
  Session,
  SessionSchedule,
  SessionScheduleKind,
  SessionScheduleWhenBusy,
} from '../../domain/types'
import {
  browserTimezone,
  formatNextRunChip,
  formatScheduleSummary,
  formatShortDateTime,
  useAllSchedules,
} from '../../hooks/useSessionSchedules'
import { isAntigravityAgent } from '../../hooks/useSessionSwarm'
import { resolveSessionDisplayTitle } from '../../components/sessionRailModel'
import { useAstrorderStore } from '../../state/store'
import './workspaceChanges.css'

export interface SessionSchedulesViewProps {
  session: Session
  agent?: Agent
  schedules: SessionSchedule[]
  loading?: boolean
  error?: unknown
  onOpenSession?: (agentId: string, sessionId: string) => void
  compact?: boolean
}

type ScheduleMode = 'daily' | 'weekdays' | 'weekly' | 'interval' | 'once' | 'cron'

interface FormState {
  editingId: string | null
  name: string
  prompt: string
  mode: ScheduleMode
  dailyTime: string
  weekdayMask: number
  intervalMinutes: number
  onceLocal: string
  cronExpr: string
  whenBusy: SessionScheduleWhenBusy
}

const WEEKDAY_ITEMS = [
  { bit: 1, label: '日' },
  { bit: 2, label: '一' },
  { bit: 4, label: '二' },
  { bit: 8, label: '三' },
  { bit: 16, label: '四' },
  { bit: 32, label: '五' },
  { bit: 64, label: '六' },
]

const INTERVAL_PRESETS = [
  { mins: 15, label: '15 分钟' },
  { mins: 30, label: '30 分钟' },
  { mins: 60, label: '1 小时' },
  { mins: 360, label: '6 小时' },
]

const STATUS_META: Record<string, { label: string; tone: 'ok' | 'warn' | 'err' | 'muted' }> = {
  sent: { label: '已发送', tone: 'ok' },
  queued: { label: '已排队', tone: 'ok' },
  skipped_busy: { label: '正忙跳过', tone: 'warn' },
  skipped_offline: { label: '离线跳过', tone: 'warn' },
  skipped_misfire: { label: '超时跳过', tone: 'warn' },
  failed: { label: '发送失败', tone: 'err' },
  paused_circuit_breaker: { label: '连续失败已暂停', tone: 'err' },
}

function defaultOnceLocalValue(): string {
  const dt = new Date(Date.now() + 60 * 60 * 1000)
  dt.setSeconds(0, 0)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())}T${pad(dt.getHours())}:${pad(dt.getMinutes())}`
}

function isoToLocalInput(iso?: string): string {
  if (!iso) return defaultOnceLocalValue()
  const dt = new Date(iso)
  if (Number.isNaN(dt.getTime())) return defaultOnceLocalValue()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${dt.getFullYear()}-${pad(dt.getMonth() + 1)}-${pad(dt.getDate())}T${pad(dt.getHours())}:${pad(dt.getMinutes())}`
}

function emptyForm(): FormState {
  return {
    editingId: null,
    name: '',
    prompt: '',
    mode: 'daily',
    dailyTime: '09:00',
    weekdayMask: 62,
    intervalMinutes: 60,
    onceLocal: defaultOnceLocalValue(),
    cronExpr: '0 9 * * 1-5',
    whenBusy: 'skip',
  }
}

function formFromSchedule(item: SessionSchedule): FormState {
  let mode: ScheduleMode = 'daily'
  if (item.schedule_kind === 'weekly') {
    mode = item.weekday_mask === 62 ? 'weekdays' : 'weekly'
  } else if (
    item.schedule_kind === 'interval' ||
    item.schedule_kind === 'once' ||
    item.schedule_kind === 'cron'
  ) {
    mode = item.schedule_kind
  }
  return {
    editingId: item.id,
    name: item.name,
    prompt: item.prompt,
    mode,
    dailyTime: item.daily_time || '09:00',
    weekdayMask: item.weekday_mask || 62,
    intervalMinutes: item.interval_minutes || 60,
    onceLocal: isoToLocalInput(item.run_at),
    cronExpr: item.cron_expr || '0 9 * * 1-5',
    whenBusy: item.when_busy === 'queue' ? 'queue' : 'skip',
  }
}

function buildSpecPayload(form: FormState): {
  schedule_kind: SessionScheduleKind
  cron_expr?: string
  interval_minutes?: number
  daily_time?: string
  weekday_mask?: number
  run_at?: string
  timezone: string
} {
  const timezone = browserTimezone()
  switch (form.mode) {
    case 'daily':
      return { schedule_kind: 'daily', daily_time: form.dailyTime, timezone }
    case 'weekdays':
      return { schedule_kind: 'weekly', daily_time: form.dailyTime, weekday_mask: 62, timezone }
    case 'weekly':
      return {
        schedule_kind: 'weekly',
        daily_time: form.dailyTime,
        weekday_mask: form.weekdayMask,
        timezone,
      }
    case 'interval':
      return {
        schedule_kind: 'interval',
        interval_minutes: Number(form.intervalMinutes) || 0,
        timezone,
      }
    case 'once': {
      const dt = new Date(form.onceLocal)
      return {
        schedule_kind: 'once',
        run_at: Number.isNaN(dt.getTime()) ? '' : dt.toISOString(),
        timezone,
      }
    }
    case 'cron':
      return { schedule_kind: 'cron', cron_expr: form.cronExpr.trim(), timezone }
  }
}

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err || '操作失败')
}

function formatRunPreview(iso: string): string {
  const dt = new Date(iso)
  if (Number.isNaN(dt.getTime())) return iso
  const weekday = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'][dt.getDay()]
  return `${formatShortDateTime(dt)} (${weekday})`
}

export function SessionSchedulesView({
  session,
  agent,
  schedules,
  loading = false,
  error,
  onOpenSession,
  compact = false,
}: SessionSchedulesViewProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<FormState | null>(null)
  const [saving, setSaving] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null)
  const [allModalOpen, setAllModalOpen] = useState(false)

  const [previewRuns, setPreviewRuns] = useState<string[]>([])
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)

  const allSchedulesQuery = useAllSchedules(allModalOpen)

  const invalidateAll = async () => {
    await queryClient.invalidateQueries({ queryKey: ['astrorder', 'session-schedules'] })
  }

  const specPayload = useMemo(() => (form ? buildSpecPayload(form) : null), [form])

  useEffect(() => {
    if (!specPayload) {
      setPreviewRuns([])
      setPreviewError(null)
      return
    }
    let cancelled = false
    setPreviewLoading(true)
    const timer = window.setTimeout(() => {
      void api
        .previewSessionSchedule({ ...specPayload, count: 3 })
        .then((res) => {
          if (cancelled) return
          setPreviewRuns(res.next_runs || [])
          setPreviewError(null)
        })
        .catch((err) => {
          if (cancelled) return
          setPreviewRuns([])
          setPreviewError(errorText(err))
        })
        .finally(() => {
          if (!cancelled) setPreviewLoading(false)
        })
    }, 180)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [specPayload])

  const showApprovalHint = useMemo(() => {
    if (!isAntigravityAgent(agent)) return false
    const cachedMode = queryClient.getQueryData<string>([
      'astrorder',
      'approval-mode',
      session.agent_id,
      session.id,
    ])
    const mode = (cachedMode || session.selected_approval_mode || '').toLowerCase()
    return mode === 'manual'
  }, [agent, queryClient, session.agent_id, session.id, session.selected_approval_mode])

  const handleSave = async () => {
    if (!form || saving) return
    const prompt = form.prompt.trim()
    if (!prompt) {
      notifications.show({ color: 'red', message: '请填写定时发送的提示词' })
      return
    }
    const spec = buildSpecPayload(form)
    setSaving(true)
    try {
      if (form.editingId) {
        await api.updateSessionSchedule(form.editingId, {
          name: form.name.trim(),
          prompt,
          when_busy: form.whenBusy,
          ...spec,
        })
        notifications.show({ color: 'teal', message: '已更新定时任务' })
      } else {
        await api.createSessionSchedule(session.id, {
          agent_id: session.agent_id,
          name: form.name.trim(),
          prompt,
          enabled: true,
          when_busy: form.whenBusy,
          ...spec,
        })
        notifications.show({ color: 'teal', message: '已创建定时任务' })
      }
      await invalidateAll()
      setForm(null)
    } catch (err) {
      notifications.show({ color: 'red', message: `保存失败：${errorText(err)}` })
    } finally {
      setSaving(false)
    }
  }

  const handleToggle = async (item: SessionSchedule, nextEnabled: boolean) => {
    setBusyId(item.id)
    try {
      await api.updateSessionSchedule(item.id, { enabled: nextEnabled })
      await invalidateAll()
    } catch (err) {
      notifications.show({ color: 'red', message: `切换失败：${errorText(err)}` })
    } finally {
      setBusyId(null)
    }
  }

  const handleRunNow = async (item: SessionSchedule) => {
    setBusyId(item.id)
    try {
      const res = await api.runSessionScheduleNow(item.id)
      await invalidateAll()
      if (res.outcome === 'sent') {
        notifications.show({ color: 'teal', message: `已立即发送「${item.name}」` })
      } else if (res.outcome === 'queued') {
        notifications.show({ color: 'teal', message: `会话正忙，「${item.name}」已加入待发队列` })
      } else if (res.outcome === 'skipped_busy') {
        notifications.show({ color: 'yellow', message: '会话正在运行，按当前忙时策略已跳过' })
      } else if (res.outcome === 'skipped_offline') {
        notifications.show({ color: 'yellow', message: `环境离线已跳过：${res.detail || ''}` })
      } else {
        notifications.show({ color: 'red', message: `发送失败：${res.detail || res.outcome}` })
      }
    } catch (err) {
      notifications.show({ color: 'red', message: `立即发送失败：${errorText(err)}` })
    } finally {
      setBusyId(null)
    }
  }

  const handleDelete = async (item: SessionSchedule) => {
    setBusyId(item.id)
    try {
      await api.deleteSessionSchedule(item.id)
      setConfirmDeleteId(null)
      await invalidateAll()
      notifications.show({ color: 'teal', message: `已删除定时任务「${item.name}」` })
    } catch (err) {
      notifications.show({ color: 'red', message: `删除失败：${errorText(err)}` })
    } finally {
      setBusyId(null)
    }
  }

  if (form) {
    const canSave = Boolean(form.prompt.trim()) && !previewError && !saving
    return (
      <div className={`sched-panel${compact ? ' is-compact' : ''}`}>
        <div className="swarm-detail-nav">
          <button
            type="button"
            className="swarm-back-btn"
            onClick={() => setForm(null)}
            disabled={saving}
          >
            <IconArrowLeft size={14} />
            <span>返回列表</span>
          </button>
          <span className="sched-form-title">
            {form.editingId ? '编辑定时任务' : '新建定时任务'}
          </span>
        </div>

        <div className="sched-form-body">
          {showApprovalHint && (
            <div className="sched-approval-tip">
              当前审批模式需手动确认，定时触发若需自动执行工具建议切换为自动审批。
            </div>
          )}

          <label className="sched-field">
            <span>任务名称（可选）</span>
            <input
              type="text"
              className="sched-input"
              placeholder="例如：每日晨报巡检（留空自动取提示词首行）"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </label>

          <label className="sched-field">
            <span>到点发送的提示词</span>
            <textarea
              className="sched-textarea"
              rows={3}
              placeholder="到时间后将作为一条消息直接发进当前会话…"
              value={form.prompt}
              onChange={(e) => setForm({ ...form, prompt: e.target.value })}
            />
          </label>

          <div className="sched-field">
            <span>触发频率</span>
            <div className="sched-mode-pills" role="radiogroup" aria-label="触发频率">
              {(
                [
                  ['daily', '每天'],
                  ['weekdays', '工作日'],
                  ['weekly', '每周'],
                  ['interval', '按间隔'],
                  ['once', '一次性'],
                  ['cron', 'Cron'],
                ] as const
              ).map(([value, label]) => (
                <button
                  key={value}
                  type="button"
                  role="radio"
                  aria-checked={form.mode === value}
                  className={`sched-mode-pill${form.mode === value ? ' is-active' : ''}`}
                  onClick={() => setForm({ ...form, mode: value })}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>

          {(form.mode === 'daily' || form.mode === 'weekdays' || form.mode === 'weekly') && (
            <div className="sched-inline-row">
              <label className="sched-field is-grow">
                <span>触发时间</span>
                <input
                  type="time"
                  className="sched-input"
                  value={form.dailyTime}
                  onChange={(e) => setForm({ ...form, dailyTime: e.target.value })}
                />
              </label>
            </div>
          )}

          {form.mode === 'weekly' && (
            <div className="sched-weekdays" aria-label="选择星期">
              {WEEKDAY_ITEMS.map((day) => {
                const active = Boolean(form.weekdayMask & day.bit)
                return (
                  <button
                    key={day.bit}
                    type="button"
                    aria-pressed={active}
                    className={`sched-weekday-btn${active ? ' is-active' : ''}`}
                    onClick={() => {
                      const next = active
                        ? form.weekdayMask & ~day.bit
                        : form.weekdayMask | day.bit
                      setForm({ ...form, weekdayMask: next || day.bit })
                    }}
                  >
                    {day.label}
                  </button>
                )
              })}
            </div>
          )}

          {form.mode === 'interval' && (
            <div className="sched-field">
              <div className="sched-interval-presets">
                {INTERVAL_PRESETS.map((preset) => (
                  <button
                    key={preset.mins}
                    type="button"
                    className={`sched-mini-chip${form.intervalMinutes === preset.mins ? ' is-active' : ''}`}
                    onClick={() => setForm({ ...form, intervalMinutes: preset.mins })}
                  >
                    {preset.label}
                  </button>
                ))}
              </div>
              <div className="sched-inline-input">
                <span>每隔</span>
                <input
                  type="number"
                  min={1}
                  max={43200}
                  className="sched-input is-narrow"
                  value={form.intervalMinutes}
                  onChange={(e) =>
                    setForm({ ...form, intervalMinutes: Math.max(1, Number(e.target.value) || 1) })
                  }
                />
                <span>分钟发送一次</span>
              </div>
            </div>
          )}

          {form.mode === 'once' && (
            <label className="sched-field">
              <span>触发日期与时间</span>
              <input
                type="datetime-local"
                className="sched-input"
                value={form.onceLocal}
                onChange={(e) => setForm({ ...form, onceLocal: e.target.value })}
              />
            </label>
          )}

          {form.mode === 'cron' && (
            <label className="sched-field">
              <span>5 段 Cron 表达式（分 时 日 月 周）</span>
              <input
                type="text"
                className="sched-input is-mono"
                placeholder="0 9 * * 1-5"
                value={form.cronExpr}
                onChange={(e) => setForm({ ...form, cronExpr: e.target.value })}
              />
            </label>
          )}

          <div className="sched-preview-box">
            <span className="sched-preview-label">未来触发预览（{browserTimezone()}）</span>
            {previewLoading ? (
              <span className="sched-preview-muted">计算中…</span>
            ) : previewError ? (
              <span className="sched-preview-error">{previewError}</span>
            ) : previewRuns.length === 0 ? (
              <span className="sched-preview-error">所选时间已过期，请选择未来时间</span>
            ) : (
              <ul className="sched-preview-list">
                {previewRuns.map((iso) => (
                  <li key={iso}>{formatRunPreview(iso)}</li>
                ))}
              </ul>
            )}
          </div>

          <div className="sched-field">
            <span>会话正忙时</span>
            <div className="sched-busy-row">
              <button
                type="button"
                className={`sched-mode-pill${form.whenBusy === 'skip' ? ' is-active' : ''}`}
                onClick={() => setForm({ ...form, whenBusy: 'skip' })}
              >
                跳过本轮（推荐）
              </button>
              <button
                type="button"
                className={`sched-mode-pill${form.whenBusy === 'queue' ? ' is-active' : ''}`}
                onClick={() => setForm({ ...form, whenBusy: 'queue' })}
              >
                排队等待空闲
              </button>
            </div>
          </div>
        </div>

        <div className="sched-form-footer">
          <button
            type="button"
            className="swarm-pill-btn"
            onClick={() => setForm(null)}
            disabled={saving}
          >
            取消
          </button>
          <button
            type="button"
            className="swarm-primary-btn"
            onClick={() => void handleSave()}
            disabled={!canSave}
          >
            {saving ? '保存中…' : '保存定时'}
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className={`sched-panel${compact ? ' is-compact' : ''}`}>
      <div className="sched-toolbar">
        <button
          type="button"
          className="swarm-primary-btn"
          onClick={() => setForm(emptyForm())}
        >
          <IconPlus size={14} />
          <span>新建定时</span>
        </button>
        <button
          type="button"
          className="swarm-pill-btn"
          onClick={() => setAllModalOpen(true)}
        >
          <IconListDetails size={14} />
          <span>全部会话定时</span>
        </button>
      </div>

      {showApprovalHint && schedules.some((s) => s.enabled) && (
        <div className="sched-approval-tip">
          当前审批模式需手动确认，定时触发若需自动执行工具建议切换为自动审批。
        </div>
      )}

      {loading && schedules.length === 0 ? (
        <p className="workbench-empty">正在读取定时任务…</p>
      ) : error && schedules.length === 0 ? (
        <div className="swarm-empty-card">
          <p className="swarm-empty-title">定时任务读取失败</p>
          <p className="swarm-empty-desc">{errorText(error)}</p>
        </div>
      ) : schedules.length === 0 ? (
        <div className="swarm-empty-card">
          <IconClock size={26} className="swarm-empty-icon" />
          <p className="swarm-empty-title">当前会话还没有定时任务</p>
          <p className="swarm-empty-desc">
            到时间后自动把预设提示词发进当前会话，适合定时巡检、晨报总结或周期性任务提醒。
          </p>
          <button
            type="button"
            className="swarm-primary-btn"
            onClick={() => setForm(emptyForm())}
          >
            <IconPlus size={14} />
            <span>新建第一个定时任务</span>
          </button>
        </div>
      ) : (
        <div className="sched-list">
          {schedules.map((item) => {
            const statusInfo = item.last_status ? STATUS_META[item.last_status] : undefined
            const nextLabel = item.enabled ? formatNextRunChip(item.next_run_at) : ''
            const isBusy = busyId === item.id
            const isConfirming = confirmDeleteId === item.id
            return (
              <article
                key={item.id}
                className={`sched-card${item.enabled ? '' : ' is-paused'}`}
              >
                <div className="sched-card-top">
                  <strong title={item.name}>{item.name}</strong>
                  <Switch
                    size="xs"
                    color="violet"
                    checked={item.enabled}
                    disabled={isBusy}
                    aria-label={`${item.enabled ? '暂停' : '启用'}定时任务 ${item.name}`}
                    onChange={(e) => void handleToggle(item, e.currentTarget.checked)}
                  />
                </div>

                <div className="swarm-meta-chips">
                  <span className="swarm-meta-chip">{formatScheduleSummary(item)}</span>
                  {nextLabel && (
                    <span className="swarm-meta-chip is-accent">下次 {nextLabel}</span>
                  )}
                  {!item.enabled && item.pause_reason === 'completed' && (
                    <span className="swarm-meta-chip">已完成</span>
                  )}
                  <span className="swarm-meta-chip">
                    {item.when_busy === 'queue' ? '忙时排队' : '忙时跳过'}
                  </span>
                </div>

                <p className="sched-prompt-snippet">{item.prompt}</p>

                {isConfirming ? (
                  <div className="swarm-confirm-bar">
                    <span>确认删除此定时任务？</span>
                    <div className="swarm-confirm-actions">
                      <button
                        type="button"
                        className="swarm-pill-btn"
                        onClick={() => setConfirmDeleteId(null)}
                        disabled={isBusy}
                      >
                        取消
                      </button>
                      <button
                        type="button"
                        className="swarm-danger-btn is-solid"
                        onClick={() => void handleDelete(item)}
                        disabled={isBusy}
                      >
                        删除
                      </button>
                    </div>
                  </div>
                ) : (
                  <div className="sched-card-foot">
                    <div className="sched-status-wrap">
                      {statusInfo ? (
                        <Tooltip
                          label={item.last_error || (item.last_run_at ? `上次触发：${formatShortDateTime(new Date(item.last_run_at))}` : statusInfo.label)}
                          multiline
                          maw={260}
                        >
                          <span className={`sched-status-pill is-${statusInfo.tone}`}>
                            {statusInfo.label}
                          </span>
                        </Tooltip>
                      ) : (
                        <span className="sched-status-pill is-muted">尚未触发</span>
                      )}
                    </div>
                    <div className="sched-card-actions">
                      <Tooltip label="立即发一次">
                        <button
                          type="button"
                          className="swarm-icon-link"
                          aria-label={`立即发送 ${item.name}`}
                          disabled={isBusy}
                          onClick={() => void handleRunNow(item)}
                        >
                          <IconPlayerPlay size={14} />
                        </button>
                      </Tooltip>
                      <Tooltip label="编辑">
                        <button
                          type="button"
                          className="swarm-icon-link"
                          aria-label={`编辑 ${item.name}`}
                          disabled={isBusy}
                          onClick={() => setForm(formFromSchedule(item))}
                        >
                          <IconEdit size={14} />
                        </button>
                      </Tooltip>
                      <Tooltip label="删除">
                        <button
                          type="button"
                          className="swarm-icon-link is-danger"
                          aria-label={`删除 ${item.name}`}
                          disabled={isBusy}
                          onClick={() => setConfirmDeleteId(item.id)}
                        >
                          <IconTrash size={14} />
                        </button>
                      </Tooltip>
                    </div>
                  </div>
                )}
              </article>
            )
          })}
        </div>
      )}

      <Modal
        opened={allModalOpen}
        onClose={() => setAllModalOpen(false)}
        title="全部会话定时任务"
        centered
        size="lg"
        radius="md"
      >
        <div className="sched-all-modal">
          {allSchedulesQuery.isPending ? (
            <p className="workbench-empty">正在读取全部定时任务…</p>
          ) : (allSchedulesQuery.data?.length || 0) === 0 ? (
            <p className="workbench-empty">当前还没有任何会话配置定时任务</p>
          ) : (
            <div className="sched-all-list">
              {(allSchedulesQuery.data || []).map((item) => {
                const statusInfo = item.last_status ? STATUS_META[item.last_status] : undefined
                return (
                  <div
                    key={item.id}
                    className={`sched-all-row${item.enabled ? '' : ' is-paused'}`}
                  >
                    <div className="sched-all-main">
                      <div className="sched-all-title">
                        <strong>{item.name}</strong>
                        <span className="swarm-meta-chip">
                          {resolveSessionDisplayTitle(useAstrorderStore.getState().sessions, item.agent_id, item.session_id, {
                            title: item.session_title,
                          })}
                        </span>
                        {item.agent_name && (
                          <span className="swarm-meta-chip">{item.agent_name}</span>
                        )}
                      </div>
                      <div className="swarm-meta-chips">
                        <span className="swarm-meta-chip">{formatScheduleSummary(item)}</span>
                        {item.enabled && item.next_run_at && (
                          <span className="swarm-meta-chip is-accent">
                            下次 {formatNextRunChip(item.next_run_at)}
                          </span>
                        )}
                        {statusInfo && (
                          <span className={`sched-status-pill is-${statusInfo.tone}`}>
                            {statusInfo.label}
                          </span>
                        )}
                      </div>
                      <p className="sched-prompt-snippet">{item.prompt}</p>
                    </div>
                    <div className="sched-all-controls">
                      <Switch
                        size="xs"
                        color="violet"
                        checked={item.enabled}
                        disabled={busyId === item.id}
                        aria-label={`切换 ${item.name}`}
                        onChange={(e) => void handleToggle(item, e.currentTarget.checked)}
                      />
                      {onOpenSession && (
                        <button
                          type="button"
                          className="swarm-pill-btn"
                          onClick={() => {
                            setAllModalOpen(false)
                            onOpenSession(item.agent_id, item.session_id)
                          }}
                        >
                          <span>进入</span>
                          <IconArrowUpRight size={13} />
                        </button>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </Modal>
    </div>
  )
}
