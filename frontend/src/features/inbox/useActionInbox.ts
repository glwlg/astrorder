import { useContext, useEffect, useMemo } from 'react'
import { QueryClient, QueryClientContext, useQuery } from '@tanstack/react-query'
import { useShallow } from 'zustand/react/shallow'

const fallbackQueryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
import {
  api,
  type MeetingActionItem,
  type OfficeTodoItem,
} from '../../api/client'
import type { SessionSchedule } from '../../domain/types'
import { buildActionInboxItems, countUrgentInboxItems, type ActionInboxItem } from '../../domain/actionInbox'
import { useSessionUnread } from '../../hooks/useSessionUnread'
import { WORKSPACE_MEETINGS_PLUGIN_ID } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { selectSessions, useAstrorderStore } from '../../state/store'

function localTodayISO(): string {
  const d = new Date()
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

export function isDueTodayOrOverdue(dueDate?: string | null, today = localTodayISO()): boolean {
  const cleaned = (dueDate || '').trim().slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(cleaned)) return false
  return cleaned <= today
}

export function isOverdue(dueDate?: string | null, today = localTodayISO()): boolean {
  const cleaned = (dueDate || '').trim().slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(cleaned)) return false
  return cleaned < today
}

export function useActionInbox(activeSessionKey?: string): {
  items: ActionInboxItem[]
  meetingActions: MeetingActionItem[]
  doneMeetingActions: MeetingActionItem[]
  todos: OfficeTodoItem[]
  openTodos: OfficeTodoItem[]
  doneTodos: OfficeTodoItem[]
  schedules: SessionSchedule[]
  urgentCount: number
  totalCount: number
  markRead: (sessionKey: string) => void
} {
  const ctxQueryClient = useContext(QueryClientContext)
  const hasQueryClient = Boolean(ctxQueryClient)
  const queryClient = ctxQueryClient ?? fallbackQueryClient
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const approvals = useAstrorderStore((state) => state.approvals)
  const messages = useAstrorderStore((state) => state.messages)
  const commands = useAstrorderStore((state) => state.commands)
  const notifications = useAstrorderStore((state) => state.notifications)
  const meetingsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_MEETINGS_PLUGIN_ID))
  const { isUnread, markRead } = useSessionUnread(activeSessionKey)

  useEffect(() => {
    if (!hasQueryClient) return
    const onMeetingsChanged = () => {
      void queryClient.invalidateQueries({ queryKey: ['astrorder', 'meeting-action-items'] })
    }
    window.addEventListener('astrorder:meetings-changed', onMeetingsChanged)
    return () => window.removeEventListener('astrorder:meetings-changed', onMeetingsChanged)
  }, [hasQueryClient, queryClient])

  const meetingActionsQuery = useQuery(
    {
      queryKey: ['astrorder', 'meeting-action-items'],
      queryFn: async () => {
        try {
          return await api.listMeetingActionItems('all')
        } catch {
          return { items: [], open_count: 0 }
        }
      },
      enabled: hasQueryClient && meetingsEnabled,
      staleTime: 60_000,
      refetchOnWindowFocus: false,
      retry: false,
    },
    queryClient,
  )

  const todosQuery = useQuery(
    {
      queryKey: ['astrorder', 'office-todos'],
      queryFn: async () => {
        if (typeof api.listTodos !== 'function') return { items: [], open_count: 0 }
        try {
          return await api.listTodos()
        } catch {
          return { items: [], open_count: 0 }
        }
      },
      enabled: hasQueryClient,
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      retry: false,
    },
    queryClient,
  )

  const schedulesQuery = useQuery(
    {
      queryKey: ['astrorder', 'schedules-all'],
      queryFn: async () => {
        if (typeof api.listAllSchedules !== 'function') return { items: [] }
        try {
          return await api.listAllSchedules()
        } catch {
          return { items: [] }
        }
      },
      enabled: hasQueryClient,
      staleTime: 60_000,
      refetchOnWindowFocus: false,
      retry: false,
    },
    queryClient,
  )

  const allMeetingActions = useMemo(
    () => (meetingsEnabled ? meetingActionsQuery.data?.items || [] : []),
    [meetingActionsQuery.data?.items, meetingsEnabled],
  )
  const meetingActions = useMemo(
    () => allMeetingActions.filter((a) => a.status !== 'done'),
    [allMeetingActions],
  )
  const doneMeetingActions = useMemo(
    () => allMeetingActions.filter((a) => a.status === 'done'),
    [allMeetingActions],
  )

  const todos = useMemo(() => todosQuery.data?.items || [], [todosQuery.data?.items])
  const openTodos = useMemo(() => todos.filter((t) => t.status !== 'done'), [todos])
  const doneTodos = useMemo(() => todos.filter((t) => t.status === 'done'), [todos])
  const schedules = useMemo(() => schedulesQuery.data?.items || [], [schedulesQuery.data?.items])

  const items = useMemo(
    () =>
      buildActionInboxItems({
        sessions,
        agents,
        approvals,
        messages,
        commands,
        notifications,
        isUnread,
      }),
    [sessions, agents, approvals, messages, commands, notifications, isUnread],
  )

  const urgentCount = useMemo(() => {
    const today = localTodayISO()
    const agentUrgent = countUrgentInboxItems(items)
    const urgentMeetings = meetingActions.filter(
      (a) => a.priority === 'high' || isDueTodayOrOverdue(a.due_date, today),
    ).length
    const urgentTodos = openTodos.filter(
      (t) => t.priority === 'high' || isDueTodayOrOverdue(t.due_date, today),
    ).length
    return agentUrgent + urgentMeetings + urgentTodos
  }, [items, meetingActions, openTodos])

  return {
    items,
    meetingActions,
    doneMeetingActions,
    todos,
    openTodos,
    doneTodos,
    schedules,
    urgentCount,
    totalCount: items.length + meetingActions.length + openTodos.length,
    markRead,
  }
}
