import { useRef, useState } from 'react'
import { IconMessageQuestion } from '@tabler/icons-react'
import { api } from '../api/client'
import type { Command, CommandPayload, Message } from '../domain/types'
import { useAstrorderStore } from '../state/store'

export function isAsyncQuestion(message: Message): boolean {
 return message.role === 'assistant' && message.delivery === 'async' && Boolean(message.questions?.length)
}

export function hasLaterNativeInput(messages: Message[], index: number): boolean {
 const question = messages[index]
 return messages.slice(index + 1).some(m => m.agent_id === question.agent_id && m.session_id === question.session_id && m.role === 'user' && !m.id.startsWith('optimistic-') && Boolean(m.text.trim() || m.attachments.length))
}

/** Async questions accept ordinary user input; they have no pending RPC ID. */
export function AsyncUserInputCard({ message, commands = [], continued = false, readOnly = false }: { message: Message; commands?: Command[]; continued?: boolean; readOnly?: boolean }) {
 const questions = message.questions || []
 const [values, setValues] = useState<Record<number, string>>({})
 const [customValues, setCustomValues] = useState<Record<number, string>>({})
 const [busy, setBusy] = useState(false)
 const [error, setError] = useState('')
 const [receipt, setReceipt] = useState<Command | null>(null)
 const submission = useRef<CommandPayload | null>(null)
 const saved = receipt || commands.find(c => c.agent_id === message.agent_id && c.session_id === message.session_id && c.action === 'send' && c.target_id === message.id && c.state !== 'failed' && c.state !== 'cancelled')
 const sent = Boolean(saved && ['accepted', 'running', 'completed'].includes(saved.state))
 const uncertain = Boolean(submission.current || saved && !['failed', 'cancelled'].includes(saved.state))
 const valid = questions.every((_, i) => values[i]?.trim())
 const submit = async () => {
  if (busy || sent || continued || readOnly) return
  setBusy(true); setError('')
  try {
   if (uncertain) {
    const result = await api.getCommands(message.session_id, message.agent_id)
    useAstrorderStore.getState().mergeCommands(result.items)
    const found = result.items.find(c => c.id === (submission.current?.id || saved?.id))
    if (found) {
     setReceipt(found)
     if (['failed', 'cancelled'].includes(found.state)) { submission.current = null; throw new Error(found.error || '回答未发送，可修改后重新发送。') }
     if (['accepted', 'running', 'completed'].includes(found.state)) return
     throw new Error('回答发送结果尚未确认，请稍后检查。')
    }
    // An absent receipt does not prove that native dispatch never happened.
    throw new Error('回答回执暂不可读，请稍后检查。')
   }
   if (!submission.current) {
    const priorFailure = receipt || commands.find(c => c.target_id === message.id && c.agent_id === message.agent_id && c.session_id === message.session_id)
    submission.current = { id: priorFailure ? crypto.randomUUID() : `async-answer:${message.id}`, agent_id: message.agent_id, session_id: message.session_id, action: 'send', text: questions.length === 1 ? values[0].trim() : questions.map((q, i) => `${q.title}\n${values[i].trim()}`).join('\n\n'), attachment_ids: [], target_id: message.id }
   }
   const command = await api.createCommand(submission.current)
   setReceipt(command)
   useAstrorderStore.getState().mergeCommands([command])
   if (['failed', 'cancelled'].includes(command.state)) { submission.current = null; throw new Error(command.error || '回答未发送，可修改后重新发送。') }
   if (!['accepted', 'running', 'completed'].includes(command.state)) throw new Error('回答发送结果尚未确认，请稍后检查。')
   // Read native history without resending; its user item closes the card after reload.
   const history = await api.getMessages(message.session_id, message.agent_id).catch(() => null)
   if (history) useAstrorderStore.getState().mergeMessages(message.agent_id, message.session_id, history.items)
  } catch (cause) { setError(cause instanceof Error ? cause.message : '回答发送失败') }
  finally { setBusy(false) }
 }
 return <section className="user-input-card" aria-label="会话内异步询问">
  <div className="user-input-heading"><IconMessageQuestion size={16} aria-hidden /><span>Codex 向你提问</span></div>
  {continued || sent ? <><p className="user-input-status" role="status">{sent ? '回答已接收。' : '已有后续输入，此问题已结束。'}</p>{questions.map((q, i) => <p className="user-input-summary" key={i}>{q.title}</p>)}</> : <form onSubmit={e => { e.preventDefault(); void submit() }}>
   {questions.map((q, i) => <fieldset className="user-input-field" key={i} disabled={busy || uncertain || readOnly}>
    <legend>{q.title}</legend>
    <div className="user-input-options">{q.options?.map(option => <label className="user-input-option" key={option}><input type="radio" name={`${message.id}:${i}`} checked={values[i] === option} onChange={() => { setValues(v => ({ ...v, [i]: option })); setCustomValues(v => ({ ...v, [i]: '' })) }} /><span>{option}</span></label>)}</div>
    <label className="user-input-custom"><span>自行填写</span><input aria-label={`${q.title} 自行填写`} placeholder={q.options?.length ? '也可以输入其他回答…' : '输入你的回答…'} value={customValues[i] || ''} onChange={e => { setCustomValues(v => ({ ...v, [i]: e.target.value })); setValues(v => ({ ...v, [i]: e.target.value })) }} /></label>
   </fieldset>)}
   {error && <p className="user-input-error" role="alert">{error}</p>}
   <div className="user-input-actions"><button className="user-input-submit" type="submit" disabled={readOnly || busy || !valid && !uncertain}>{busy ? '发送中…' : uncertain ? '检查发送结果' : '发送回答'}</button></div>
  </form>}
 </section>
}
