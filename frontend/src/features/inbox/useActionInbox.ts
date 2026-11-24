import { useMemo } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { buildActionInboxItems, countUrgentInboxItems, type ActionInboxItem } from '../../domain/actionInbox'
import { useSessionUnread } from '../../hooks/useSessionUnread'
import { selectSessions, useAstrorderStore } from '../../state/store'

export function useActionInbox(activeSessionKey?: string): {
  items: ActionInboxItem[]
  urgentCount: number
  totalCount: number
  markRead: (sessionKey: string) => void
} {
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const approvals = useAstrorderStore((state) => state.approvals)
  const messages = useAstrorderStore((state) => state.messages)
  const commands = useAstrorderStore((state) => state.commands)
  const notifications = useAstrorderStore((state) => state.notifications)
  const { isUnread, markRead } = useSessionUnread(activeSessionKey)

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

  const urgentCount = useMemo(() => countUrgentInboxItems(items), [items])

  return {
    items,
    urgentCount,
    totalCount: items.length,
    markRead,
  }
}
