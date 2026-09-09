import type { Attachment, Command, CommandPayload, Session } from '../../domain/types'

export type OutboxEntry = {
  payload: CommandPayload
  files: File[]
  attachments: Attachment[]
  state: Command['state'] | 'submitting'
  sawRunning?: boolean
  error?: string
}
export interface OutboxStorage {
  load: () => Promise<OutboxEntry[]>
  save: (entries: OutboxEntry[]) => Promise<void>
}
type Transport = { send: (payload: CommandPayload) => Promise<Command>; upload: (file: File) => Promise<Attachment> }
const inFlight = (row: OutboxEntry) => ['submitting', 'received', 'accepted', 'running', 'unknown'].includes(row.state)

/** Persistent command receipts, never optimistic transcript messages. */
export class MobileOutbox {
  entries: OutboxEntry[] = []
  private serial: Promise<unknown> = Promise.resolve()
  private listeners = new Set<() => void>()
  private storage: OutboxStorage
  private transport: Transport
  constructor(storage: OutboxStorage, transport: Transport) { this.storage = storage; this.transport = transport }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  snapshot = () => this.entries
  private run<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.serial.then(operation)
    this.serial = next.catch(() => undefined)
    return next
  }
  private async commit(rows: OutboxEntry[]) {
    await this.storage.save(rows)
    this.entries = rows
    this.listeners.forEach(listener => listener())
  }
  load() {
    return this.run(async () => {
      const rows = await this.storage.load()
      await this.commit(rows.map(row => row.state === 'submitting' ? { ...row, state: 'unknown' } : row))
    })
  }
  enqueue(payload: CommandPayload, files: File[]) {
    return this.run(() => this.commit([...this.entries, { payload, files, attachments: [], state: 'queued' }]))
  }
  flush(agentId: string, sessionId: string) {
    return this.run(async () => {
      const scoped = this.entries.filter(row => row.payload.agent_id === agentId && row.payload.session_id === sessionId)
      if (scoped.some(inFlight)) return
      const queued = scoped[0]
      if (!queued || queued.state !== 'queued') return
      let entry = queued
      const update = async (next: OutboxEntry) => {
        await this.commit(this.entries.map(row => row.payload.id === entry.payload.id && row.payload.agent_id === agentId && row.payload.session_id === sessionId ? next : row))
      }
      try {
        while (entry.files.length) {
          const attachment = await this.transport.upload(entry.files[0])
          entry = { ...entry, files: entry.files.slice(1), attachments: [...entry.attachments, attachment], payload: { ...entry.payload, attachment_ids: [...entry.payload.attachment_ids, attachment.id] } }
          await update(entry)
        }
      } catch (error) {
        await update({ ...entry, state: 'failed', error: error instanceof Error ? error.message : '附件上传未完成' })
        return
      }
      // received is not acceptance. Persist an uncertain boundary before the HTTP write.
      await update({ ...entry, state: 'submitting' })
      try {
        const result = await this.transport.send(entry.payload)
        if (result.id !== entry.payload.id || result.agent_id !== agentId || result.session_id !== sessionId || result.action !== 'send') throw new Error('命令回执身份不匹配')
        if (result.state === 'completed') await this.commit(this.entries.filter(row => !(row.payload.id === entry.payload.id && row.payload.agent_id === agentId && row.payload.session_id === sessionId)))
        else await update({ ...entry, state: result.state, error: result.error || undefined })
      } catch (error) {
        await update({ ...entry, state: 'unknown', error: error instanceof Error ? error.message : '提交结果未确认' })
      }
    })
  }
  remove(agentId: string, sessionId: string, id: string) {
    return this.run(() => this.commit(this.entries.filter(row => !(row.payload.id === id && row.payload.agent_id === agentId && row.payload.session_id === sessionId && ['queued', 'failed', 'cancelled'].includes(row.state)))))
  }
  retry(agentId: string, sessionId: string, id: string) {
    return this.run(() => this.commit(this.entries.map(row => row.payload.id === id && row.payload.agent_id === agentId && row.payload.session_id === sessionId && row.state === 'failed'
      ? { ...row, payload: { ...row.payload, id: crypto.randomUUID() }, state: 'queued', error: undefined, sawRunning: false }
      : row)))
  }
  reconcile(commands: Command[], sessions: Session[]) {
    return this.run(async () => {
      const rows = this.entries.flatMap(row => {
        const command = commands.find(c => c.id === row.payload.id && c.agent_id === row.payload.agent_id && c.session_id === row.payload.session_id && c.action === row.payload.action)
        if (command?.state === 'completed') return []
        const next = command ? { ...row, state: command.state, error: command.error || undefined } : row
        const session = sessions.find(s => s.id === row.payload.session_id && s.agent_id === row.payload.agent_id)
        if (['accepted', 'running'].includes(next.state)) {
          if (row.sawRunning && session?.status === 'idle') return []
          if (session?.status === 'running') return [{ ...next, sawRunning: true }]
        }
        return [next]
      })
      if (JSON.stringify(rows) !== JSON.stringify(this.entries)) await this.commit(rows)
    })
  }
}
