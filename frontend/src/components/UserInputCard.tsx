import { useRef, useState } from 'react'
import { IconMessageQuestion } from '@tabler/icons-react'
import type { Approval } from '../domain/types'
import { api } from '../api/client'

export function isUserInput(approval: Approval): boolean {
 return (approval.data || approval as unknown as Record<string, unknown>).method === 'item/tool/requestUserInput'
}
interface Question { id: string; header: string; question: string; isOther?: boolean; isSecret?: boolean; options?: { label: string; description: string }[] | null }

/** The answer is sent to the pending native request, never through chat send. */
export function UserInputCard({ approval }: { approval: Approval }) {
 const data = approval.data || approval as unknown as Record<string, unknown>
 const questions = (Array.isArray(data.questions) ? data.questions : []) as Question[]
 const [values, setValues] = useState<Record<string, string>>({})
 const [choices, setChoices] = useState<Record<string, string>>({})
 const [busy, setBusy] = useState(false)
 const [sent, setSent] = useState(false)
 const [error, setError] = useState('')
 const submission = useRef<{ id: string; answers: Record<string, { answers: string[] }> } | null>(null)
 const valid = questions.length > 0 && questions.every(q => (choices[q.id] === '__other' || !q.options?.length ? values[q.id]?.trim() : choices[q.id]))
 const submit = async (skip: boolean) => {
  if (busy || sent || !approval.agent_id || !approval.session_id) return
  if (!submission.current) {
   const answers: Record<string, { answers: string[] }> = {}
   if (!skip) for (const q of questions) answers[q.id] = { answers: [choices[q.id] === '__other' || !q.options?.length ? values[q.id] : choices[q.id]] }
   submission.current = { id: crypto.randomUUID(), answers }
  }
  setBusy(true); setError('')
  try {
   const response = await api.respondUserInput(approval.session_id, { ...submission.current, agent_id: approval.agent_id, request_id: approval.id })
   if (!response.accepted) throw new Error('回答尚未确认')
   setSent(true); setValues({}); setChoices({})
  } catch (cause) { setError(cause instanceof Error ? cause.message : '回答发送失败') }
  finally { setBusy(false) }
 }
 return <section className="user-input-card" aria-label="会话内询问">
  <div className="user-input-heading"><IconMessageQuestion size={16} aria-hidden /><span>Codex 等待你的回答</span></div>
  {sent ? <p className="user-input-status" role="status">回答已发送，等待 Codex 继续。</p> : <form onSubmit={event => { event.preventDefault(); void submit(false) }}>
   {questions.map(q => <fieldset className="user-input-field" key={q.id} disabled={busy || Boolean(submission.current)}>
    <legend>{q.question}</legend>
    <div className="user-input-options">{q.options?.map(option => <label className="user-input-option" key={option.label}>
     <input type="radio" name={`${approval.id}:${q.id}`} checked={choices[q.id] === option.label} onChange={() => setChoices(current => ({ ...current, [q.id]: option.label }))} /><span>{option.label}{option.description && <small>{option.description}</small>}</span>
    </label>)}
    {!!q.options?.length && q.isOther && <label className="user-input-option"><input type="radio" name={`${approval.id}:${q.id}`} checked={choices[q.id] === '__other'} onChange={() => setChoices(current => ({ ...current, [q.id]: '__other' }))} /><span>自行填写</span></label>}</div>
    {(!q.options?.length || choices[q.id] === '__other') && <div className="user-input-custom"><input aria-label={q.question} placeholder="输入你的回答…" type={q.isSecret ? 'password' : 'text'} autoComplete="off" value={values[q.id] || ''} onChange={event => setValues(current => ({ ...current, [q.id]: event.target.value }))} /></div>}
   </fieldset>)}
   {error && <p className="user-input-error" role="alert">{error}</p>}
   <div className="user-input-actions">
    <button className="user-input-skip" type="button" disabled={busy || Boolean(submission.current)} onClick={() => void submit(true)}>跳过</button>
    <button className="user-input-submit" type="submit" disabled={busy || (!valid && !submission.current)}>{busy ? '发送中…' : submission.current ? '重试相同回答' : '发送回答'}</button>
   </div>
  </form>}
 </section>
}
