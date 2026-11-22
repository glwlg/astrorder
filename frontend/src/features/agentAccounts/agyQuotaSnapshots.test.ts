import { QueryClient } from '@tanstack/react-query'
import { afterEach, expect, it } from 'vitest'
import type { AgyQuota } from './agyAccountsApi'
import { snapshotOptions, storeQuotaSnapshot, syncQuotaWindows } from './agyQuotaSnapshots'

const old: AgyQuota = { as_of: '2026-11-21T14:00:00Z', plan: 'Free', groups: [] }
const fresh: AgyQuota = { as_of: '2026-11-21T15:00:00Z', plan: 'Pro', groups: [] }
afterEach(() => localStorage.clear())

it('migrates the newest legacy copy and keeps it when an older request completes later', () => {
  localStorage.setItem('astrorder:agy-quota:v1:wsl:a', JSON.stringify(fresh))
  localStorage.setItem('astrorder:agy-quota:v1:local:a', JSON.stringify(old))
  const client = new QueryClient()
  storeQuotaSnapshot(client, 'local', 'a', old)
  expect(client.getQueryData(snapshotOptions('wsl', 'a').queryKey)).toEqual(fresh)
  expect(JSON.parse(localStorage.getItem('astrorder:agy-quota:v2:a')!)).toEqual(fresh)
  expect(client.getQueryData(snapshotOptions('wsl', 'b').queryKey)).toBeUndefined()
})

it('shares saved snapshots between windows, rejects older events and isolates unsaved logins', () => {
  const client = new QueryClient()
  const stop = syncQuotaWindows(client)
  try {
    const event = (value: AgyQuota) => window.dispatchEvent(new StorageEvent('storage', {
      key: 'astrorder:agy-quota:v2:a', newValue: JSON.stringify(value), storageArea: localStorage,
    }))
    event(fresh)
    event(old)
    expect(client.getQueryData(snapshotOptions('wsl', 'a').queryKey)).toEqual(fresh)
    storeQuotaSnapshot(client, 'wsl', 'current', old)
    expect(client.getQueryData(snapshotOptions('local', 'current').queryKey)).toBeUndefined()
    expect(localStorage.getItem('astrorder:agy-quota:v2:current')).toBeNull()
  } finally {
    stop()
  }
})
