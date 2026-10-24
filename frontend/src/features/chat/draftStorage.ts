import { useEffect, useRef, useState } from 'react'
import { scopeKey } from '../../domain/semantics'
import type { DraftState } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { ApiError, authenticatedApiFetch, request } from '../../api/client'

const DATABASE = 'astrorder:drafts'
const STORE = 'drafts'
const TEXT_PREFIX = 'astrorder:draft:'
const EMPTY_DRAFT: DraftState = { text: '', attachments: [], sessionRefs: [] }
const writes = new Map<string, Promise<void>>()

function hasIndexedDb(): boolean {
  return typeof indexedDB !== 'undefined'
}

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE, 1)
    request.onupgradeneeded = () => request.result.createObjectStore(STORE)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

async function transact<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open()
  try {
    return await new Promise<T>((resolve, reject) => {
      const transaction = db.transaction(STORE, mode)
      const request = run(transaction.objectStore(STORE))
      let result: T
      request.onsuccess = () => { result = request.result }
      request.onerror = () => reject(request.error)
      transaction.oncomplete = () => resolve(result)
      transaction.onerror = () => reject(transaction.error)
      transaction.onabort = () => reject(transaction.error)
    })
  } finally {
    db.close()
  }
}

function queueWrite(key: string, write: () => Promise<unknown>): Promise<void> {
  const next = (writes.get(key) || Promise.resolve()).catch(() => undefined).then(write).then(() => undefined)
  writes.set(key, next)
  return next.finally(() => { if (writes.get(key) === next) writes.delete(key) })
}

export const draftStorage = {
  async load(key: string) {
    await writes.get(key)?.catch(() => undefined)
    const stored = hasIndexedDb() ? await transact<DraftState | undefined>('readonly', store => store.get(key)) : undefined
    let text: string | null = null
    try { text = localStorage.getItem(TEXT_PREFIX + key) } catch { /* IndexedDB remains authoritative. */ }
    if (!text) return stored
    try {
      const plain = JSON.parse(text) as Pick<DraftState, 'text' | 'sessionRefs'>
      return { ...(stored || EMPTY_DRAFT), ...plain }
    } catch {
      return stored
    }
  },
  save(key: string, draft: DraftState) {
    try { localStorage.setItem(TEXT_PREFIX + key, JSON.stringify({ text: draft.text, sessionRefs: draft.sessionRefs || [] })) } catch { /* IndexedDB still stores the complete draft. */ }
    return hasIndexedDb() ? queueWrite(key, () => transact<IDBValidKey>('readwrite', store => store.put(draft, key))) : Promise.resolve()
  },
  remove(key: string) {
    try { localStorage.removeItem(TEXT_PREFIX + key) } catch { /* IndexedDB removal still proceeds. */ }
    return hasIndexedDb() ? queueWrite(key, () => transact<undefined>('readwrite', store => store.delete(key))) : Promise.resolve()
  },
}

export interface StashedDraft {
  id: string
  savedAt: string
  draft: DraftState
}

// Kept separately from the active draft, so sending or clearing the composer
// never removes explicitly stashed text and File bytes.
const legacyDraftShelfStorage = {
  async load(key: string): Promise<StashedDraft[]> {
    if (!hasIndexedDb()) return []
    const storageKey = `shelf:${key}`
    await writes.get(storageKey)?.catch(() => undefined)
    return await transact<StashedDraft[] | undefined>('readonly', store => store.get(storageKey)) || []
  },
  save(key: string, entries: StashedDraft[]): Promise<void> {
    if (!hasIndexedDb()) return Promise.reject(new Error('无法暂存草稿：本地存储不可用，输入内容已保留。'))
    const storageKey = `shelf:${key}`
    return queueWrite(storageKey, async () => {
      // Materialize disk-backed Files before storing; WebView File handles may
      // otherwise stop being readable after a reload or source-file change.
      const snapshots = await Promise.all(entries.map(async entry => ({
        ...entry,
        draft: {
          ...entry.draft,
          attachments: await Promise.all(entry.draft.attachments.map(async item => ({
            ...item,
            file: item.attachment ? item.file : new File([await item.file.arrayBuffer()], item.file.name, { type: item.file.type, lastModified: item.file.lastModified }),
          }))),
        },
      })))
      await transact<IDBValidKey>('readwrite', store => store.put(snapshots, storageKey))
    })
  },
}

type StoredFile = { name: string; type: string; lastModified: number; data: string }
type StoredEntry = Omit<StashedDraft, 'draft'> & { draft: Omit<DraftState, 'attachments'> & { attachments: Array<Omit<DraftState['attachments'][number], 'file'> & { file: StoredFile }> } }
type ShelfResponse = { revision: number; entries: StoredEntry[] }
const revisions = new Map<string, number>()
const shelfPath = (key: string) => `/draft-shelf?scope=${encodeURIComponent(key)}`

async function encodeEntries(entries: StashedDraft[]): Promise<StoredEntry[]> {
  return Promise.all(entries.map(async row => ({ ...row, draft: { ...row.draft, attachments: await Promise.all(row.draft.attachments.map(async item => {
    let bytes: Uint8Array
    if (item.attachment) {
      const response = await authenticatedApiFetch(item.attachment.url)
      if (!response.ok) throw new Error('无法保存草稿附件，输入内容已保留。')
      bytes = new Uint8Array(await response.arrayBuffer())
    } else bytes = new Uint8Array(await item.file.arrayBuffer())
    let binary = ''
    for (let offset = 0; offset < bytes.length; offset += 32768) binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768))
    return { ...item, file: { name: item.file.name, type: item.file.type, lastModified: item.file.lastModified, data: btoa(binary) } }
  })) } })))
}

function decodeEntries(entries: StoredEntry[]): StashedDraft[] {
  return entries.map(row => ({ ...row, draft: { ...row.draft, attachments: row.draft.attachments.map(item => {
    const bytes = Uint8Array.from(atob(item.file.data), char => char.charCodeAt(0))
    return { ...item, file: new File([bytes], item.file.name, { type: item.file.type, lastModified: item.file.lastModified }) }
  }) } }))
}

export const draftShelfStorage = {
  async load(key: string): Promise<StashedDraft[]> {
    await writes.get(`remote-shelf:${key}`)?.catch(() => undefined)
    let result = await request<ShelfResponse>(shelfPath(key))
    const legacy = await legacyDraftShelfStorage.load(key)
    const imported = legacy.filter(row => !result.entries.some(saved => saved.id === row.id))
    if (imported.length) {
      try {
        result = await request<ShelfResponse>(shelfPath(key), { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ revision: result.revision, entries: [...await encodeEntries(imported), ...result.entries] }) })
      } catch (error) {
        // A second tab may have imported first; retain local bytes and let a
        // later load retry if any entries are still missing.
        if (!(error instanceof ApiError) || error.status !== 409) throw error
        result = await request<ShelfResponse>(shelfPath(key))
        if (imported.some(row => !result.entries.some(saved => saved.id === row.id))) throw error
      }
    }
    if (legacy.length) await legacyDraftShelfStorage.save(key, [])
    revisions.set(key, result.revision)
    return decodeEntries(result.entries)
  },
  save(key: string, entries: StashedDraft[]): Promise<void> {
    const revision = revisions.get(key)
    if (revision === undefined) return Promise.reject(new Error('草稿箱尚未载入，输入内容已保留。'))
    return queueWrite(`remote-shelf:${key}`, async () => {
      const result = await request<ShelfResponse>(shelfPath(key), { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ revision, entries: await encodeEntries(entries) }) })
      revisions.set(key, result.revision)
    })
  },
}

function hasContent(draft: DraftState | undefined): boolean {
  return Boolean(draft && (draft.text || draft.attachments.length || draft.sessionRefs?.length))
}

export function usePersistentDraft(agentId: string, sessionId: string) {
  const key = agentId && sessionId ? scopeKey(agentId, sessionId) : ''
  const draft = useAstrorderStore(state => state.drafts[key] || EMPTY_DRAFT)
  const [hydratedKey, setHydratedKey] = useState('')
  const latest = useRef(draft)
  latest.current = draft

  useEffect(() => {
    if (!key) return
    let mounted = true
    setHydratedKey('')
    void draftStorage.load(key).then(saved => {
      if (!mounted) return
      const current = useAstrorderStore.getState().drafts[key]
      if (saved && !hasContent(current)) useAstrorderStore.getState().setDraft(agentId, sessionId, saved)
      setHydratedKey(key)
    }).catch(error => {
      console.error('Unable to restore draft', error)
      if (mounted) setHydratedKey(key)
    })
    return () => { mounted = false }
  }, [agentId, key, sessionId])

  useEffect(() => {
    if (!key || hydratedKey !== key) return
    void (hasContent(draft) ? draftStorage.save(key, draft) : draftStorage.remove(key)).catch(error => console.error('Unable to save draft', error))
  }, [draft, hydratedKey, key])

  useEffect(() => {
    if (!key) return
    const save = () => { void (hasContent(latest.current) ? draftStorage.save(key, latest.current) : draftStorage.remove(key)).catch(error => console.error('Unable to save draft', error)) }
    window.addEventListener('beforeunload', save)
    window.addEventListener('pagehide', save)
    return () => {
      save()
      window.removeEventListener('beforeunload', save)
      window.removeEventListener('pagehide', save)
    }
  }, [key])

  return {
    draft,
    setDraft: (next: DraftState | ((current: DraftState) => DraftState)) => {
      const current = useAstrorderStore.getState().drafts[key] || EMPTY_DRAFT
      useAstrorderStore.getState().setDraft(agentId, sessionId, typeof next === 'function' ? next(current) : next)
    },
  }
}
