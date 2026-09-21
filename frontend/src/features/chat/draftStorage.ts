import { useEffect, useRef, useState } from 'react'
import { scopeKey } from '../../domain/semantics'
import type { DraftState } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'

const DATABASE = 'astrorder:drafts'
const STORE = 'drafts'
const TEXT_PREFIX = 'astrorder:draft:'
const EMPTY_DRAFT: DraftState = { text: '', attachments: [], sessionRefs: [] }
const writes = new Map<string, Promise<void>>()

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
    const stored = await transact<DraftState | undefined>('readonly', store => store.get(key))
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
    return queueWrite(key, () => transact<IDBValidKey>('readwrite', store => store.put(draft, key)))
  },
  remove(key: string) {
    try { localStorage.removeItem(TEXT_PREFIX + key) } catch { /* IndexedDB removal still proceeds. */ }
    return queueWrite(key, () => transact<undefined>('readwrite', store => store.delete(key)))
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
