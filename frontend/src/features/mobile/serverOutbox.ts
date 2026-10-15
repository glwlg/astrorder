import { api } from '../../api/client'
import type { Attachment, Command, CommandPayload, Session } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import type { OutboxEntry, OutboxStorage } from './mobileOutbox'
import { mobileOutboxStorage } from './mobileOutboxStorage'

/** Server queue with durable local staging while disconnected. */
export class ServerOutbox {
  private local: OutboxEntry[] = []
  private rows: OutboxEntry[] = []
  private source: unknown
  private loaded = false
  private listeners = new Set<() => void>()
  private unsubscribe?: () => void
  private storage: OutboxStorage
  constructor(storage: OutboxStorage = mobileOutboxStorage) { this.storage = storage }
  private refresh = () => {
    const commands = useAstrorderStore.getState().commands
    if (this.source === commands) return
    this.source = commands
    const server = Object.values(commands).filter(c => c.action === 'enqueue' && ['queued', 'received', 'failed', 'unknown'].includes(c.state))
    this.rows = [
      ...this.local.filter(row => !Object.values(commands).some(c => c.id === row.payload.id && c.agent_id === row.payload.agent_id && c.session_id === row.payload.session_id)),
      ...server.map(command => ({ payload: { id: command.id, agent_id: command.agent_id, session_id: command.session_id, action: command.action, text: command.text, attachment_ids: command.attachments.map(a => a.id), target_id: command.target_id ?? null }, files: [], attachments: command.attachments, state: command.state, error: command.error || undefined })),
    ]
    this.listeners.forEach(listener => listener())
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    if (!this.unsubscribe) this.unsubscribe = useAstrorderStore.subscribe(this.refresh)
    return () => { this.listeners.delete(listener); if (!this.listeners.size) { this.unsubscribe?.(); this.unsubscribe = undefined } }
  }
  snapshot = () => { this.refresh(); return this.rows }
  private merge(command: Command) { useAstrorderStore.getState().mergeCommands([command]); this.refresh(); return command }
  private serial: Promise<unknown> = Promise.resolve()
  private run<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.serial.then(operation)
    this.serial = next.catch(() => undefined)
    return next
  }
  private async saveLocal(rows: OutboxEntry[]) {
    await this.storage.save(rows)
    this.local = rows
    this.source = undefined
    this.refresh()
  }
  async load() {
    await this.run(async () => {
      if (this.loaded) return
      this.local = (await this.storage.load()).map(row => row.state === 'submitting' ? { ...row, state: 'unknown' as const } : row)
      this.loaded = true
      this.source = undefined
      this.refresh()
    })
  }
  private async persistRow(row: OutboxEntry) {
    let entry = row
    while (entry.files.length) {
      let attachment: Attachment
      try { attachment = await api.uploadAttachment(entry.files[0]) } catch (error) {
        // The staged item already owns the draft; keep its identity and progress.
        const next = { ...entry, error: error instanceof Error ? error.message : '附件上传中断，等待恢复连接' }
        await this.saveLocal(this.local.map(item => item === entry ? next : item))
        return
      }
      const next = { ...entry, files: entry.files.slice(1), attachments: [...entry.attachments, attachment], payload: { ...entry.payload, attachment_ids: [...new Set([...entry.payload.attachment_ids, attachment.id])] } }
      await this.saveLocal(this.local.map(item => item === entry ? next : item))
      entry = next
    }
    const submitting = { ...entry, state: 'unknown' as const, error: undefined }
    await this.saveLocal(this.local.map(item => item === entry ? submitting : item))
    try {
      this.merge(await api.createCommand(submitting.payload))
      await this.saveLocal(this.local.filter(item => item !== submitting))
    } catch (error) {
      // Repeating enqueue with the exact payload only reads its durable receipt.
      // It never invokes native Send; retain the identity across reconnects.
      const next = { ...submitting, error: error instanceof Error ? error.message : '队列提交结果待核对' }
      await this.saveLocal(this.local.map(item => item === submitting ? next : item))
    }
  }
  enqueue(payload: CommandPayload, files: File[], attachments: Attachment[] = []) {
    return this.run(async () => {
      if (!this.loaded) { this.local = await this.storage.load(); this.loaded = true }
      const row: OutboxEntry = { payload: { ...payload, action: 'enqueue', attachment_ids: [...new Set([...payload.attachment_ids, ...attachments.map(item => item.id)])] }, files, attachments, state: 'queued' }
      await this.saveLocal([...this.local, row])
      if (useAstrorderStore.getState().connection === 'connected') await this.persistRow(row)
    })
  }
  migrate() {
    return this.run(async () => {
      if (useAstrorderStore.getState().connection !== 'connected') return
      // Only undispatched local work can migrate. Unknown direct receipts stay put.
      for (const row of [...this.local]) {
        if (row.state !== 'queued' && !(row.state === 'unknown' && row.payload.action === 'enqueue')) continue
        const known = Object.values(useAstrorderStore.getState().commands).find(c => c.id === row.payload.id && c.agent_id === row.payload.agent_id && c.session_id === row.payload.session_id)
        if (known) await this.saveLocal(this.local.filter(other => other !== row))
        else {
          const entry = { ...row, payload: { ...row.payload, action: 'enqueue' as const } }
          await this.saveLocal(this.local.map(other => other === row ? entry : other))
          await this.persistRow(entry)
        }
      }
    })
  }
  async flush(agentId: string, sessionId: string, commandId?: string) {
    await this.load()
    // Explicit delivery also migrates legacy staging before claiming it.
    if (commandId && useAstrorderStore.getState().connection !== 'connected') throw new Error('连接恢复后才能发送待发消息')
    await this.migrate()
    // Automatic dispatch belongs solely to the App, regardless of mounted pages.
    if (!commandId) return
    const entry = this.snapshot().find(row => row.payload.id === commandId && row.payload.agent_id === agentId && row.payload.session_id === sessionId)
    if (!entry || entry.state !== 'queued') return
    const command = this.merge(await api.sendQueuedCommand(commandId, agentId, sessionId))
    return { command, attachments: command.attachments }
  }
  async remove(agentId: string, sessionId: string, id: string) {
    return this.run(async () => {
      const local = this.local.find(row => row.payload.id === id && row.payload.agent_id === agentId && row.payload.session_id === sessionId)
      const known = Object.values(useAstrorderStore.getState().commands).find(c => c.id === id && c.agent_id === agentId && c.session_id === sessionId)
      if (local && ['queued','failed'].includes(local.state) && !known) await this.saveLocal(this.local.filter(row => row !== local))
      else {
        this.merge(await api.deleteQueuedCommand(id, agentId, sessionId))
        if (local) await this.saveLocal(this.local.filter(row => row !== local))
      }
    })
  }
  async updateText(agentId: string, sessionId: string, id: string, text: string) {
    return this.run(async () => {
      const local = this.local.find(row => row.payload.id === id && row.payload.agent_id === agentId && row.payload.session_id === sessionId)
      if (local && local.state === 'queued') await this.saveLocal(this.local.map(row => row === local ? { ...row, payload: { ...row.payload, text } } : row))
      else this.merge(await api.editQueuedCommand(id, agentId, sessionId, text))
    })
  }
  async retry(agentId: string, sessionId: string, id: string) {
    const entry = this.snapshot().find(row => row.payload.id === id && row.payload.agent_id === agentId && row.payload.session_id === sessionId && row.state === 'failed')
    if (!entry) return null
    const nextId = crypto.randomUUID()
    await this.enqueue({ ...entry.payload, id: nextId }, entry.files, entry.attachments)
    await this.remove(agentId, sessionId, id)
    return nextId
  }
  async reconcile(commands: Command[], _sessions: Session[]) {
    const current = useAstrorderStore.getState().commands
    if (commands.some(command => !Object.values(current).some(row => row === command))) useAstrorderStore.getState().mergeCommands(commands)
  }
}

const sharedOutboxes = new WeakMap<OutboxStorage, ServerOutbox>()
export function sharedServerOutbox(storage: OutboxStorage = mobileOutboxStorage) {
  let outbox = sharedOutboxes.get(storage)
  if (!outbox) { outbox = new ServerOutbox(storage); sharedOutboxes.set(storage, outbox) }
  return outbox
}
