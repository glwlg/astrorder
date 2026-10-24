import { ApiError } from '../../api/client'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { DraftState } from '../../domain/types'
import { useAstrorderStore } from '../../state/store'
import { draftShelfStorage, draftStorage } from './draftStorage'

export function isUnsentError(error: unknown): boolean {
  return error instanceof ApiError && (error.code === 'command_rejected' || /daemon command was rejected before a confirmed receipt/.test(error.detail) || error.status >= 400 && error.status < 500 && error.status !== 408)
}

export function sendErrorMessage(error: unknown): string {
  const detail = error instanceof Error ? error.message : '提交失败，请检查服务连接。'
  if (/\bsession_busy\b/.test(detail)) return 'Hermes 会话仍在忙碌，暂时无法切换模型；消息未发送，请等待当前任务结束后重试。'
  if (/rejected before a confirmed receipt/.test(detail)) return '原生服务拒绝了本次请求，消息未发送，请稍后重试。'
  if (error instanceof TypeError && /fetch|network|connection/i.test(detail)) return '网络连接中断，发送结果尚未确认；内容已保留，请先核对发送结果。'
  return detail
}

export async function restoreUnsentDraft(agentId: string, sessionId: string, original: DraftState): Promise<string> {
  const key = scopeKey(agentId, sessionId)
  const store = useAstrorderStore.getState()
  const current = store.drafts[key]
  if (!current || !current.text.trim() && !current.attachments.length && !current.sessionRefs?.length) {
    store.setDraft(agentId, sessionId, original)
    await draftStorage.save(key, original)
    return '内容已退回输入框。'
  }
  const entries = await draftShelfStorage.load(key)
  await draftShelfStorage.save(key, [...entries, { id: newCommandId(), savedAt: new Date().toISOString(), draft: original }])
  return '原消息已保存到草稿箱，当前输入已保留。'
}

export function removeUnsentMessage(agentId: string, sessionId: string, commandId: string) {
  const key = scopeKey(agentId, sessionId)
  useAstrorderStore.getState().removeOutbox(agentId, sessionId, commandId)
  useAstrorderStore.setState(current => ({ messages: { ...current.messages, [key]: Object.fromEntries(Object.entries(current.messages[key] || {}).filter(([id]) => id !== `optimistic-${commandId}`)) } }))
}
