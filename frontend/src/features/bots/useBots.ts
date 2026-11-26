import { QueryClientContext, useQuery, useQueryClient } from '@tanstack/react-query'
import { useContext, useEffect, useState } from 'react'
import { api, type BotSummary } from '../../api/client'
import { connectionName } from '../../components/AgentSessionFilter'
import type { Agent } from '../../domain/types'

export const BOTS_QUERY_KEY = ['astrorder', 'bots'] as const

export function useBots(enabled = true) {
  return useQuery({
    queryKey: BOTS_QUERY_KEY,
    queryFn: () => api.listBots(),
    enabled,
    staleTime: 20_000,
    retry: false,
  })
}

/**
 * Safe variant for components that may render outside a QueryClientProvider in unit tests
 * (e.g. SessionRail). Subscribes to React Query cache when available and gracefully returns [] otherwise.
 */
export function useBotsOptional(): BotSummary[] {
  const queryClient = useContext(QueryClientContext)
  const [items, setItems] = useState<BotSummary[]>(
    () => queryClient?.getQueryData<{ items: BotSummary[] }>(BOTS_QUERY_KEY)?.items || [],
  )

  useEffect(() => {
    if (!queryClient) return
    let active = true
    const sync = () => {
      if (!active) return
      const cached = queryClient.getQueryData<{ items: BotSummary[] }>(BOTS_QUERY_KEY)?.items
      if (cached) setItems(cached)
    }
    sync()
    const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
      if (event.query.queryKey[0] === BOTS_QUERY_KEY[0] && event.query.queryKey[1] === BOTS_QUERY_KEY[1]) {
        sync()
      }
    })
    void queryClient
      .ensureQueryData({
        queryKey: BOTS_QUERY_KEY,
        queryFn: () => api.listBots(),
        staleTime: 20_000,
      })
      .then((res) => {
        if (active && res?.items) setItems(res.items)
      })
      .catch(() => {})
    return () => {
      active = false
      unsubscribe()
    }
  }, [queryClient])

  return items
}

export function useInvalidateBots() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: BOTS_QUERY_KEY })
}

export function normalizeBotWorkspace(ws?: string | null): string {
  const normalized = (ws || '').trim().replaceAll('\\', '/').replace(/\/+$/, '')
  if (normalized.length >= 2 && normalized[1] === ':') {
    return normalized.toLowerCase()
  }
  return normalized
}

export function normalizeBotConnection(connId?: string | null): string {
  const trimmed = (connId || '').trim()
  if (!trimmed || trimmed.toLowerCase() === 'local') return ''
  return trimmed
}

export function findBotForWorkspace(
  bots: BotSummary[],
  workspace?: string | null,
  connectionId?: string | null,
): BotSummary | undefined {
  const targetWs = normalizeBotWorkspace(workspace)
  if (!targetWs) return undefined
  const targetConn = normalizeBotConnection(connectionId)
  return bots.find(
    (bot) =>
      normalizeBotWorkspace(bot.workspace) === targetWs &&
      normalizeBotConnection(bot.connection_id) === targetConn,
  )
}

export interface DistillBotIntent {
  sessionKey: string
  sessionTitle?: string
  workspace?: string
  connectionId?: string
  agentId?: string
}

const PENDING_DISTILL_KEY = 'astrorder:pending-distill-bot'

export function storePendingDistillIntent(intent: DistillBotIntent) {
  try {
    sessionStorage.setItem(PENDING_DISTILL_KEY, JSON.stringify(intent))
  } catch {}
}

export function consumePendingDistillIntent(): DistillBotIntent | null {
  try {
    const raw = sessionStorage.getItem(PENDING_DISTILL_KEY)
    if (!raw) return null
    sessionStorage.removeItem(PENDING_DISTILL_KEY)
    const parsed = JSON.parse(raw) as DistillBotIntent
    if (parsed && typeof parsed.sessionKey === 'string' && parsed.sessionKey.includes('::')) {
      return parsed
    }
  } catch {}
  return null
}

export interface HandoffBotIntent {
  callerSessionKey: string
  callerTitle?: string
  callerWorkspace?: string
  callerConnectionId?: string
  sourceBotId?: string
  targetBotId?: string
  prompt?: string
}

const PENDING_HANDOFF_KEY = 'astrorder:pending-handoff-bot'

export function storePendingHandoffIntent(intent: HandoffBotIntent) {
  try {
    sessionStorage.setItem(PENDING_HANDOFF_KEY, JSON.stringify(intent))
  } catch {}
}

export function consumePendingHandoffIntent(): HandoffBotIntent | null {
  try {
    const raw = sessionStorage.getItem(PENDING_HANDOFF_KEY)
    if (!raw) return null
    sessionStorage.removeItem(PENDING_HANDOFF_KEY)
    const parsed = JSON.parse(raw) as HandoffBotIntent
    if (parsed && typeof parsed.callerSessionKey === 'string' && parsed.callerSessionKey.includes('::')) {
      return parsed
    }
  } catch {}
  return null
}

export interface BotEnvironmentOption {
  value: string
  label: string
}

export function buildBotEnvironmentOptions(
  agents: Agent[],
  currentConnectionId?: string | null,
): BotEnvironmentOption[] {
  const options: BotEnvironmentOption[] = [{ value: '', label: '本机' }]
  const byValue = new Map<string, string>()
  const labelCounts = new Map<string, number>()

  for (const agent of agents) {
    const conn = normalizeBotConnection(agent.connection_id)
    if (!conn || byValue.has(conn)) continue
    const label = connectionName(agent) || conn
    byValue.set(conn, label)
    labelCounts.set(label, (labelCounts.get(label) || 0) + 1)
  }

  for (const [value, label] of byValue.entries()) {
    const duplicate = (labelCounts.get(label) || 0) > 1
    options.push({
      value,
      label: duplicate ? `${label} (${value.slice(0, 12)})` : label,
    })
  }

  const current = normalizeBotConnection(currentConnectionId)
  if (current && !options.some((opt) => opt.value === current)) {
    const fallbackLabel = current.startsWith('wsl:')
      ? current.slice(4) || 'WSL'
      : current.startsWith('ssh-')
        ? `SSH (${current.slice(0, 12)}…)`
        : current
    options.push({ value: current, label: fallbackLabel })
  }

  return options
}
