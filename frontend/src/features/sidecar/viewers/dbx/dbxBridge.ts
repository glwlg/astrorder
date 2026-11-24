export interface DbxInjectSqlPayload {
  filename: string
  path: string
  content: string
  line?: number
  forceNew?: boolean
}

export interface DbxInjectResult {
  ok: boolean
  tabId?: string
  reused?: boolean
  connectionBound?: boolean
  reason?: string
}

interface DbxQueryTab {
  id: string
  title?: string
  customTitle?: boolean
  mode?: string
  connectionId?: string
  database?: string
  schema?: string
  catalog?: string
  sql?: string
  savedSqlId?: string
  externalSqlPath?: string
  isExecuting?: boolean
  editorRevealRequest?: { id: number; line: number; column: number }
}

interface DbxQueryStore {
  tabs?: DbxQueryTab[]
  activeTabId?: string | null
  isOpenTabsLoaded?: boolean
  createTab?: (
    connectionId: string,
    database: string,
    title?: string,
    mode?: string,
    schema?: string,
    sql?: string,
    catalog?: string,
    options?: Record<string, unknown>,
  ) => string
  activateTab?: (tabId: string) => void
  switchTab?: (tabId: string) => void
}

interface DbxVueApp {
  config?: {
    globalProperties?: {
      $pinia?: {
        _s?: Map<string, unknown>
      }
    }
  }
}

function getDbxQueryStore(iframe: HTMLIFrameElement | null): DbxQueryStore | null {
  if (!iframe) return null
  try {
    const win = iframe.contentWindow
    const doc = iframe.contentDocument
    if (!win || !doc) return null
    const root = doc.getElementById('root') as (HTMLElement & { __vue_app__?: DbxVueApp }) | null
    const stores = root?.__vue_app__?.config?.globalProperties?.$pinia?._s
    if (!stores || typeof stores.get !== 'function') return null
    const queryStore = stores.get('query') as DbxQueryStore | undefined
    if (!queryStore || typeof queryStore.createTab !== 'function') return null
    return queryStore
  } catch {
    return null
  }
}

let revealSeq = 1

export function tryInjectSqlSync(
  iframe: HTMLIFrameElement | null,
  payload: DbxInjectSqlPayload,
): DbxInjectResult | null {
  const store = getDbxQueryStore(iframe)
  if (!store || typeof store.createTab !== 'function') return null
  if (store.isOpenTabsLoaded === false) return null

  const tabs = Array.isArray(store.tabs) ? store.tabs : []
  const activeTab = tabs.find((tab) => tab.id === store.activeTabId) || null
  const connectionId = activeTab?.connectionId || ''
  const database = activeTab?.database || ''
  const schema = activeTab?.schema
  const catalog = activeTab?.catalog

  const existing = !payload.forceNew
    ? tabs.find(
        (tab) =>
          tab.mode === 'query' &&
          !tab.savedSqlId &&
          !tab.externalSqlPath &&
          tab.title === payload.filename,
      )
    : undefined

  if (existing && !existing.isExecuting) {
    existing.sql = payload.content
    if (payload.line && payload.line > 0) {
      existing.editorRevealRequest = { id: ++revealSeq, line: payload.line, column: 1 }
    }
    if (typeof store.activateTab === 'function') {
      store.activateTab(existing.id)
    } else if (typeof store.switchTab === 'function') {
      store.switchTab(existing.id)
    }
    return {
      ok: true,
      tabId: existing.id,
      reused: true,
      connectionBound: Boolean(existing.connectionId),
    }
  }

  // Never auto-execute: createTab initializes isExecuting = false and opens an
  // editable query tab so the user always chooses the target DB and runs manually.
  const tabId = store.createTab(
    connectionId,
    database,
    payload.filename,
    'query',
    schema,
    payload.content,
    catalog,
    { forceNew: true },
  )

  if (payload.line && payload.line > 0 && Array.isArray(store.tabs)) {
    const created = store.tabs.find((tab) => tab.id === tabId)
    if (created) {
      created.editorRevealRequest = { id: ++revealSeq, line: payload.line, column: 1 }
    }
  }

  return {
    ok: true,
    tabId,
    reused: false,
    connectionBound: Boolean(connectionId),
  }
}

export async function injectSqlIntoDbxFrame(
  iframe: HTMLIFrameElement | null,
  payload: DbxInjectSqlPayload,
  options?: { timeoutMs?: number; intervalMs?: number; signal?: AbortSignal },
): Promise<DbxInjectResult> {
  const timeoutMs = options?.timeoutMs ?? 8000
  const intervalMs = options?.intervalMs ?? 150
  const deadline = Date.now() + timeoutMs

  while (Date.now() <= deadline) {
    if (options?.signal?.aborted) {
      return { ok: false, reason: '已取消载入' }
    }
    const result = tryInjectSqlSync(iframe, payload)
    if (result) return result
    await new Promise((resolve) => window.setTimeout(resolve, intervalMs))
  }

  return {
    ok: false,
    reason: 'DBX 页面尚未完成初始化（如开启了密码保护，请先解锁后再点击载入）',
  }
}
