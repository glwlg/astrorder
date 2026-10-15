import { useRef, useState } from 'react'
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
 return <section className="approval-card user-input-card" aria-label="会话内询问" style={{ padding: 14, border: '1px solid var(--mantine-color-default-border, #ddd)', borderRadius: 10 }}>
  <strong>Codex 等待你的回答</strong>
  {sent ? <p role="status">回答已发送，等待 Codex 继续。</p> : <form onSubmit={event => { event.preventDefault(); void submit(false) }}>
   {questions.map(q => <fieldset key={q.id} disabled={busy || Boolean(submission.current)} style={{ border: 0, padding: '12px 0', margin: 0, minWidth: 0 }}>
    <legend style={{ fontWeight: 600 }}>{q.question}</legend>
    {q.options?.map(option => <label key={option.label} style={{ display: 'block', padding: '6px 0', overflowWrap: 'anywhere' }}>
     <input type="radio" name={`${approval.id}:${q.id}`} checked={choices[q.id] === option.label} onChange={() => setChoices(current => ({ ...current, [q.id]: option.label }))} /> {option.label}{option.description && <small style={{ display: 'block', paddingLeft: 20 }}>{option.description}</small>}
    </label>)}
    {q.options?.length && q.isOther && <label style={{ display: 'block' }}><input type="radio" name={`${approval.id}:${q.id}`} checked={choices[q.id] === '__other'} onChange={() => setChoices(current => ({ ...current, [q.id]: '__other' }))} /> 自行填写</label>}
    {(!q.options?.length || choices[q.id] === '__other') && <input aria-label={q.question} type={q.isSecret ? 'password' : 'text'} autoComplete="off" value={values[q.id] || ''} onChange={event => setValues(current => ({ ...current, [q.id]: event.target.value }))} style={{ width: '100%', boxSizing: 'border-box', marginTop: 8, padding: 10, borderRadius: 6, border: '1px solid #aaa', color: 'inherit', background: 'transparent' }} />}
   </fieldset>)}
   {error && <p role="alert">{error}</p>}
   <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
    <button type="button" disabled={busy || Boolean(submission.current)} onClick={() => void submit(true)}>跳过</button>
    <button type="submit" disabled={busy || (!valid && !submission.current)}>{busy ? '发送中…' : submission.current ? '重试相同回答' : '发送回答'}</button>
   </div>
  </form>}
 </section>
}
