import { skipToken, type QueryClient, type UseQueryOptions } from '@tanstack/react-query'
import type { AccountPlatform, AgyQuota } from './agyAccountsApi'

const agyPrefix = 'astrorder:agy-quota:v2:'
const legacyPrefix = 'astrorder:agy-quota:v1:'
const platformPrefixBase = 'astrorder:account-quota:v1:'

function storageKey(id: string, platform: AccountPlatform = 'antigravity') {
  if (platform === 'antigravity') return agyPrefix + encodeURIComponent(id)
  return `${platformPrefixBase}${platform}:${encodeURIComponent(id)}`
}

function snapshotKey(environment: string, id: string, platform: AccountPlatform = 'antigravity') {
  if (platform === 'antigravity') {
    return id === 'current'
      ? ['agy-quota-snapshot', 'current', environment]
      : ['agy-quota-snapshot', 'saved', id]
  }
  return id === 'current'
    ? ['account-quota-snapshot', platform, 'current', environment]
    : ['account-quota-snapshot', platform, 'saved', id]
}

function latest(a: AgyQuota | undefined, b: AgyQuota | undefined) {
  if (!a || !b) return a ?? b
  return Date.parse(a.as_of) > Date.parse(b.as_of) ? a : b
}

function readSaved(id: string, platform: AccountPlatform = 'antigravity'): AgyQuota | undefined {
  const key = storageKey(id, platform)
  const saved = localStorage.getItem(key)
  if (saved) return JSON.parse(saved)
  if (platform !== 'antigravity') return undefined

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

export function snapshotOptions(
  environment: string,
  id: string,
  platform: AccountPlatform = 'antigravity',
): UseQueryOptions<AgyQuota> {
  return {
    queryKey: snapshotKey(environment, id, platform), queryFn: skipToken, staleTime: Infinity,
    initialData: (): AgyQuota | undefined => id === 'current' ? undefined : readSaved(id, platform),
  }
}

export function storeQuotaSnapshot(
  client: QueryClient,
  environment: string,
  id: string,
  value: AgyQuota,
  platform: AccountPlatform = 'antigravity',
) {
  const key = snapshotKey(environment, id, platform)
  const previous = client.getQueryData<AgyQuota>(key)
  const newest = latest(id === 'current' ? previous : latest(previous, readSaved(id, platform)), value)!
  if (id !== 'current') localStorage.setItem(storageKey(id, platform), JSON.stringify(newest))
  client.setQueryData(key, newest)
}

export function syncQuotaWindows(client: QueryClient) {
  function onStorage(event: StorageEvent) {
    if (event.storageArea !== localStorage || !event.key || !event.newValue) return
    if (event.key.startsWith(agyPrefix)) {
      const id = decodeURIComponent(event.key.slice(agyPrefix.length))
      const value: AgyQuota = JSON.parse(event.newValue)
      client.setQueryData<AgyQuota>(snapshotKey('', id, 'antigravity'), cached => latest(cached, value))
      return
    }
    if (event.key.startsWith(platformPrefixBase)) {
      const rest = event.key.slice(platformPrefixBase.length)
      const sep = rest.indexOf(':')
      if (sep <= 0) return
      const platform = rest.slice(0, sep) as AccountPlatform
      if (platform !== 'codex' && platform !== 'grok') return
      const id = decodeURIComponent(rest.slice(sep + 1))
      const value: AgyQuota = JSON.parse(event.newValue)
      client.setQueryData<AgyQuota>(snapshotKey('', id, platform), cached => latest(cached, value))
    }
  }
  window.addEventListener('storage', onStorage)
  return () => window.removeEventListener('storage', onStorage)
}
