import { useEffect, useRef } from 'react'
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { agyAccountsApi, type AgyQuota } from './agyAccountsApi'

export const metadataPolicy = { staleTime: Infinity, retry: false, retryOnMount: false, refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false } as const
export const libraryOptions = { queryKey: ['agy-accounts'], queryFn: ({ signal }: { signal: AbortSignal }) => agyAccountsApi.accounts(signal) }
export const targetStateOptions = (environment: string) => ({
  queryKey: ['agy-account-state', environment],
  queryFn: ({ signal }: { signal: AbortSignal }) => agyAccountsApi.targetState(environment, signal),
})
const quotaKey = (environment: string, id: string) => ['agy-account-quota', environment, id]

function quotaOptions(environment: string, id: string) {
  const key = `astrorder:agy-quota:v1:${encodeURIComponent(environment)}:${encodeURIComponent(id)}`
  return {
    queryKey: quotaKey(environment, id), retry: false,
    initialData: () => {
      if (id === 'current') return undefined
      const cached = localStorage.getItem(key)
      return cached ? JSON.parse(cached) as AgyQuota : undefined
    },
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const value = await agyAccountsApi.quota(environment, id, signal)
      if (!signal.aborted && id !== 'current') localStorage.setItem(key, JSON.stringify(value))
      return value
    },
  }
}

export function useAgyAccountOverview(environment: string, readCurrent = false) {
  const client = useQueryClient()
  const accounts = useQuery({ ...metadataPolicy, ...libraryOptions })
  const state = useQuery({ ...metadataPolicy, ...targetStateOptions(environment),
    enabled: readCurrent && Boolean(environment), staleTime: readCurrent ? 0 : Infinity, refetchOnMount: readCurrent })
  const currentID = state.data?.current_account_id
  const rows = (accounts.data?.items ?? []).map(account => ({ ...account, current: account.id === currentID }))
  if (currentID === 'current') rows.unshift({ id: 'current', name: '未保存账号', current: true })
  const quotas = useQueries({ queries: rows.map(row => ({ ...quotaOptions(environment, row.id), enabled: false })) })
  const busy = useRef(false)
  const scope = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    scope.current = controller
    return () => { controller.abort(); busy.current = false }
  }, [environment])

  async function refresh(mode: 'all' | 'current' | { id: string }) {
    if (!environment || busy.current) return
    const controller = scope.current
    busy.current = true
    try {
      const [latest, library] = await Promise.all([
        state.refetch({ cancelRefetch: false }),
        mode === 'all' ? accounts.refetch({ cancelRefetch: false }) : accounts,
      ])
      if (controller?.signal.aborted || latest.isError || !latest.data?.quota_supported || (mode === 'all' && library.isError)) return
      const active = latest.data.current_account_id
      const ids = new Set<string>()
      if (mode === 'all') library.data?.items.forEach(account => ids.add(account.id))
      if (typeof mode === 'object') ids.add(mode.id)
      else if (active) ids.add(active)
      // Each row observes its own result, so one unavailable account does not erase the others.
      await Promise.allSettled([...ids].map(id => client.fetchQuery({ ...quotaOptions(environment, id), staleTime: 0 })))
    } finally {
      if (!controller?.signal.aborted) busy.current = false
    }
  }
  async function refreshLibrary() {
    await accounts.refetch()
    if (environment) await state.refetch()
  }
  return { accounts, state, rows, quotas, currentID, refresh, refreshLibrary,
    refreshing: state.isFetching || quotas.some(quota => quota.isFetching) }
}
