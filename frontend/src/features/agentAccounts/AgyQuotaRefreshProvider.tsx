import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Agent, Session } from '../../domain/types'
import { useRefreshSettings, useScheduledRefresh } from './accountRefresh'
import { useAgyQuotaRefresh } from './useAgyAccountOverview'
import { syncQuotaWindows } from './agyQuotaSnapshots'

export function agySessionEnvironment(session?: Session, agent?: Agent) {
  if (!session || agent?.kind.replace(/-ssh$/, '') !== 'antigravity') return ''
  if (session.connection_id && agent.connection_id && session.connection_id !== agent.connection_id) return ''
  return session.connection_id || agent.connection_id || (agent.kind.endsWith('-ssh') ? '' : 'local')
}

type PageTarget = { environment: string; paused: boolean }
const PageTargetContext = createContext<((target: PageTarget | null) => void) | null>(null)

export function useAccountPageRefreshTarget(environment: string, paused: boolean) {
  const setTarget = useContext(PageTargetContext)
  useEffect(() => {
    setTarget?.({ environment, paused })
    return () => setTarget?.(null)
  }, [environment, paused, setTarget])
}

export function AgyQuotaRefreshProvider({ sessionEnvironment, children }: { sessionEnvironment: string; children: ReactNode }) {
  const [page, setPage] = useState<PageTarget | null>(null)
  const [settings] = useRefreshSettings('antigravity')
  const environment = page?.environment ?? sessionEnvironment
  const refresh = useAgyQuotaRefresh(environment)
  const client = useQueryClient()
  useEffect(() => syncQuotaWindows(client), [client])
  useScheduledRefresh(environment, settings, scope => {
    if (page?.paused || client.isMutating({ mutationKey: ['agy-account-action', environment] }) ||
        client.isFetching({ queryKey: ['agy-account-quota', environment] })) return
    void refresh(scope)
  })
  return <PageTargetContext.Provider value={setPage}>{children}</PageTargetContext.Provider>
}
