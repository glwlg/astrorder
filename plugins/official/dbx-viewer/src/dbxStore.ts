import { useSyncExternalStore } from 'react'
import type {
  AstrorderPluginContext,
  OpenSqlFileParams,
  OpenSqlSnippetParams,
} from '@astrorder/sdk'
import {
  buildSqlSnippetFilename,
  extractSqlFileName,
  extractSqlLine,
  stripSqlLocationSuffix,
} from './sqlFormats'

export const DBX_PLUGIN_ID = 'dbx-viewer'
export const DBX_MEDIA_TYPE = 'application/x-astrorder-dbx'
export const DBX_ALT_MEDIA_TYPE = 'application/x-dbx'
export const DBX_DEFAULT_UPSTREAM_URL = 'http://127.0.0.1:14224'
export const DBX_PROXY_ENTRY_URL = '/api/v1/plugins/dbx/'

export interface DbxSqlEntry {
  id: string
  path: string
  filename: string
  content: string
  line?: number
  isSnippet?: boolean
  sessionId: string
  agentId: string
  connectionId?: string
  status: 'pending' | 'injected' | 'failed'
  statusDetail?: string
  executed: boolean
  updatedAt: number
}

export interface DbxPendingInject {
  requestId: number
  sessionId: string
  entryId: string
  forceNew?: boolean
}

export interface DbxSidecarSnapshot {
  itemsBySession: Record<string, DbxSqlEntry[]>
  pendingRequest: DbxPendingInject | null
  nextRequestId: number
}

const MAX_ITEMS_PER_SESSION = 40
export const EMPTY_SQL_LIST: DbxSqlEntry[] = []

function normalizePathKey(path: string): string {
  return path.trim().replace(/\\/g, '/').toLowerCase()
}

type StoreListener = () => void

class DbxSidecarStore {
  private state: DbxSidecarSnapshot = {
    itemsBySession: {},
    pendingRequest: null,
    nextRequestId: 1,
  }

  private listeners = new Set<StoreListener>()

  getState = (): DbxSidecarSnapshot => this.state

  subscribe = (listener: StoreListener): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  private setState(partial: Partial<DbxSidecarSnapshot>): void {
    this.state = { ...this.state, ...partial }
    for (const listener of [...this.listeners]) {
      listener()
    }
  }

  enqueueSql(
    sessionId: string,
    input: {
      path: string
      filename: string
      content: string
      line?: number
      isSnippet?: boolean
      agentId?: string
      connectionId?: string
    },
    options?: { forceNew?: boolean },
  ): DbxSqlEntry {
    const key = sessionId || 'global'
    const existingList = this.state.itemsBySession[key] || EMPTY_SQL_LIST
    const norm = normalizePathKey(input.path)
    const now = Date.now()
    const requestId = this.state.nextRequestId

    const existing = existingList.find((item) => normalizePathKey(item.path) === norm)
    const entry: DbxSqlEntry = existing
      ? {
          ...existing,
          path: input.path,
          filename: input.filename,
          content: input.content,
          line: input.line,
          isSnippet: input.isSnippet ?? existing.isSnippet,
          agentId: input.agentId ?? existing.agentId,
          connectionId: input.connectionId ?? existing.connectionId,
          status: 'pending',
          statusDetail: undefined,
          updatedAt: now,
        }
      : {
          id: `sql:${now}:${requestId}`,
          path: input.path,
          filename: input.filename,
          content: input.content,
          line: input.line,
          isSnippet: input.isSnippet,
          sessionId: key,
          agentId: input.agentId || '',
          connectionId: input.connectionId,
          status: 'pending',
          executed: false,
          updatedAt: now,
        }

    const nextList = existing
      ? existingList.map((item) => (item.id === existing.id ? entry : item))
      : [...existingList, entry].slice(-MAX_ITEMS_PER_SESSION)

    this.setState({
      itemsBySession: { ...this.state.itemsBySession, [key]: nextList },
      pendingRequest: {
        requestId,
        sessionId: key,
        entryId: entry.id,
        forceNew: options?.forceNew,
      },
      nextRequestId: requestId + 1,
    })

    return entry
  }

  requestInject(sessionId: string, entryId: string, forceNew?: boolean): void {
    const key = sessionId || 'global'
    const requestId = this.state.nextRequestId
    this.setState({
      pendingRequest: { requestId, sessionId: key, entryId, forceNew },
      nextRequestId: requestId + 1,
    })
  }

  clearPendingRequest(requestId: number): void {
    const curr = this.state.pendingRequest
    if (curr && curr.requestId === requestId) {
      this.setState({ pendingRequest: null })
    }
  }

  markEntryStatus(
    sessionId: string,
    entryId: string,
    status: DbxSqlEntry['status'],
    detail?: string,
  ): void {
    const key = sessionId || 'global'
    const list = this.state.itemsBySession[key]
    if (!list) return
    this.setState({
      itemsBySession: {
        ...this.state.itemsBySession,
        [key]: list.map((item) =>
          item.id === entryId ? { ...item, status, statusDetail: detail } : item,
        ),
      },
    })
  }

  toggleExecuted(sessionId: string, entryId: string): void {
    const key = sessionId || 'global'
    const list = this.state.itemsBySession[key]
    if (!list) return
    this.setState({
      itemsBySession: {
        ...this.state.itemsBySession,
        [key]: list.map((item) =>
          item.id === entryId ? { ...item, executed: !item.executed } : item,
        ),
      },
    })
  }

  removeEntry(sessionId: string, entryId: string): void {
    const key = sessionId || 'global'
    const list = this.state.itemsBySession[key]
    if (!list) return
    this.setState({
      itemsBySession: {
        ...this.state.itemsBySession,
        [key]: list.filter((item) => item.id !== entryId),
      },
    })
  }

  clearSession(sessionId: string): void {
    const key = sessionId || 'global'
    const next = { ...this.state.itemsBySession }
    delete next[key]
    this.setState({ itemsBySession: next })
  }

  reset(): void {
    this.setState({
      itemsBySession: {},
      pendingRequest: null,
      nextRequestId: 1,
    })
  }
}

export const dbxSidecarStore = new DbxSidecarStore()

export function useDbxSidecarStore<T>(selector: (state: DbxSidecarSnapshot) => T): T {
  return useSyncExternalStore(
    dbxSidecarStore.subscribe,
    () => selector(dbxSidecarStore.getState()),
    () => selector(dbxSidecarStore.getState()),
  )
}

export function openDbxWorkbench(
  ctx: AstrorderPluginContext,
  sessionId?: string,
  agentId?: string,
  connectionId?: string,
): void {
  ctx.api.openDatabaseWorkbench(DBX_MEDIA_TYPE, DBX_PLUGIN_ID, sessionId, agentId, connectionId)
}

export async function readSqlText(
  ctx: AstrorderPluginContext,
  path: string,
  sessionId?: string,
  connectionId?: string,
): Promise<string> {
  const query = new URLSearchParams({ path })
  if (sessionId) query.set('session_id', sessionId)
  if (connectionId) query.set('connection_id', connectionId)
  const res = await ctx.api.fetch(`/api/v1/files/raw?${query.toString()}`, {
    headers: { Accept: 'text/plain, application/sql, */*' },
  })
  if (!res.ok) {
    throw new Error(`无法读取 SQL 文件（HTTP ${res.status}）`)
  }
  const bytes = new Uint8Array(await res.arrayBuffer())
  const encoding =
    bytes[0] === 0xff && bytes[1] === 0xfe
      ? 'utf-16le'
      : bytes[0] === 0xfe && bytes[1] === 0xff
        ? 'utf-16be'
        : 'utf-8'
  return new TextDecoder(encoding).decode(bytes)
}

export async function openSqlFileInDbx(
  ctx: AstrorderPluginContext,
  params: OpenSqlFileParams,
): Promise<void> {
  const cleanPath = stripSqlLocationSuffix(params.path)
  if (!cleanPath) {
    ctx.api.notify({ color: 'red', message: '无法识别有效的 SQL 文件路径' })
    return
  }
  const sessionId = params.session?.id || params.sessionId || 'global'
  const agentId = params.session?.agent_id || params.agentId || ''
  const connectionId = params.session?.connection_id || params.connectionId || undefined
  const filename = extractSqlFileName(cleanPath)
  const line = params.line ?? extractSqlLine(params.path)

  let sqlText = params.content
  if (sqlText === undefined) {
    try {
      sqlText = await readSqlText(
        ctx,
        cleanPath,
        sessionId === 'global' ? undefined : sessionId,
        connectionId,
      )
    } catch (err) {
      ctx.api.notify({
        color: 'red',
        title: '读取 SQL 失败',
        message: err instanceof Error ? err.message : '请确认文件存在且有权限读取',
      })
      return
    }
  }

  dbxSidecarStore.enqueueSql(
    sessionId,
    {
      path: cleanPath,
      filename,
      content: sqlText,
      line,
      agentId,
      connectionId,
    },
    { forceNew: params.forceNew },
  )

  openDbxWorkbench(ctx, sessionId, agentId, connectionId)
}

function hashSqlSnippet(value: string): string {
  let hash = 2166136261
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0).toString(36)
}

export function openSqlSnippetInDbx(
  ctx: AstrorderPluginContext,
  params: OpenSqlSnippetParams,
): void {
  const sqlText = params.sql.replace(/\r\n/g, '\n').trim()
  if (!sqlText) {
    ctx.api.notify({ color: 'yellow', message: 'SQL 代码块为空' })
    return
  }
  const sessionId = params.session?.id || params.sessionId || 'global'
  const agentId = params.session?.agent_id || params.agentId || ''
  const connectionId = params.session?.connection_id || params.connectionId || undefined
  const hash = hashSqlSnippet(sqlText)
  const filename = params.filename?.trim() || buildSqlSnippetFilename(sqlText, hash)

  dbxSidecarStore.enqueueSql(
    sessionId,
    {
      path: `inline-sql:${hash}`,
      filename,
      content: sqlText,
      isSnippet: true,
      agentId,
      connectionId,
    },
    { forceNew: params.forceNew },
  )

  openDbxWorkbench(ctx, sessionId, agentId, connectionId)
}
