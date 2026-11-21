import { QueryObserver, useIsMutating, useMutation, useQueries, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
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
  const accounts = useQuery({ ...metadataPolicy, ...libraryOptions })
  const state = useQuery({ ...metadataPolicy, ...targetStateOptions(environment),
    enabled: readCurrent && Boolean(environment), staleTime: readCurrent ? 0 : Infinity, refetchOnMount: readCurrent })
  const currentID = state.data?.current_account_id
  const rows = (accounts.data?.items ?? []).map(account => ({ ...account, current: account.id === currentID }))
  if (currentID === 'current') rows.unshift({ id: 'current', name: '未保存账号', current: true })
  const quotas = useQueries({ queries: rows.map(row => ({ ...quotaOptions(environment, row.id), enabled: false })) })
  const refresh = useAgyQuotaRefresh(environment)
  const refreshing = useIsMutating({ mutationKey: refreshKey(environment), exact: true }) > 0
  async function refreshLibrary() {
    await accounts.refetch()
    if (environment) await state.refetch()
  }
  return { accounts, state, rows, quotas, currentID, refresh, refreshLibrary, refreshing }
}

const refreshKey = (environment: string) => ['agy-quota-refresh', environment]
type RefreshMode = 'all' | 'current' | { id: string }

async function fetchForRefresh<T>(client: QueryClient, options: {
  queryKey: string[]; queryFn: (ctx: { signal: AbortSignal }) => Promise<T>
}) {
  // A refresh owns its reads until completion, even when the last view unmounts.
  const observer = new QueryObserver(client, { ...options, enabled: false })
  const release = observer.subscribe(() => {})
  try {
    return await client.fetchQuery({ ...options, staleTime: 0, retry: false })
  } finally {
    release()
  }
}

export function useAgyQuotaRefresh(environment: string) {
  const client = useQueryClient()
  const mutationKey = refreshKey(environment)
  const job = useMutation({
    mutationKey, retry: false,
    mutationFn: async ({ target, mode }: { target: string; mode: RefreshMode }) => {
      const [latest, library] = await Promise.allSettled([
        fetchForRefresh(client, targetStateOptions(target)),
        mode === 'all' ? fetchForRefresh(client, libraryOptions) : undefined,
      ])
      if (latest.status === 'rejected' || !latest.value.quota_supported || library.status === 'rejected') return
      const active = latest.value.current_account_id
      const ids = new Set<string>()
      library.value?.items.forEach(account => ids.add(account.id))
      if (typeof mode === 'object') ids.add(mode.id)
      else if (active) ids.add(active)
      // Each row observes its own result, so one unavailable account does not erase the others.
      await Promise.allSettled([...ids].map(id => fetchForRefresh(client, quotaOptions(target, id))))
    },
  })
  return (mode: RefreshMode) => {
    if (environment && !client.isMutating({ mutationKey, exact: true })) job.mutate({ target: environment, mode })
  }
}
