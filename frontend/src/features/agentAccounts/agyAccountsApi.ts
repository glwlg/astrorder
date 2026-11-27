import { request } from '../../api/client'

export type AccountPlatform = 'antigravity' | 'codex' | 'grok'
export const supportedAccountPlatforms: readonly AccountPlatform[] = ['antigravity', 'codex', 'grok']

// Paused to comply with Google usage policy; account saving, import, and switching stay enabled.
export const AGY_QUOTA_PAUSED = true
export const AGY_QUOTA_PAUSED_MESSAGE = 'AGY 账号额度查询已暂停（遵循 Google 使用政策），账号导入、保存与切换不受影响。'

export function isPlatformQuotaPaused(platform: AccountPlatform): boolean {
  return AGY_QUOTA_PAUSED && platform === 'antigravity'
}

export function isDirectAntigravityModelRoute(modelRoute: string, agentKind?: string): boolean {
  if (!AGY_QUOTA_PAUSED || !modelRoute) return false
  const trimmed = modelRoute.trim()
  if (trimmed.startsWith('antigravity/')) {
    const rest = trimmed.slice('antigravity/'.length)
    return rest.length > 0 && !rest.includes('/')
  }
  const baseKind = agentKind?.replace(/-ssh$/, '')
  return baseKind === 'antigravity' && !trimmed.includes('/')
}

export function isAccountPlatform(kind?: string | null): kind is AccountPlatform {
  return kind === 'antigravity' || kind === 'codex' || kind === 'grok'
}

export type AccountPlatformMeta = {
  platform: AccountPlatform
  label: string
  shortLabel: string
  routePath: string
  credentialLabel: string
  fileLabel: string
  emptyHint: string
  targetEmptyHint: string
  notLoggedInHint: string
  canDelete: boolean
}

const platformMetaMap: Record<AccountPlatform, AccountPlatformMeta> = {
  antigravity: {
    platform: 'antigravity',
    label: 'Antigravity',
    shortLabel: 'AGY',
    routePath: '/agent-accounts',
    credentialLabel: 'AGY Token JSON',
    fileLabel: 'AGY Token 文件（最大 1 MB）',
    emptyHint: '暂无已保存账号，可导入 AGY Token JSON 或保存当前账号。',
    targetEmptyHint: '接入 AGY Agent 后即可保存和切换账号。',
    notLoggedInHint: '目标 Agent 尚未登录 AGY',
    canDelete: false,
  },
  codex: {
    platform: 'codex',
    label: 'Codex',
    shortLabel: 'Codex',
    routePath: '/agent-accounts/codex',
    credentialLabel: 'Codex auth.json',
    fileLabel: 'Codex 凭证文件（~/.codex/auth.json，最大 1 MB）',
    emptyHint: '暂无已保存账号，可导入 ~/.codex/auth.json 或保存当前账号。',
    targetEmptyHint: '接入 Codex Agent 后即可应用账号和查询额度。',
    notLoggedInHint: '目标 Agent 尚未登录 Codex',
    canDelete: true,
  },
  grok: {
    platform: 'grok',
    label: 'Grok',
    shortLabel: 'Grok',
    routePath: '/agent-accounts/grok',
    credentialLabel: 'Grok auth.json',
    fileLabel: 'Grok 凭证文件（~/.grok/auth.json，最大 1 MB）',
    emptyHint: '暂无已保存账号，可导入 ~/.grok/auth.json 或保存当前账号。',
    targetEmptyHint: '接入 Grok Agent 后即可应用账号和查询订阅状态。',
    notLoggedInHint: '目标 Agent 尚未登录 Grok',
    canDelete: true,
  },
}

export function accountPlatformMeta(platform: AccountPlatform): AccountPlatformMeta {
  return platformMetaMap[platform]
}

export type SavedAgyAccount = { id: string; name: string; email?: string; auth_mode?: string }
export type AgyAccount = SavedAgyAccount & { current: boolean }
export type AgyTarget = { id: string; name: string }
export type AgyQuotaBucket = { id: string; name: string; window: string; remaining_fraction: number; reset_at?: number }
export type AgyQuota = {
  plan?: string
  tier_id?: string
  account_label?: string
  detail?: string
  groups: Array<{ name: string; buckets: AgyQuotaBucket[] }>
  as_of: string
}

export type PlatformAccountsApi = {
  importPath: string
  targets: () => Promise<{ items: AgyTarget[] }>
  accounts: (signal?: AbortSignal) => Promise<{ items: SavedAgyAccount[] }>
  targetState: (environment: string, signal?: AbortSignal) => Promise<{ current_account_id: string; quota_supported: boolean; quota_paused?: boolean }>
  quota: (environment: string, account: string, signal?: AbortSignal) => Promise<AgyQuota>
  importCredential: (name: string, credential: string) => Promise<{ account_id: string }>
  save: (environment: string, name: string) => Promise<{ account_id: string }>
  switch: (environment: string, account: string, force: boolean) => Promise<{ account_id: string; restarted: boolean }>
  deleteAccount?: (account: string) => Promise<{ deleted: boolean; account_id: string }>
}

const accountsPath = '/agy-accounts'
export const agyImportPath = `${accountsPath}/import`
export const agyImportMaxBytes = 1024 * 1024

function scopedPath(base: string, environment: string, account?: string) {
  const params = new URLSearchParams({ environment_id: environment })
  if (account) params.set('account_id', account)
  return `${base}?${params}`
}

function post<T>(path: string, body: object) {
  return request<T>(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
}

function createPlatformAccountsApi(basePath: string, withDelete: boolean): PlatformAccountsApi {
  const api: PlatformAccountsApi = {
    importPath: `${basePath}/import`,
    targets: () => request<{ items: AgyTarget[] }>(`${basePath}/targets`),
    accounts: (signal?: AbortSignal) => request<{ items: SavedAgyAccount[] }>(basePath, { signal }),
    targetState: (environment: string, signal?: AbortSignal) =>
      request<{ current_account_id: string; quota_supported: boolean; quota_paused?: boolean }>(scopedPath(basePath, environment), { signal }),
    quota: (environment: string, account: string, signal?: AbortSignal) =>
      request<AgyQuota>(scopedPath(basePath, environment, account), { signal }),
    importCredential: (name: string, credential: string) =>
      post<{ account_id: string }>(`${basePath}/import`, { name, credential }),
    save: (environment: string, name: string) =>
      post<{ account_id: string }>(scopedPath(basePath, environment), { action: 'save', name }),
    switch: (environment: string, account: string, force: boolean) =>
      post<{ account_id: string; restarted: boolean }>(scopedPath(basePath, environment), {
        action: 'switch', account_id: account, confirm_active: force,
      }),
  }
  if (withDelete) {
    api.deleteAccount = (account: string) =>
      post<{ deleted: boolean; account_id: string }>(basePath, { action: 'delete', account_id: account })
  }
  return api
}

export const agyAccountsApi: PlatformAccountsApi = createPlatformAccountsApi(accountsPath, false)
export const codexAccountsApi: PlatformAccountsApi = createPlatformAccountsApi('/agent-accounts/codex', true)
export const grokAccountsApi: PlatformAccountsApi = createPlatformAccountsApi('/agent-accounts/grok', true)

export function accountsApiFor(platform: AccountPlatform): PlatformAccountsApi {
  switch (platform) {
    case 'codex': return codexAccountsApi
    case 'grok': return grokAccountsApi
    default: return agyAccountsApi
  }
}
