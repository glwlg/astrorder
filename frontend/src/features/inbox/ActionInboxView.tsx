import { useCallback, useMemo, useState } from 'react'
import { Badge, Button, Group, Modal, Stack, Switch, Text, TextInput, Textarea } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQueryClient } from '@tanstack/react-query'
import {
  IconAlertTriangle,
  IconArrowBackUp,
  IconArrowRight,
  IconBolt,
  IconCalendarEvent,
  IconCheck,
  IconChecks,
  IconClock,
  IconCopy,
  IconInbox,
  IconMessageForward,
  IconMessageQuestion,
  IconMicrophone,
  IconPencil,
  IconPlus,
  IconSearch,
  IconSend,
  IconShieldCheck,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import {
  ApiError,
  api,
  type MeetingActionItem,
  type OfficeTodoItem,
} from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { ArtifactReviewCard } from '../../components/ArtifactReviewCard'
import { AsyncUserInputCard } from '../../components/AsyncUserInputCard'
import { UserInputCard } from '../../components/UserInputCard'
import { formatRelativeTime, sessionActivityStatus } from '../../components/sessionRailModel'
import type { ActionInboxItem, ActionInboxKind } from '../../domain/actionInbox'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Command, SessionSchedule } from '../../domain/types'
import { selectSessions, useAstrorderStore } from '../../state/store'
import { useShallow } from 'zustand/react/shallow'
import { useIsOfficeProfile } from '../office/appProfile'
import { formatWeekdayMask } from '../../hooks/useSessionSchedules'
import { isDueTodayOrOverdue, isOverdue, useActionInbox } from './useActionInbox'
import {
  buildReminderSchedulePayload,
  createEmptyReminderDraft,
  formatReminderHumanSummary,
  REMINDER_WEEKDAY_ITEMS,
  validateReminderDraft,
  type ReminderDraft,
  type ReminderTriggerMode,
} from './scheduleReminderHelper'
import './actionInbox.css'

type InboxFilter = 'all' | 'today' | 'todo' | 'meeting' | 'schedule' | 'agent' | 'done'

function kindBadge(kind: ActionInboxKind): { label: string; color: string } {
  switch (kind) {
    case 'approval':
      return { label: '待审批', color: 'yellow' }
    case 'question':
      return { label: '待回答', color: 'violet' }
    case 'error':
      return { label: '异常', color: 'red' }
    case 'ready':
      return { label: '待验收', color: 'teal' }
  }
}

function priorityLabel(p?: string): string {
  if (p === 'high') return '高'
  if (p === 'low') return '低'
  return '中'
}

function formatScheduleCadence(s: SessionSchedule): string {
  if (s.schedule_kind === 'interval' && s.interval_minutes) return `每 ${s.interval_minutes} 分钟`
  if (s.schedule_kind === 'daily' && s.daily_time) return `每天 ${s.daily_time}`
  if (s.schedule_kind === 'weekly' && s.daily_time) {
    return `${formatWeekdayMask(s.weekday_mask)} ${s.daily_time}`
  }
  if (s.schedule_kind === 'once' && s.run_at) return `单次 ${s.run_at.slice(0, 16).replace('T', ' ')}`
  if (s.cron_expr) return s.cron_expr
  return '定时任务'
}

export interface ActionInboxViewProps {
  compact?: boolean
  onOpenSession: (agentId: string, sessionId: string) => void
}

export function ActionInboxView({ compact = false, onOpenSession }: ActionInboxViewProps) {
  const queryClient = useQueryClient()
  const isOfficeProfile = useIsOfficeProfile()
  const {
    items,
    meetingActions,
    doneMeetingActions,
    openTodos,
    doneTodos,
    schedules,
    markRead,
  } = useActionInbox()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const projects = useAstrorderStore((state) => state.projects)
  const commandsMap = useAstrorderStore((state) => state.commands)

  const [filter, setFilter] = useState<InboxFilter>('all')
  const [searchQuery, setSearchQuery] = useState('')
  const [priorityFilter, setPriorityFilter] = useState<'all' | 'high' | 'medium' | 'low'>('all')
  const [busyIds, setBusyIds] = useState<Record<string, boolean>>({})
  const [replyDrafts, setReplyDrafts] = useState<Record<string, string>>({})

  // Quick-add personal todo bar
  const [newTitle, setNewTitle] = useState('')
  const [newPriority, setNewPriority] = useState<'high' | 'medium' | 'low'>('medium')
  const [newDueDate, setNewDueDate] = useState('')
  const [newWorkspace, setNewWorkspace] = useState('')
  const [addingTodo, setAddingTodo] = useState(false)

  // Inline edit state for personal todo & meeting action item
  const [editingTodoId, setEditingTodoId] = useState<string | null>(null)
  const [editTodoTitle, setEditTodoTitle] = useState('')
  const [editTodoPriority, setEditTodoPriority] = useState<'high' | 'medium' | 'low'>('medium')
  const [editTodoDue, setEditTodoDue] = useState('')

  const [editingMtgId, setEditingMtgId] = useState<string | null>(null)
  const [editMtgText, setEditMtgText] = useState('')
  const [editMtgOwner, setEditMtgOwner] = useState('')
  const [editMtgDue, setEditMtgDue] = useState('')
  const [editMtgPriority, setEditMtgPriority] = useState('medium')

  // Dispatch-to-AI-chat session picker modal
  const [dispatchTask, setDispatchTask] = useState<{ title: string; prompt: string } | null>(null)
  const [dispatchSearch, setDispatchSearch] = useState('')

  // Create scheduled reminder modal
  const [reminderModalOpen, setReminderModalOpen] = useState(false)
  const [reminderDraft, setReminderDraft] = useState<ReminderDraft>(() => createEmptyReminderDraft())
  const [reminderSaving, setReminderSaving] = useState(false)
  const [reminderError, setReminderError] = useState<string | null>(null)

  const runningCount = useMemo(
    () => sessions.filter((s) => sessionActivityStatus(s) === 'running').length,
    [sessions],
  )

  const todayCount = useMemo(() => {
    const dueTodos = openTodos.filter((t) => isDueTodayOrOverdue(t.due_date) || t.priority === 'high').length
    const dueMeetings = meetingActions.filter((a) => isDueTodayOrOverdue(a.due_date) || a.priority === 'high').length
    return dueTodos + dueMeetings
  }, [meetingActions, openTodos])

  const tabs = useMemo<Array<{ id: InboxFilter; label: string; count: number }>>(() => {
    const list: Array<{ id: InboxFilter; label: string; count: number }> = [
      { id: 'all', label: '全部', count: openTodos.length + meetingActions.length + items.length },
      { id: 'today', label: '今天 / 逾期', count: todayCount },
      { id: 'todo', label: '个人待办', count: openTodos.length },
      { id: 'meeting', label: '会议行动项', count: meetingActions.length },
      { id: 'schedule', label: '定时提醒', count: schedules.filter((s) => s.enabled).length },
    ]
    if (!isOfficeProfile || items.length > 0) {
      list.push({ id: 'agent', label: isOfficeProfile ? 'AI 待确认' : 'Agent 待决策', count: items.length })
    }
    list.push({ id: 'done', label: '已完成', count: doneTodos.length + doneMeetingActions.length })
    return list
  }, [
    doneMeetingActions.length,
    doneTodos.length,
    isOfficeProfile,
    items.length,
    meetingActions.length,
    openTodos.length,
    schedules,
    todayCount,
  ])

  const normalizedQuery = searchQuery.trim().toLowerCase()

  const matchesSearchAndPriority = useCallback(
    (text: string, extra: string, priority?: string) => {
      if (priorityFilter !== 'all' && (priority || 'medium') !== priorityFilter) return false
      if (!normalizedQuery) return true
      return text.toLowerCase().includes(normalizedQuery) || extra.toLowerCase().includes(normalizedQuery)
    },
    [normalizedQuery, priorityFilter],
  )

  const projectNameByKey = useMemo(() => {
    const map = new Map<string, string>()
    for (const p of Object.values(projects)) {
      const label = p.project_name || p.workspace || p.project_id
      if (p.project_id) map.set(p.project_id, label)
      if (p.workspace) map.set(p.workspace, label)
    }
    return map
  }, [projects])

  const visibleOpenTodos = useMemo(() => {
    if (filter !== 'all' && filter !== 'todo' && filter !== 'today') return []
    return openTodos.filter((t) => {
      if (filter === 'today' && !isDueTodayOrOverdue(t.due_date) && t.priority !== 'high') return false
      const projLabel = t.project_key ? projectNameByKey.get(t.project_key) || t.project_key : ''
      return matchesSearchAndPriority(t.text, `${t.note || ''} ${projLabel}`, t.priority)
    })
  }, [filter, matchesSearchAndPriority, openTodos, projectNameByKey])

  const visibleMeetingActions = useMemo(() => {
    if (filter !== 'all' && filter !== 'meeting' && filter !== 'today') return []
    return meetingActions.filter((a) => {
      if (filter === 'today' && !isDueTodayOrOverdue(a.due_date) && a.priority !== 'high') return false
      return matchesSearchAndPriority(a.text, `${a.meeting_title || ''} ${a.owner || ''}`, a.priority)
    })
  }, [filter, matchesSearchAndPriority, meetingActions])

  const visibleAgentItems = useMemo(() => {
    if (filter !== 'all' && filter !== 'agent') return []
    if (!normalizedQuery) return items
    return items.filter(
      (it) =>
        it.title.toLowerCase().includes(normalizedQuery) ||
        it.summary.toLowerCase().includes(normalizedQuery) ||
        (it.session?.project_name || '').toLowerCase().includes(normalizedQuery),
    )
  }, [filter, items, normalizedQuery])

  const visibleSchedules = useMemo(() => {
    if (filter !== 'schedule') return []
    if (!normalizedQuery) return schedules
    return schedules.filter(
      (s) =>
        (s.name || '').toLowerCase().includes(normalizedQuery) ||
        (s.prompt || '').toLowerCase().includes(normalizedQuery),
    )
  }, [filter, normalizedQuery, schedules])

  const visibleDoneTodos = useMemo(() => {
    if (filter !== 'done') return []
    return doneTodos.filter((t) => {
      const projLabel = t.project_key ? projectNameByKey.get(t.project_key) || t.project_key : ''
      return matchesSearchAndPriority(t.text, `${t.note || ''} ${projLabel}`, t.priority)
    })
  }, [doneTodos, filter, matchesSearchAndPriority, projectNameByKey])

  const visibleDoneMeetings = useMemo(() => {
    if (filter !== 'done') return []
    return doneMeetingActions.filter((a) =>
      matchesSearchAndPriority(a.text, `${a.meeting_title || ''} ${a.owner || ''}`, a.priority),
    )
  }, [doneMeetingActions, filter, matchesSearchAndPriority])

  const workspaceOptions = useMemo(() => {
    const list: Array<{ key: string; workspace: string; name: string }> = []
    const seen = new Set<string>()
    for (const p of Object.values(projects)) {
      if (!p.workspace) continue
      const k = p.project_id || p.workspace
      if (seen.has(k)) continue
      seen.add(k)
      list.push({ key: k, workspace: p.workspace, name: p.project_name || p.workspace })
    }
    return list
  }, [projects])

  const reminderSessionOptions = useMemo(
    () => [...sessions].sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || '')),
    [sessions],
  )

  const recentSessions = useMemo(() => {
    const q = dispatchSearch.trim().toLowerCase()
    if (!q) return reminderSessionOptions.slice(0, 20)
    return reminderSessionOptions
      .filter(
        (s) =>
          (s.title || '').toLowerCase().includes(q) ||
          (s.project_name || '').toLowerCase().includes(q) ||
          (s.workspace || '').toLowerCase().includes(q),
      )
      .slice(0, 20)
  }, [dispatchSearch, reminderSessionOptions])

  const handleAddTodo = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = newTitle.trim()
    if (!trimmed || addingTodo) return
    setAddingTodo(true)
    try {
      const matchedProj = workspaceOptions.find((w) => w.key === newWorkspace || w.workspace === newWorkspace)
      await api.createTodo({
        text: trimmed,
        priority: newPriority,
        due_date: newDueDate || undefined,
        project_key: matchedProj?.key || undefined,
      })
      setNewTitle('')
      setNewDueDate('')
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
      notifications.show({ color: 'teal', message: '已添加个人待办' })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '添加待办失败',
      })
    } finally {
      setAddingTodo(false)
    }
  }

  const handleToggleTodo = async (todo: OfficeTodoItem) => {
    const key = `todo:${todo.id}`
    if (busyIds[key]) return
    setBusyIds((prev) => ({ ...prev, [key]: true }))
    const nextStatus = todo.status === 'done' ? 'open' : 'done'
    try {
      await api.patchTodo(todo.id, { status: nextStatus })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
      notifications.show({
        color: 'teal',
        message: nextStatus === 'done' ? '已完成待办' : '已恢复为未完成',
      })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '更新待办失败' })
    } finally {
      setBusyIds((prev) => {
        const next = { ...prev }
        delete next[key]
        return next
      })
    }
  }

  const handleSaveEditTodo = async (todo: OfficeTodoItem) => {
    const trimmed = editTodoTitle.trim()
    if (!trimmed) return
    try {
      await api.patchTodo(todo.id, {
        text: trimmed,
        priority: editTodoPriority,
        due_date: editTodoDue.trim(),
      })
      setEditingTodoId(null)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
      notifications.show({ color: 'teal', message: '已更新待办' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '更新待办失败' })
    }
  }

  const handleDeleteTodo = async (todo: OfficeTodoItem) => {
    const key = `todo-del:${todo.id}`
    if (busyIds[key]) return
    setBusyIds((prev) => ({ ...prev, [key]: true }))
    try {
      await api.deleteTodo(todo.id)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
      notifications.show({ color: 'teal', message: '已删除待办' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '删除待办失败' })
    } finally {
      setBusyIds((prev) => {
        const next = { ...prev }
        delete next[key]
        return next
      })
    }
  }

  const handleToggleMeetingAction = useCallback(
    async (action: MeetingActionItem, nextStatus: 'open' | 'done') => {
      const busyKey = `mtg:${action.id}`
      if (busyIds[busyKey]) return
      setBusyIds((prev) => ({ ...prev, [busyKey]: true }))
      try {
        await api.patchMeetingActionItem({
          meeting_id: action.meeting_id,
          item_id: action.id,
          status: nextStatus,
        })
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'meeting-action-items'] })
        notifications.show({
          color: 'teal',
          message: nextStatus === 'done' ? '已完成该会议行动项' : '已恢复该会议行动项',
        })
      } catch (cause) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '更新行动项失败',
        })
      } finally {
        setBusyIds((prev) => {
          const next = { ...prev }
          delete next[busyKey]
          return next
        })
      }
    },
    [busyIds, queryClient],
  )

  const handleSaveEditMeetingAction = async (act: MeetingActionItem) => {
    const trimmed = editMtgText.trim()
    if (!trimmed) return
    try {
      await api.patchMeetingActionItem({
        meeting_id: act.meeting_id,
        item_id: act.id,
        text: trimmed,
        owner: editMtgOwner.trim(),
        due_date: editMtgDue.trim(),
        priority:
          editMtgPriority === 'high' || editMtgPriority === 'medium' || editMtgPriority === 'low'
            ? editMtgPriority
            : undefined,
      })
      setEditingMtgId(null)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'meeting-action-items'] })
      notifications.show({ color: 'teal', message: '已更新会议行动项' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '更新失败' })
    }
  }

  const handleToggleSchedule = async (sched: SessionSchedule, enabled: boolean) => {
    try {
      await api.updateSessionSchedule(sched.id, { enabled })
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'schedules-all'] }),
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'session-schedules'] }),
      ])
      notifications.show({ color: 'teal', message: enabled ? '已开启定时提醒' : '已暂停定时提醒' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '更新定时任务失败' })
    }
  }

  const handleDeleteSchedule = async (sched: SessionSchedule) => {
    const key = `sched-del:${sched.id}`
    if (busyIds[key]) return
    setBusyIds((prev) => ({ ...prev, [key]: true }))
    try {
      await api.deleteSessionSchedule(sched.id)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'schedules-all'] }),
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'session-schedules'] }),
      ])
      notifications.show({ color: 'teal', message: '已删除定时提醒' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '删除定时提醒失败' })
    } finally {
      setBusyIds((prev) => {
        const next = { ...prev }
        delete next[key]
        return next
      })
    }
  }

  const openReminderModal = () => {
    const defaultSession = reminderSessionOptions[0]
    const defaultKey = defaultSession ? scopeKey(defaultSession.agent_id, defaultSession.id) : ''
    setReminderDraft(createEmptyReminderDraft(defaultKey))
    setReminderError(null)
    setReminderModalOpen(true)
  }

  const handleCreateReminder = async (e: React.FormEvent) => {
    e.preventDefault()
    if (reminderSaving) return
    const validationError = validateReminderDraft(reminderDraft)
    if (validationError) {
      setReminderError(validationError)
      return
    }
    const target = reminderSessionOptions.find(
      (s) => scopeKey(s.agent_id, s.id) === reminderDraft.sessionKey,
    )
    if (!target) {
      setReminderError('所选目标对话不存在，请重新选择')
      return
    }
    setReminderError(null)
    setReminderSaving(true)
    try {
      const payload = buildReminderSchedulePayload(reminderDraft, target.agent_id)
      await api.createSessionSchedule(target.id, payload)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'schedules-all'] }),
        queryClient.invalidateQueries({ queryKey: ['astrorder', 'session-schedules'] }),
      ])
      setReminderModalOpen(false)
      notifications.show({ color: 'teal', message: '已创建定时提醒' })
    } catch (cause) {
      const msg = cause instanceof Error ? cause.message : '创建定时提醒失败'
      setReminderError(msg)
      notifications.show({ color: 'red', message: msg })
    } finally {
      setReminderSaving(false)
    }
  }

  const handleDecision = useCallback(
    async (item: ActionInboxItem, action: 'approve' | 'cancel') => {
      const approval = item.approval
      if (!approval || busyIds[item.id]) return
      setBusyIds((prev) => ({ ...prev, [item.id]: true }))
      const id = newCommandId()
      const command: Command = {
        id,
        session_id: item.sessionId,
        agent_id: item.agentId,
        action,
        state: 'received',
        text: '',
        attachments: [],
        created_at: new Date().toISOString(),
        error: null,
        target_id: approval.target_id || approval.id,
      }
      const store = useAstrorderStore.getState()
      store.addOutbox(command, 'submitting')
      try {
        const result = await api.createCommand({
          id,
          agent_id: item.agentId,
          session_id: item.sessionId,
          action,
          text: '',
          attachment_ids: [],
          target_id: approval.target_id || approval.id,
        })
        store.mergeCommands([result])
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', item.agentId, item.sessionId] })
        notifications.show({
          color: action === 'approve' ? 'teal' : 'gray',
          message: action === 'approve' ? '已批准该请求' : '已拒绝该请求',
        })
      } catch (cause) {
        const msg = cause instanceof Error ? cause.message : '审批操作失败'
        store.markOutboxError(
          item.agentId,
          item.sessionId,
          id,
          msg,
          cause instanceof ApiError && cause.status < 500 ? 'failed' : 'unknown',
        )
        notifications.show({ color: 'red', message: msg })
      } finally {
        setBusyIds((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
      }
    },
    [busyIds, queryClient],
  )

  const handleQuickReply = useCallback(
    async (item: ActionInboxItem) => {
      const text = (replyDrafts[item.id] || '').trim()
      if (!text || busyIds[item.id]) return
      setBusyIds((prev) => ({ ...prev, [item.id]: true }))
      const id = newCommandId()
      try {
        const result = await api.createCommand({
          id,
          agent_id: item.agentId,
          session_id: item.sessionId,
          action: 'send',
          text,
          attachment_ids: [],
          target_id: null,
        })
        useAstrorderStore.getState().mergeCommands([result])
        markRead(item.sessionKey)
        setReplyDrafts((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
        await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', item.agentId, item.sessionId] })
        notifications.show({ color: 'teal', message: `已发送至「${item.title}」` })
      } catch (cause) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '发送失败',
        })
      } finally {
        setBusyIds((prev) => {
          const next = { ...prev }
          delete next[item.id]
          return next
        })
      }
    },
    [busyIds, markRead, queryClient, replyDrafts],
  )

  const markAllDismissableRead = () => {
    for (const item of items) {
      if (item.kind === 'ready' || item.kind === 'error') markRead(item.sessionKey)
    }
  }
  const dismissableCount = items.filter((i) => i.kind === 'ready' || i.kind === 'error').length

  const isEmptyView =
    visibleOpenTodos.length === 0 &&
    visibleMeetingActions.length === 0 &&
    visibleAgentItems.length === 0 &&
    visibleSchedules.length === 0 &&
    visibleDoneTodos.length === 0 &&
    visibleDoneMeetings.length === 0

  const dispatchToSession = (agentId: string, sessionId: string) => {
    if (!dispatchTask) return
    const prompt = dispatchTask.prompt
    setDispatchTask(null)
    onOpenSession(agentId, sessionId)
    window.setTimeout(() => {
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: { agentId, sessionId, text: prompt },
        }),
      )
    }, 180)
    notifications.show({ color: 'teal', message: '已发送到所选对话' })
  }

  return (
    <div className={`action-inbox${compact ? ' is-compact' : ''}`} aria-label={isOfficeProfile ? '统一待办中心' : '待我决策收件箱'}>
      {!compact && (
        <header className="action-inbox-header">
          <div className="action-inbox-heading">
            <div className="action-inbox-title-row">
              <IconInbox size={20} className="action-inbox-title-icon" aria-hidden="true" />
              <h1>{isOfficeProfile ? '待办中心' : '待办与决策中心'}</h1>
              {openTodos.length + meetingActions.length + items.length > 0 && (
                <span className="action-inbox-total-pill">
                  {openTodos.length + meetingActions.length + items.length}
                </span>
              )}
            </div>
            <Text size="xs" c="dimmed">
              {isOfficeProfile
                ? '统一管理个人办公待办、会议行动项与定时提醒，随时交由 AI 助手协助推进。'
                : '聚合个人待办、会议行动项、定时提醒与跨会话待审批、提问及待验收结果。'}
            </Text>
          </div>
          <Group gap="xs">
            {runningCount > 0 && (
              <span className="action-inbox-running-pill">
                <IconBolt size={13} aria-hidden="true" />
                <span>{runningCount} 个后台运行中</span>
              </span>
            )}
            {dismissableCount > 0 && (
              <Button size="xs" variant="subtle" color="gray" leftSection={<IconChecks size={14} />} onClick={markAllDismissableRead}>
                全部标为已读
              </Button>
            )}
          </Group>
        </header>
      )}

      {/* Quick-add personal todo bar */}
      {!compact && (
        <form className="action-inbox-add-bar" onSubmit={(e) => void handleAddTodo(e)}>
          <IconPlus size={15} className="action-inbox-add-icon" aria-hidden="true" />
          <input
            type="text"
            className="action-inbox-add-input"
            aria-label="新增个人待办"
            placeholder="输入待办事项，按 Enter 快速添加…"
            value={newTitle}
            onChange={(e) => setNewTitle(e.currentTarget.value)}
          />
          <select
            className="action-inbox-add-select"
            aria-label="待办优先级"
            value={newPriority}
            onChange={(e) => setNewPriority(e.currentTarget.value as 'high' | 'medium' | 'low')}
          >
            <option value="high">高优先级</option>
            <option value="medium">中优先级</option>
            <option value="low">低优先级</option>
          </select>
          <input
            type="date"
            className="action-inbox-add-date"
            aria-label="待办截止日期"
            value={newDueDate}
            onChange={(e) => setNewDueDate(e.currentTarget.value)}
          />
          {workspaceOptions.length > 0 && (
            <select
              className="action-inbox-add-select"
              aria-label="关联工作空间"
              value={newWorkspace}
              onChange={(e) => setNewWorkspace(e.currentTarget.value)}
            >
              <option value="">工作空间</option>
              {workspaceOptions.map((w) => (
                <option key={w.key} value={w.key}>
                  {w.name}
                </option>
              ))}
            </select>
          )}
          <button
            type="submit"
            className="action-inbox-add-submit"
            disabled={!newTitle.trim() || addingTodo}
          >
            添加
          </button>
        </form>
      )}

      <div className="action-inbox-toolbar">
        <div className="action-inbox-filters" role="tablist" aria-label="筛选待办类型">
          {tabs.map((tab) => {
            const active = filter === tab.id
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`action-inbox-filter-pill${active ? ' is-active' : ''}`}
                onClick={() => setFilter(tab.id)}
              >
                <span>{tab.label}</span>
                {tab.count > 0 && <span className="action-inbox-filter-count">{tab.count}</span>}
              </button>
            )
          })}
        </div>

        <div className="action-inbox-toolbar-right">
          {!compact && (
            <>
              <div className="action-inbox-search">
                <IconSearch size={13} />
                <input
                  type="text"
                  aria-label="搜索待办"
                  placeholder="搜索…"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.currentTarget.value)}
                />
                {searchQuery && (
                  <button type="button" className="action-inbox-icon-ghost" aria-label="清空搜索" onClick={() => setSearchQuery('')}>
                    <IconX size={12} />
                  </button>
                )}
              </div>
              <select
                className="action-inbox-add-select"
                aria-label="按优先级筛选"
                value={priorityFilter}
                onChange={(e) => setPriorityFilter(e.currentTarget.value as 'all' | 'high' | 'medium' | 'low')}
              >
                <option value="all">全部优先级</option>
                <option value="high">高</option>
                <option value="medium">中</option>
                <option value="low">低</option>
              </select>
            </>
          )}
          {compact && dismissableCount > 0 && (
            <button type="button" className="action-inbox-clear-btn" onClick={markAllDismissableRead}>
              <IconChecks size={14} aria-hidden="true" />
              <span>已读全部</span>
            </button>
          )}
        </div>
      </div>

      {filter === 'schedule' && (
        <div className="action-inbox-schedule-bar">
          <div className="action-inbox-schedule-bar-copy">
            <strong>定时提醒</strong>
            <span>选择时间与重复方式，到点自动在关联对话中提醒你，无需手写 Cron。</span>
          </div>
          <Button
            size="xs"
            color="dark"
            leftSection={<IconPlus size={14} />}
            onClick={openReminderModal}
          >
            新建提醒
          </Button>
        </div>
      )}

      {isEmptyView ? (
        <div className="action-inbox-empty">
          <div className="action-inbox-empty-icon" aria-hidden="true">
            <IconCheck size={22} />
          </div>
          <strong>{filter === 'all' ? '全部处理完毕' : '当前分类暂无条目'}</strong>
          <p>
            {filter === 'done'
              ? '还没有已完成的待办记录。'
              : filter === 'schedule'
                ? '暂无定时提醒，点击下方按钮即可快速创建。'
                : runningCount > 0
                  ? `目前没有待你处理的事项，另有 ${runningCount} 个任务正在后台执行。`
                  : '没有待办事项、未完成会议行动项或待确认请求。'}
          </p>
          {filter === 'schedule' && (
            <Button
              size="xs"
              color="dark"
              mt="xs"
              leftSection={<IconPlus size={14} />}
              onClick={openReminderModal}
            >
              新建第一条提醒
            </Button>
          )}
        </div>
      ) : (
        <div className="action-inbox-list">
          {/* Open Personal Todos */}
          {visibleOpenTodos.map((todo) => {
            const busy = Boolean(busyIds[`todo:${todo.id}`])
            const overdue = isOverdue(todo.due_date)
            const isEditing = editingTodoId === todo.id
            return (
              <article key={`todo-${todo.id}`} className="action-inbox-card is-todo">
                {isEditing ? (
                  <div className="action-inbox-inline-edit">
                    <input
                      type="text"
                      className="action-inbox-add-input"
                      aria-label="编辑待办标题"
                      value={editTodoTitle}
                      onChange={(e) => setEditTodoTitle(e.currentTarget.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          void handleSaveEditTodo(todo)
                        } else if (e.key === 'Escape') {
                          setEditingTodoId(null)
                        }
                      }}
                      autoFocus
                    />
                    <select
                      className="action-inbox-add-select"
                      value={editTodoPriority}
                      onChange={(e) => setEditTodoPriority(e.currentTarget.value as 'high' | 'medium' | 'low')}
                    >
                      <option value="high">高</option>
                      <option value="medium">中</option>
                      <option value="low">低</option>
                    </select>
                    <input
                      type="date"
                      className="action-inbox-add-date"
                      value={editTodoDue}
                      onChange={(e) => setEditTodoDue(e.currentTarget.value)}
                    />
                    <Button size="compact-xs" color="dark" onClick={() => void handleSaveEditTodo(todo)}>
                      保存
                    </Button>
                    <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setEditingTodoId(null)}>
                      取消
                    </Button>
                  </div>
                ) : (
                  <div className="action-inbox-todo-row">
                    <button
                      type="button"
                      className="action-inbox-check-btn"
                      aria-label={`完成待办 ${todo.text}`}
                      disabled={busy}
                      onClick={() => void handleToggleTodo(todo)}
                    />
                    <div className="action-inbox-todo-main">
                      <div className="action-inbox-todo-title">{todo.text}</div>
                      <div className="action-inbox-todo-meta">
                        <span className={`action-inbox-prio-tag is-${todo.priority || 'medium'}`}>
                          {priorityLabel(todo.priority)}
                        </span>
                        {todo.due_date && (
                          <span className={`action-inbox-due-tag${overdue ? ' is-overdue' : ''}`}>
                            <IconCalendarEvent size={11} />
                            <span>{overdue ? `已逾期 ${todo.due_date}` : todo.due_date}</span>
                          </span>
                        )}
                        {todo.project_key && (
                          <span className="action-inbox-project-tag">
                            {projectNameByKey.get(todo.project_key) || todo.project_key}
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="action-inbox-todo-ops">
                      <button
                        type="button"
                        className="action-inbox-icon-ghost"
                        title="编辑"
                        aria-label={`编辑待办 ${todo.text}`}
                        onClick={() => {
                          setEditingTodoId(todo.id)
                          setEditTodoTitle(todo.text)
                          setEditTodoPriority(
                            todo.priority === 'high' || todo.priority === 'low' ? todo.priority : 'medium',
                          )
                          setEditTodoDue(todo.due_date || '')
                        }}
                      >
                        <IconPencil size={14} />
                      </button>
                      <button
                        type="button"
                        className="action-inbox-icon-ghost"
                        title="交由 AI 助手协助处理"
                        aria-label={`交由 AI 处理 ${todo.text}`}
                        onClick={() => {
                          setDispatchSearch('')
                          setDispatchTask({
                            title: todo.text,
                            prompt: `请协助我推进待办事项：${todo.text}${todo.due_date ? `（截止日期：${todo.due_date}）` : ''}`,
                          })
                        }}
                      >
                        <IconMessageForward size={14} />
                      </button>
                      <button
                        type="button"
                        className="action-inbox-icon-ghost is-danger"
                        title="删除"
                        aria-label={`删除待办 ${todo.text}`}
                        onClick={() => void handleDeleteTodo(todo)}
                      >
                        <IconTrash size={14} />
                      </button>
                    </div>
                  </div>
                )}
              </article>
            )
          })}

          {/* Open Meeting Action Items */}
          {visibleMeetingActions.map((act) => {
            const busy = Boolean(busyIds[`mtg:${act.id}`])
            const overdue = isOverdue(act.due_date)
            const isEditing = editingMtgId === act.id
            return (
              <article key={`mtg-${act.id}`} className="action-inbox-card is-meeting">
                <div className="action-inbox-card-head">
                  <div className="action-inbox-card-meta">
                    <span className="action-inbox-brand" aria-hidden="true">
                      <IconMicrophone size={14} />
                    </span>
                    <strong className="action-inbox-card-title" title={act.meeting_title}>
                      {act.meeting_title || '会议行动项'}
                    </strong>
                    <Badge size="xs" variant="light" color="gray">会议行动项</Badge>
                    {act.priority === 'high' && <span className="action-inbox-prio-tag is-high">高</span>}
                    {act.owner && <span className="action-inbox-project-tag">{act.owner}</span>}
                    {act.due_date && (
                      <span className={`action-inbox-due-tag${overdue ? ' is-overdue' : ''}`}>
                        {overdue ? `已逾期 ${act.due_date}` : act.due_date}
                      </span>
                    )}
                  </div>
                  <div className="action-inbox-card-right">
                    <button
                      type="button"
                      className="action-inbox-open-btn"
                      onClick={() =>
                        window.dispatchEvent(
                          new CustomEvent('astrorder:navigate', {
                            detail: {
                              to: act.meeting_id
                                ? `/meetings?id=${encodeURIComponent(act.meeting_id)}`
                                : '/meetings',
                            },
                          }),
                        )
                      }
                    >
                      <span>查看纪要</span>
                      <IconArrowRight size={13} aria-hidden="true" />
                    </button>
                  </div>
                </div>

                {isEditing ? (
                  <div className="action-inbox-inline-edit">
                    <input
                      type="text"
                      className="action-inbox-add-input"
                      aria-label="编辑会议行动项内容"
                      value={editMtgText}
                      onChange={(e) => setEditMtgText(e.currentTarget.value)}
                      autoFocus
                    />
                    <input
                      type="text"
                      className="action-inbox-add-select"
                      style={{ width: 84 }}
                      placeholder="负责人"
                      value={editMtgOwner}
                      onChange={(e) => setEditMtgOwner(e.currentTarget.value)}
                    />
                    <input
                      type="date"
                      className="action-inbox-add-date"
                      value={editMtgDue}
                      onChange={(e) => setEditMtgDue(e.currentTarget.value)}
                    />
                    <select
                      className="action-inbox-add-select"
                      value={editMtgPriority}
                      onChange={(e) => setEditMtgPriority(e.currentTarget.value)}
                    >
                      <option value="high">高</option>
                      <option value="medium">中</option>
                      <option value="low">低</option>
                    </select>
                    <Button size="compact-xs" color="dark" onClick={() => void handleSaveEditMeetingAction(act)}>
                      保存
                    </Button>
                    <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setEditingMtgId(null)}>
                      取消
                    </Button>
                  </div>
                ) : (
                  <>
                    <p className="action-inbox-summary">{act.text}</p>
                    <div className="action-inbox-actions">
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="gray"
                        leftSection={<IconPencil size={12} />}
                        onClick={() => {
                          setEditingMtgId(act.id)
                          setEditMtgText(act.text)
                          setEditMtgOwner(act.owner || '')
                          setEditMtgDue(act.due_date || '')
                          setEditMtgPriority(act.priority || 'medium')
                        }}
                      >
                        编辑
                      </Button>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="gray"
                        leftSection={<IconMessageForward size={12} />}
                        onClick={() => {
                          setDispatchSearch('')
                          setDispatchTask({
                            title: act.text,
                            prompt: `请协助跟进会议《${act.meeting_title || '会议纪要'}》的行动项：${act.text}${act.owner ? `（负责人：${act.owner}）` : ''}${act.due_date ? `（截止：${act.due_date}）` : ''}`,
                          })
                        }}
                      >
                        交由 AI 协助
                      </Button>
                      <Button
                        size="xs"
                        color="dark"
                        variant="light"
                        loading={busy}
                        leftSection={<IconCheck size={14} />}
                        onClick={() => void handleToggleMeetingAction(act, 'done')}
                      >
                        标为完成
                      </Button>
                    </div>
                  </>
                )}
              </article>
            )
          })}

          {/* Scheduled Reminders */}
          {visibleSchedules.map((sched) => (
            <article key={`sched-${sched.id}`} className="action-inbox-card is-schedule">
              <div className="action-inbox-card-head">
                <div className="action-inbox-card-meta">
                  <span className="action-inbox-brand" aria-hidden="true">
                    <IconClock size={14} />
                  </span>
                  <strong className="action-inbox-card-title">{sched.name || '定时提醒'}</strong>
                  <Badge size="xs" variant="light" color={sched.enabled ? 'teal' : 'gray'}>
                    {formatScheduleCadence(sched)}
                  </Badge>
                  {sched.next_run_at && (
                    <span className="action-inbox-time">
                      下次：{sched.next_run_at.slice(0, 16).replace('T', ' ')}
                    </span>
                  )}
                </div>
                <div className="action-inbox-card-right">
                  <Switch
                    size="xs"
                    checked={sched.enabled}
                    aria-label={`开关定时提醒 ${sched.name}`}
                    onChange={(e) => void handleToggleSchedule(sched, e.currentTarget.checked)}
                  />
                  <button
                    type="button"
                    className="action-inbox-open-btn"
                    onClick={() => onOpenSession(sched.agent_id, sched.session_id)}
                  >
                    <span>进入对话</span>
                    <IconArrowRight size={13} aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    className="action-inbox-icon-ghost is-danger"
                    title="删除定时提醒"
                    aria-label={`删除定时提醒 ${sched.name || '定时提醒'}`}
                    disabled={Boolean(busyIds[`sched-del:${sched.id}`])}
                    onClick={() => void handleDeleteSchedule(sched)}
                  >
                    <IconTrash size={14} />
                  </button>
                </div>
              </div>
              {sched.prompt && <p className="action-inbox-summary">{sched.prompt}</p>}
            </article>
          ))}

          {/* Completed Items (Personal Todos + Meeting Actions) */}
          {visibleDoneTodos.map((todo) => (
            <article key={`done-todo-${todo.id}`} className="action-inbox-card is-done-item">
              <div className="action-inbox-todo-row">
                <button
                  type="button"
                  className="action-inbox-check-btn is-checked"
                  aria-label={`恢复待办 ${todo.text}`}
                  onClick={() => void handleToggleTodo(todo)}
                >
                  <IconCheck size={11} stroke={2.8} />
                </button>
                <div className="action-inbox-todo-main">
                  <div className="action-inbox-todo-title is-done">{todo.text}</div>
                  <div className="action-inbox-todo-meta">
                    <span className="action-inbox-project-tag">个人待办</span>
                    {todo.project_key && (
                      <span className="action-inbox-project-tag">
                        {projectNameByKey.get(todo.project_key) || todo.project_key}
                      </span>
                    )}
                  </div>
                </div>
                <div className="action-inbox-todo-ops">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="gray"
                    leftSection={<IconArrowBackUp size={12} />}
                    onClick={() => void handleToggleTodo(todo)}
                  >
                    恢复未完成
                  </Button>
                  <button
                    type="button"
                    className="action-inbox-icon-ghost is-danger"
                    aria-label={`删除已完成待办 ${todo.text}`}
                    onClick={() => void handleDeleteTodo(todo)}
                  >
                    <IconTrash size={14} />
                  </button>
                </div>
              </div>
            </article>
          ))}

          {visibleDoneMeetings.map((act) => (
            <article key={`done-mtg-${act.id}`} className="action-inbox-card is-done-item">
              <div className="action-inbox-todo-row">
                <button
                  type="button"
                  className="action-inbox-check-btn is-checked"
                  aria-label={`恢复会议行动项 ${act.text}`}
                  onClick={() => void handleToggleMeetingAction(act, 'open')}
                >
                  <IconCheck size={11} stroke={2.8} />
                </button>
                <div className="action-inbox-todo-main">
                  <div className="action-inbox-todo-title is-done">{act.text}</div>
                  <div className="action-inbox-todo-meta">
                    <span className="action-inbox-project-tag">会议 · {act.meeting_title || '未命名'}</span>
                    {act.owner && <span className="action-inbox-project-tag">{act.owner}</span>}
                  </div>
                </div>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  leftSection={<IconArrowBackUp size={12} />}
                  onClick={() => void handleToggleMeetingAction(act, 'open')}
                >
                  恢复未完成
                </Button>
              </div>
            </article>
          ))}

          {/* Agent Decision Items (Approvals / Questions / Errors / Ready) */}
          {visibleAgentItems.map((item) => {
            const badge = kindBadge(item.kind)
            const busy = Boolean(busyIds[item.id])
            const draft = replyDrafts[item.id] || ''
            const sessionCommands = Object.values(commandsMap).filter(
              (c) => scopeKey(c.agent_id, c.session_id) === item.sessionKey,
            )
            return (
              <article key={item.id} className={`action-inbox-card is-${item.kind}`}>
                <div className="action-inbox-card-head">
                  <div className="action-inbox-card-meta">
                    <span className="action-inbox-brand" aria-hidden="true">
                      {item.agent && !isOfficeProfile ? (
                        <AgentBrandIcon kind={item.agent.kind} size={15} />
                      ) : item.kind === 'question' ? (
                        <IconMessageQuestion size={15} />
                      ) : item.kind === 'error' ? (
                        <IconAlertTriangle size={15} />
                      ) : (
                        <IconShieldCheck size={15} />
                      )}
                    </span>
                    <strong className="action-inbox-card-title" title={item.title}>{item.title}</strong>
                    {item.session?.project_name && (
                      <span className="action-inbox-project-tag">{item.session.project_name}</span>
                    )}
                    <Badge size="xs" variant="light" color={badge.color}>
                      {item.artifactReview ? '方案待确认' : badge.label}
                    </Badge>
                  </div>
                  <div className="action-inbox-card-right">
                    {item.timestamp && (
                      <span className="action-inbox-time">{formatRelativeTime(item.timestamp)}</span>
                    )}
                    <button
                      type="button"
                      className="action-inbox-open-btn"
                      onClick={() => {
                        if (item.kind === 'ready' || item.kind === 'error') markRead(item.sessionKey)
                        onOpenSession(item.agentId, item.sessionId)
                      }}
                    >
                      <span>进入</span>
                      <IconArrowRight size={13} aria-hidden="true" />
                    </button>
                  </div>
                </div>

                {item.kind === 'question' && item.approval ? (
                  <div className="action-inbox-embedded-card">
                    <UserInputCard approval={item.approval} />
                  </div>
                ) : item.kind === 'question' && item.questionMessage ? (
                  <div className="action-inbox-embedded-card">
                    <AsyncUserInputCard message={item.questionMessage} commands={sessionCommands} />
                  </div>
                ) : item.kind === 'question' && item.artifactReview ? (
                  <div className="action-inbox-embedded-card">
                    <ArtifactReviewCard
                      artifact={item.artifactReview}
                      session={item.session}
                      commands={sessionCommands}
                      turnEndAt={item.artifactTurnEndAt}
                      readOnly={item.session?.control_state === 'readonly'}
                      compact
                    />
                  </div>
                ) : (
                  <>
                    <p className="action-inbox-summary">{item.summary}</p>
                    {item.detail && <pre className="action-inbox-code">{item.detail}</pre>}
                  </>
                )}

                {item.kind === 'approval' && item.canApproveInline && (
                  <div className="action-inbox-actions">
                    <Button
                      size="xs"
                      variant="subtle"
                      color="gray"
                      disabled={busy}
                      onClick={() => void handleDecision(item, 'cancel')}
                    >
                      拒绝
                    </Button>
                    <Button
                      size="xs"
                      color="dark"
                      loading={busy}
                      leftSection={<IconCheck size={14} />}
                      onClick={() => void handleDecision(item, 'approve')}
                    >
                      批准执行
                    </Button>
                  </div>
                )}

                {(item.kind === 'ready' || item.kind === 'error') && (
                  <div className="action-inbox-reply-row">
                    {item.canQuickReply && (
                      <form
                        className="action-inbox-quick-form"
                        onSubmit={(e) => {
                          e.preventDefault()
                          void handleQuickReply(item)
                        }}
                      >
                        <input
                          type="text"
                          aria-label={`快捷回复 ${item.title}`}
                          placeholder={item.kind === 'error' ? '输入重试或处理指令…' : '输入下一步指令…'}
                          value={draft}
                          disabled={busy}
                          onChange={(e) => setReplyDrafts((prev) => ({ ...prev, [item.id]: e.target.value }))}
                        />
                        <button
                          type="submit"
                          className="action-inbox-send-btn"
                          aria-label="发送快捷指令"
                          disabled={busy || !draft.trim()}
                        >
                          <IconSend size={15} aria-hidden="true" />
                        </button>
                      </form>
                    )}
                    <button
                      type="button"
                      className="action-inbox-read-btn"
                      onClick={() => markRead(item.sessionKey)}
                    >
                      标为已读
                    </button>
                  </div>
                )}
              </article>
            )
          })}
        </div>
      )}

      {/* Session Picker Modal for Dispatching Tasks to AI Assistant */}
      <Modal
        opened={dispatchTask !== null}
        onClose={() => setDispatchTask(null)}
        title="交由 AI 助手协助处理"
        size="sm"
        withinPortal
      >
        <Stack gap="sm">
          {dispatchTask && (
            <Text size="xs" c="dimmed">
              待办：{dispatchTask.title}
            </Text>
          )}
          <TextInput
            size="xs"
            leftSection={<IconSearch size={13} />}
            placeholder="搜索对话标题或工作空间…"
            value={dispatchSearch}
            onChange={(e) => setDispatchSearch(e.currentTarget.value)}
          />
          <div className="action-inbox-session-list">
            {recentSessions.length === 0 ? (
              <Text size="xs" c="dimmed" ta="center" py="md">
                暂无可选对话，可先复制内容后新建对话粘贴。
              </Text>
            ) : (
              recentSessions.map((s) => (
                <button
                  key={`${s.agent_id}:${s.id}`}
                  type="button"
                  className="action-inbox-session-row"
                  onClick={() => dispatchToSession(s.agent_id, s.id)}
                >
                  <span className="action-inbox-session-title">{s.title || '未命名对话'}</span>
                  {s.project_name && <span className="action-inbox-session-sub">{s.project_name}</span>}
                </button>
              ))
            )}
          </div>
          <Group justify="space-between">
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              leftSection={<IconCopy size={13} />}
              onClick={() => {
                if (!dispatchTask) return
                void navigator.clipboard.writeText(dispatchTask.prompt)
                setDispatchTask(null)
                notifications.show({ color: 'teal', message: '已复制到剪贴板' })
              }}
            >
              仅复制到剪贴板
            </Button>
            <Button size="xs" variant="subtle" color="gray" onClick={() => setDispatchTask(null)}>
              取消
            </Button>
          </Group>
        </Stack>
      </Modal>

      {/* Friendly Create Scheduled Reminder Modal */}
      <Modal
        opened={reminderModalOpen}
        onClose={() => {
          if (!reminderSaving) setReminderModalOpen(false)
        }}
        title="新建定时提醒"
        size="md"
        withinPortal
      >
        <form onSubmit={(e) => void handleCreateReminder(e)}>
          <Stack gap="sm">
            <TextInput
              size="xs"
              label="提醒名称（可选）"
              placeholder="例如：每日晨报提醒（留空自动取提醒内容首行）"
              value={reminderDraft.name}
              onChange={(e) => {
                const value = e.currentTarget.value
                setReminderDraft((prev) => ({ ...prev, name: value }))
              }}
              disabled={reminderSaving}
            />

            <Textarea
              size="xs"
              label="提醒内容"
              placeholder="例如：提醒我整理今日待办清单并同步项目进度"
              minRows={3}
              required
              value={reminderDraft.prompt}
              onChange={(e) => {
                const value = e.currentTarget.value
                setReminderDraft((prev) => ({ ...prev, prompt: value }))
                setReminderError(null)
              }}
              disabled={reminderSaving}
            />

            <div className="action-inbox-reminder-field">
              <span className="action-inbox-reminder-label">触发方式</span>
              <div className="action-inbox-reminder-pills" role="radiogroup" aria-label="触发方式">
                {(
                  [
                    ['once', '一次性'],
                    ['daily', '每天'],
                    ['weekdays', '每工作日'],
                    ['weekly', '每周几'],
                  ] as const satisfies ReadonlyArray<readonly [ReminderTriggerMode, string]>
                ).map(([modeValue, label]) => (
                  <button
                    key={modeValue}
                    type="button"
                    role="radio"
                    aria-checked={reminderDraft.mode === modeValue}
                    className={`action-inbox-reminder-pill${reminderDraft.mode === modeValue ? ' is-active' : ''}`}
                    disabled={reminderSaving}
                    onClick={() => {
                      setReminderDraft((prev) => ({ ...prev, mode: modeValue }))
                      setReminderError(null)
                    }}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {reminderDraft.mode === 'once' ? (
              <label className="action-inbox-reminder-field">
                <span className="action-inbox-reminder-label">提醒日期与时间</span>
                <input
                  type="datetime-local"
                  aria-label="提醒日期与时间"
                  className="action-inbox-reminder-native-input"
                  value={reminderDraft.onceLocal}
                  disabled={reminderSaving}
                  onChange={(e) => {
                    const value = e.currentTarget.value
                    setReminderDraft((prev) => ({ ...prev, onceLocal: value }))
                    setReminderError(null)
                  }}
                />
              </label>
            ) : (
              <>
                {reminderDraft.mode === 'weekly' && (
                  <div className="action-inbox-reminder-field">
                    <span className="action-inbox-reminder-label">重复星期</span>
                    <div className="action-inbox-weekday-row" role="group" aria-label="选择星期">
                      {REMINDER_WEEKDAY_ITEMS.map((day) => {
                        const active = Boolean(reminderDraft.weekdayMask & day.bit)
                        return (
                          <button
                            key={day.bit}
                            type="button"
                            aria-pressed={active}
                            aria-label={`周${day.label}`}
                            className={`action-inbox-weekday-btn${active ? ' is-active' : ''}`}
                            disabled={reminderSaving}
                            onClick={() => {
                              setReminderDraft((prev) => {
                                const nextMask = active
                                  ? prev.weekdayMask & ~day.bit
                                  : prev.weekdayMask | day.bit
                                return { ...prev, weekdayMask: nextMask || day.bit }
                              })
                              setReminderError(null)
                            }}
                          >
                            {day.label}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                )}

                <label className="action-inbox-reminder-field">
                  <span className="action-inbox-reminder-label">提醒时间</span>
                  <input
                    type="time"
                    aria-label="提醒时间"
                    className="action-inbox-reminder-native-input"
                    value={reminderDraft.time}
                    disabled={reminderSaving}
                    onChange={(e) => {
                      const value = e.currentTarget.value
                      setReminderDraft((prev) => ({ ...prev, time: value }))
                      setReminderError(null)
                    }}
                  />
                </label>
              </>
            )}

            <label className="action-inbox-reminder-field">
              <span className="action-inbox-reminder-label">关联目标对话</span>
              {reminderSessionOptions.length === 0 ? (
                <Text size="xs" c="dimmed">
                  当前暂无可用对话，请先创建一个对话后再设置定时提醒。
                </Text>
              ) : (
                <select
                  aria-label="关联目标对话"
                  className="action-inbox-reminder-native-input"
                  value={reminderDraft.sessionKey}
                  disabled={reminderSaving}
                  onChange={(e) => {
                    const value = e.currentTarget.value
                    setReminderDraft((prev) => ({ ...prev, sessionKey: value }))
                    setReminderError(null)
                  }}
                >
                  {reminderSessionOptions.map((s) => {
                    const sKey = scopeKey(s.agent_id, s.id)
                    return (
                      <option key={sKey} value={sKey}>
                        {s.title || '未命名对话'}
                        {s.project_name ? ` · ${s.project_name}` : ''}
                      </option>
                    )
                  })}
                </select>
              )}
            </label>

            <div className="action-inbox-reminder-summary" role="status">
              {formatReminderHumanSummary(reminderDraft)}
            </div>

            {reminderError && (
              <div className="action-inbox-reminder-error" role="alert">
                {reminderError}
              </div>
            )}

            <Group justify="flex-end" gap="xs" mt="xs">
              <Button
                size="xs"
                variant="subtle"
                color="gray"
                disabled={reminderSaving}
                onClick={() => setReminderModalOpen(false)}
              >
                取消
              </Button>
              <Button
                type="submit"
                size="xs"
                color="dark"
                loading={reminderSaving}
                disabled={reminderSessionOptions.length === 0}
              >
                创建提醒
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </div>
  )
}
