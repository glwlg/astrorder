import { QueryObserver, useIsMutating, useMutation, useQueries, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { accountsApiFor, type AccountPlatform } from './agyAccountsApi'
import { snapshotOptions, storeQuotaSnapshot } from './agyQuotaSnapshots'

export const metadataPolicy = { staleTime: Infinity, retry: false, retryOnMount: false, refetchOnMount: false, refetchOnWindowFocus: false, refetchOnReconnect: false } as const

export function platformTargetsOptions(platform: AccountPlatform = 'antigravity') {
  return {
    queryKey: platform === 'antigravity' ? ['agy-account-targets'] : ['agent-account-targets', platform],
    queryFn: () => accountsApiFor(platform).targets(),
  }
}

export function platformLibraryOptions(platform: AccountPlatform = 'antigravity') {
  return {
    queryKey: platform === 'antigravity' ? ['agy-accounts'] : ['agent-accounts', platform],
    queryFn: ({ signal }: { signal: AbortSignal }) => accountsApiFor(platform).accounts(signal),
  }
}

export function platformTargetStateOptions(environment: string, platform: AccountPlatform = 'antigravity') {
  return {
    queryKey: platform === 'antigravity' ? ['agy-account-state', environment] : ['agent-account-state', platform, environment],
    queryFn: ({ signal }: { signal: AbortSignal }) => accountsApiFor(platform).targetState(environment, signal),
  }
}

export const libraryOptions = platformLibraryOptions('antigravity')
export const targetStateOptions = (environment: string) => platformTargetStateOptions(environment, 'antigravity')

export function accountQuotaQueryPrefix(environment: string, platform: AccountPlatform = 'antigravity') {
  return platform === 'antigravity' ? ['agy-account-quota', environment] : ['agent-account-quota', platform, environment]
}

const quotaKey = (environment: string, id: string, platform: AccountPlatform = 'antigravity') =>
  platform === 'antigravity' ? ['agy-account-quota', environment, id] : ['agent-account-quota', platform, environment, id]

function quotaOptions(client: QueryClient, environment: string, id: string, platform: AccountPlatform = 'antigravity') {
  return {
    queryKey: quotaKey(environment, id, platform), retry: false,
    queryFn: async ({ signal }: { signal: AbortSignal }) => {
      const value = await accountsApiFor(platform).quota(environment, id, signal)
      if (!signal.aborted) storeQuotaSnapshot(client, environment, id, value, platform)
      return null
    },
  }
}

export function useAgyAccountOverview(environment: string, platform: AccountPlatform = 'antigravity') {
  const client = useQueryClient()
  const accounts = useQuery({ ...metadataPolicy, ...platformLibraryOptions(platform) })
  const state = useQuery({
    ...metadataPolicy, ...platformTargetStateOptions(environment, platform),
    enabled: Boolean(environment), staleTime: 30_000, refetchOnMount: true,
  })
  const currentID = state.data?.current_account_id
  const rows = (accounts.data?.items ?? []).map(account => ({ ...account, current: account.id === currentID }))
  if (currentID === 'current') rows.unshift({ id: 'current', name: '未保存账号', current: true })
  const reads = useQueries({ queries: rows.map(row => ({ ...quotaOptions(client, environment, row.id, platform), enabled: false })) })
  const snapshots = useQueries({ queries: rows.map(row => snapshotOptions(environment, row.id, platform)) })
  const quotas = reads.map((read, i) => ({ data: snapshots[i].data, isFetching: read.isFetching, error: read.error }))
  const refresh = useAgyQuotaRefresh(environment, platform)
  const refreshing = useIsMutating({ mutationKey: refreshKey(environment, platform), exact: true }) > 0
  async function refreshLibrary() {
    await accounts.refetch()
    if (environment) await state.refetch()
  }
  return { accounts, state, rows, quotas, currentID, refresh, refreshLibrary, refreshing }
}

const refreshKey = (environment: string, platform: AccountPlatform = 'antigravity') =>
  platform === 'antigravity' ? ['agy-quota-refresh', environment] : ['agent-quota-refresh', platform, environment]

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

export function useAgyQuotaRefresh(environment: string, platform: AccountPlatform = 'antigravity') {
  const client = useQueryClient()
  const mutationKey = refreshKey(environment, platform)
  const job = useMutation({
    mutationKey, retry: false,
    mutationFn: async ({ target, mode }: { target: string; mode: RefreshMode }) => {
      const [latest, library] = await Promise.allSettled([
        fetchForRefresh(client, platformTargetStateOptions(target, platform)),
        mode === 'all' ? fetchForRefresh(client, platformLibraryOptions(platform)) : undefined,
      ])
      if (latest.status === 'rejected' || !latest.value.quota_supported || library.status === 'rejected') return
      const active = latest.value.current_account_id
      const ids = new Set<string>()
      library.value?.items.forEach(account => ids.add(account.id))
      if (typeof mode === 'object') ids.add(mode.id)
      else if (active) ids.add(active)
      // Each row observes its own result, so one unavailable account does not erase the others.
      await Promise.allSettled([...ids].map(id => fetchForRefresh(client, quotaOptions(client, target, id, platform))))
    },
  })
  return (mode: RefreshMode) => {
    if (environment && !client.isMutating({ mutationKey, exact: true })) job.mutate({ target: environment, mode })
  }
}
