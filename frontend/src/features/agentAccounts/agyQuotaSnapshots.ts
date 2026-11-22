import { skipToken, type QueryClient, type UseQueryOptions } from '@tanstack/react-query'
import type { AgyQuota } from './agyAccountsApi'

const prefix = 'astrorder:agy-quota:v2:'
const legacyPrefix = 'astrorder:agy-quota:v1:'
const storageKey = (id: string) => prefix + encodeURIComponent(id)
const snapshotKey = (environment: string, id: string) => id === 'current'
  ? ['agy-quota-snapshot', 'current', environment]
  : ['agy-quota-snapshot', 'saved', id]

function latest(a: AgyQuota | undefined, b: AgyQuota | undefined) {
  if (!a || !b) return a ?? b
  return Date.parse(a.as_of) > Date.parse(b.as_of) ? a : b
}

function readSaved(id: string): AgyQuota | undefined {
  const key = storageKey(id)
  const saved = localStorage.getItem(key)
  if (saved) return JSON.parse(saved)

  // Older clients kept one snapshot per environment and saved account.
  let newest: AgyQuota | undefined
  for (let i = 0; i < localStorage.length; i++) {
    const legacy = localStorage.key(i)
    if (legacy?.startsWith(legacyPrefix) && legacy.endsWith(`:${encodeURIComponent(id)}`)) {
      newest = latest(newest, JSON.parse(localStorage.getItem(legacy)!))
    }
  }
  if (newest) localStorage.setItem(key, JSON.stringify(newest))
  return newest
}

export function snapshotOptions(environment: string, id: string): UseQueryOptions<AgyQuota> {
  return {
    queryKey: snapshotKey(environment, id), queryFn: skipToken, staleTime: Infinity,
    initialData: (): AgyQuota | undefined => id === 'current' ? undefined : readSaved(id),
  }
}

export function storeQuotaSnapshot(client: QueryClient, environment: string, id: string, value: AgyQuota) {
  const key = snapshotKey(environment, id)
  const previous = client.getQueryData<AgyQuota>(key)
  const newest = latest(id === 'current' ? previous : latest(previous, readSaved(id)), value)!
  if (id !== 'current') localStorage.setItem(storageKey(id), JSON.stringify(newest))
  client.setQueryData(key, newest)
}

export function syncQuotaWindows(client: QueryClient) {
  function onStorage(event: StorageEvent) {
    if (event.storageArea !== localStorage || !event.key?.startsWith(prefix) || !event.newValue) return
    const id = decodeURIComponent(event.key.slice(prefix.length))
    const value: AgyQuota = JSON.parse(event.newValue)
    client.setQueryData<AgyQuota>(snapshotKey('', id), cached => latest(cached, value))
  }
  window.addEventListener('storage', onStorage)
  return () => window.removeEventListener('storage', onStorage)
}
