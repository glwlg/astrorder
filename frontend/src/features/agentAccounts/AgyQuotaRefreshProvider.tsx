import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Agent, Session } from '../../domain/types'
import { isAccountPlatform, type AccountPlatform } from './agyAccountsApi'
import { useRefreshSettings, useScheduledRefresh } from './accountRefresh'
import { accountActionMutationKey } from './useAgyAccountMutation'
import { accountQuotaQueryPrefix, useAgyQuotaRefresh } from './useAgyAccountOverview'
import { syncQuotaWindows } from './agyQuotaSnapshots'

export function sessionAccountTarget(session?: Session, agent?: Agent): { platform: AccountPlatform; environment: string } | null {
  if (!session || !agent) return null
  const baseKind = agent.kind.replace(/-ssh$/, '')
  if (!isAccountPlatform(baseKind)) return null
  if (session.connection_id && agent.connection_id && session.connection_id !== agent.connection_id) return null
  const environment = session.connection_id || agent.connection_id || (agent.kind.endsWith('-ssh') ? '' : 'local')
  if (!environment) return null
  return { platform: baseKind, environment }
}

export function agySessionEnvironment(session?: Session, agent?: Agent) {
  const target = sessionAccountTarget(session, agent)
  return target?.platform === 'antigravity' ? target.environment : ''
}

type PageTarget = { platform: AccountPlatform; environment: string; paused: boolean }
const PageTargetContext = createContext<((target: PageTarget | null) => void) | null>(null)

export function useAccountPageRefreshTarget(environment: string, paused: boolean, platform: AccountPlatform = 'antigravity') {
  const setTarget = useContext(PageTargetContext)
  useEffect(() => {
    setTarget?.({ platform, environment, paused })
    return () => setTarget?.(null)
  }, [platform, environment, paused, setTarget])
}

export function AgyQuotaRefreshProvider({
  sessionEnvironment,
  sessionPlatform = 'antigravity',
  children,
}: {
  sessionEnvironment: string
  sessionPlatform?: AccountPlatform
  children: ReactNode
}) {
  const [page, setPage] = useState<PageTarget | null>(null)
  const platform = page?.platform ?? sessionPlatform
  const environment = page?.environment ?? sessionEnvironment
  const [settings] = useRefreshSettings(platform)
  const refresh = useAgyQuotaRefresh(environment, platform)
  const client = useQueryClient()
  useEffect(() => syncQuotaWindows(client), [client])
  useScheduledRefresh(environment, settings, scope => {
    if (
      page?.paused ||
      client.isMutating({ mutationKey: accountActionMutationKey(environment, platform) }) ||
      client.isFetching({ queryKey: accountQuotaQueryPrefix(environment, platform) })
    ) return
    void refresh(scope)
  })
  return <PageTargetContext.Provider value={setPage}>{children}</PageTargetContext.Provider>
}
