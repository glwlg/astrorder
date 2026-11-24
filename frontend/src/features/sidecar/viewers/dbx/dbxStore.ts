import { create } from 'zustand'
import { notifications } from '@mantine/notifications'
import { authenticatedApiFetch } from '../../../../api/client'
import type { Session } from '../../../../domain/types'
import { useSidecarStore } from '../../sidecarStore'
import { buildSqlSnippetFilename, extractSqlFileName, extractSqlLine, stripSqlLocationSuffix } from './sqlFormats'

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

interface DbxSidecarState {
  itemsBySession: Record<string, DbxSqlEntry[]>
  pendingRequest: DbxPendingInject | null
  nextRequestId: number
  enqueueSql: (
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
  ) => DbxSqlEntry
  requestInject: (sessionId: string, entryId: string, forceNew?: boolean) => void
  clearPendingRequest: (requestId: number) => void
  markEntryStatus: (sessionId: string, entryId: string, status: DbxSqlEntry['status'], detail?: string) => void
  toggleExecuted: (sessionId: string, entryId: string) => void
  removeEntry: (sessionId: string, entryId: string) => void
  clearSession: (sessionId: string) => void
}

const MAX_ITEMS_PER_SESSION = 40

function normalizePathKey(path: string): string {
  return path.trim().replace(/\\/g, '/').toLowerCase()
}

export const useDbxSidecarStore = create<DbxSidecarState>((set, get) => ({
  itemsBySession: {},
  pendingRequest: null,
  nextRequestId: 1,

  enqueueSql: (sessionId, input, options) => {
    const key = sessionId || 'global'
    const existingList = get().itemsBySession[key] || []
    const norm = normalizePathKey(input.path)
    const now = Date.now()
    const requestId = get().nextRequestId

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

    set({
      itemsBySession: { ...get().itemsBySession, [key]: nextList },
      pendingRequest: {
        requestId,
        sessionId: key,
        entryId: entry.id,
        forceNew: options?.forceNew,
      },
      nextRequestId: requestId + 1,
    })

    return entry
  },

  requestInject: (sessionId, entryId, forceNew) => {
    const key = sessionId || 'global'
    const requestId = get().nextRequestId
    set({
      pendingRequest: { requestId, sessionId: key, entryId, forceNew },
      nextRequestId: requestId + 1,
    })
  },

  clearPendingRequest: (requestId) => {
    const curr = get().pendingRequest
    if (curr && curr.requestId === requestId) {
      set({ pendingRequest: null })
    }
  },

  markEntryStatus: (sessionId, entryId, status, detail) => {
    const key = sessionId || 'global'
    const list = get().itemsBySession[key]
    if (!list) return
    set({
      itemsBySession: {
        ...get().itemsBySession,
        [key]: list.map((item) =>
          item.id === entryId ? { ...item, status, statusDetail: detail } : item,
        ),
      },
    })
  },

  toggleExecuted: (sessionId, entryId) => {
    const key = sessionId || 'global'
    const list = get().itemsBySession[key]
    if (!list) return
    set({
      itemsBySession: {
        ...get().itemsBySession,
        [key]: list.map((item) =>
          item.id === entryId ? { ...item, executed: !item.executed } : item,
        ),
      },
    })
  },

  removeEntry: (sessionId, entryId) => {
    const key = sessionId || 'global'
    const list = get().itemsBySession[key]
    if (!list) return
    set({
      itemsBySession: {
        ...get().itemsBySession,
        [key]: list.filter((item) => item.id !== entryId),
      },
    })
  },

  clearSession: (sessionId) => {
    const key = sessionId || 'global'
    const next = { ...get().itemsBySession }
    delete next[key]
    set({ itemsBySession: next })
  },
}))

async function readSqlText(path: string, sessionId?: string, connectionId?: string): Promise<string> {
  const query = new URLSearchParams({ path })
  if (sessionId) query.set('session_id', sessionId)
  if (connectionId) query.set('connection_id', connectionId)
  const res = await authenticatedApiFetch(`/api/v1/files/raw?${query.toString()}`, {
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

export async function openSqlFileInDbx(params: {
  path: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  content?: string
  line?: number
  forceNew?: boolean
}): Promise<void> {
  const cleanPath = stripSqlLocationSuffix(params.path)
  if (!cleanPath) {
    notifications.show({ color: 'red', message: '无法识别有效的 SQL 文件路径' })
    return
  }
  const sessionId = params.session?.id || params.sessionId || 'global'
  const agentId = params.session?.agent_id || params.agentId || ''
  const connectionId = params.session?.connection_id || params.connectionId
  const filename = extractSqlFileName(cleanPath)
  const line = params.line ?? extractSqlLine(params.path)

  let sqlText = params.content
  if (sqlText === undefined) {
    try {
      sqlText = await readSqlText(cleanPath, sessionId === 'global' ? undefined : sessionId, connectionId)
    } catch (err) {
      notifications.show({
        color: 'red',
        title: '读取 SQL 失败',
        message: err instanceof Error ? err.message : '请确认文件存在且有权限读取',
      })
      return
    }
  }

  useDbxSidecarStore.getState().enqueueSql(
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

  useSidecarStore
    .getState()
    .openDatabaseWorkbench('application/x-astrorder-dbx', 'dbx-viewer', sessionId, agentId, connectionId)
}

function hashSqlSnippet(value: string): string {
  let hash = 2166136261
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i)
    hash = Math.imul(hash, 16777619)
  }
  return (hash >>> 0).toString(36)
}

export function openSqlSnippetInDbx(params: {
  sql: string
  filename?: string
  session?: Pick<Session, 'id' | 'agent_id' | 'connection_id'> | null
  sessionId?: string
  agentId?: string
  connectionId?: string
  forceNew?: boolean
}): void {
  const sqlText = params.sql.replace(/\r\n/g, '\n').trim()
  if (!sqlText) {
    notifications.show({ color: 'yellow', message: 'SQL 代码块为空' })
    return
  }
  const sessionId = params.session?.id || params.sessionId || 'global'
  const agentId = params.session?.agent_id || params.agentId || ''
  const connectionId = params.session?.connection_id || params.connectionId
  const hash = hashSqlSnippet(sqlText)
  const filename = params.filename?.trim() || buildSqlSnippetFilename(sqlText, hash)

  useDbxSidecarStore.getState().enqueueSql(
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

  useSidecarStore
    .getState()
    .openDatabaseWorkbench('application/x-astrorder-dbx', 'dbx-viewer', sessionId, agentId, connectionId)
}
