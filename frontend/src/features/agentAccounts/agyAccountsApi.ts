import { request } from '../../api/client'

export type SavedAgyAccount = { id: string; name: string }
export type AgyAccount = SavedAgyAccount & { current: boolean }
export type AgyTarget = { id: string; name: string }
export type AgyQuotaBucket = { id: string; name: string; window: string; remaining_fraction: number; reset_at?: number }
export type AgyQuota = { plan?: string; tier_id?: string; groups: Array<{ name: string; buckets: AgyQuotaBucket[] }>; as_of: string }

const accountsPath = '/agy-accounts'
export const agyImportPath = `${accountsPath}/import`
export const agyImportMaxBytes = 1024 * 1024
function scopedPath(environment: string, account?: string) {
  const params = new URLSearchParams({ environment_id: environment })
  if (account) params.set('account_id', account)
  return `${accountsPath}?${params}`
}

function post<T>(path: string, body: object) {
  return request<T>(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
}

export const agyAccountsApi = {
  targets: () => request<{ items: AgyTarget[] }>(`${accountsPath}/targets`),
  accounts: (signal?: AbortSignal) => request<{ items: SavedAgyAccount[] }>(accountsPath, { signal }),
  targetState: (environment: string, signal?: AbortSignal) =>
    request<{ current_account_id: string; quota_supported: boolean }>(scopedPath(environment), { signal }),
  quota: (environment: string, account: string, signal?: AbortSignal) =>
    request<AgyQuota>(scopedPath(environment, account), { signal }),
  importCredential: (name: string, credential: string) =>
    post<{ account_id: string }>(agyImportPath, { name, credential }),
  save: (environment: string, name: string) => post<{ account_id: string }>(scopedPath(environment), { action: 'save', name }),
  switch: (environment: string, account: string, force: boolean) =>
    post<{ account_id: string; restarted: boolean }>(scopedPath(environment), { action: 'switch', account_id: account, confirm_active: force }),
}
