import type { OutboxEntry, OutboxStorage } from './mobileOutbox'

const LEGACY_KEY = 'astrorder:mobile-outbox'
const STORE = 'outbox'
function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open('astrorder:mobile', 1)
    request.onupgradeneeded = () => request.result.createObjectStore(STORE)
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(new Error('无法打开本地消息队列，输入内容已保留'))
  })
}

export const mobileOutboxStorage: OutboxStorage = {
  async load() {
    const db = await open()
    try {
      const rows = await new Promise<OutboxEntry[] | undefined>((resolve, reject) => {
        const request = db.transaction(STORE, 'readonly').objectStore(STORE).get('entries')
        request.onsuccess = () => resolve(request.result)
        request.onerror = () => reject(request.error)
      })
      if (rows) return rows
      const legacy = JSON.parse(localStorage.getItem(LEGACY_KEY) || '[]')
      if (!Array.isArray(legacy)) throw new Error('旧消息队列格式异常；原数据未删除')
      return legacy.map(row => {
        if (!row?.payload?.id || !row?.payload?.agent_id || !row?.payload?.session_id) throw new Error('旧消息队列身份缺失；原数据未删除')
        // Old code did not persist the dispatch boundary; never blindly replay it.
        return { ...row, files: [], attachments: row.attachments || [], state: 'unknown' }
      })
    } finally { db.close() }
  },
  async save(rows) {
    const db = await open()
    try {
      await new Promise<void>((resolve, reject) => {
        const transaction = db.transaction(STORE, 'readwrite')
        transaction.objectStore(STORE).put(rows, 'entries')
        transaction.oncomplete = () => resolve()
        transaction.onerror = () => reject(new Error('本地队列未保存成功，输入内容已保留'))
        transaction.onabort = () => reject(new Error('本地队列保存被中断，输入内容已保留'))
      })
      try { localStorage.removeItem(LEGACY_KEY) } catch { /* Durable IndexedDB copy already committed. */ }
    } finally { db.close() }
  },
}
